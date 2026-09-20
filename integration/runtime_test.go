//go:build linux && integration

package integration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("integration test requires root")
	}
}

func requireRootfs(t *testing.T) string {
	t.Helper()

	rootfs := os.Getenv("MINICTR_TEST_ROOTFS")
	if rootfs == "" {
		t.Skip("MINICTR_TEST_ROOTFS is not set")
	}

	return rootfs
}

func buildMinictr(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "minictr")

	cmd := exec.Command(
		"go",
		"build",
		"-o",
		binary,
		"../cmd/minictr",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build minictr: %v\n%s", err, output)
	}

	return binary
}

func startSleepInMinictr(t *testing.T, cgConfig *struct{ key, value string }) (cmd *exec.Cmd) {
	rootfs := requireRootfs(t)
	binary := buildMinictr(t)

	if cgConfig != nil {
		cmd = exec.Command(
			binary,
			"run",
			rootfs,
			cgConfig.key,
			cgConfig.value,
			"--",
			"/bin/sleep",
			"30",
		)
	} else {

		cmd = exec.Command(
			binary,
			"run",
			rootfs,
			"--",
			"/bin/sleep",
			"30",
		)
	}

	// The test deliberately does not depend on runtime or workload output.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("start minictr: %v", err)
	}

	return cmd
}

func TestExitCode(t *testing.T) {
	requireRoot(t)

	rootfs := requireRootfs(t)
	binary := buildMinictr(t)

	cmd := exec.Command(
		binary,
		"run",
		rootfs,
		"--",
		"/bin/sh",
		"-c",
		"exit 42",
	)

	output, err := cmd.CombinedOutput()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf(
			"expected ExitError, got %T: %v\n%s",
			err,
			err,
			output,
		)
	}

	if got := exitErr.ExitCode(); got != 42 {
		t.Fatalf("exit code = %d, want 42\n%s", got, output)
	}
}

func TestUTSIsolation(t *testing.T) {
	testNamespaceIsolation(t, "uts")
}

func TestIPCNamespaceIsolation(t *testing.T) {
	testNamespaceIsolation(t, "ipc")
}

func TestMountNamespaceIsolation(t *testing.T) {
	testNamespaceIsolation(t, "mnt")
}

func TestPIDNamespaceIsolation(t *testing.T) {
	requireRoot(t)

	cmd := startSleepInMinictr(t, nil)

	t.Cleanup(func() {
		stopMinictr(t, cmd)
	})

	initPID, err := waitForDirectChild(cmd.Process.Pid, 5*time.Second)
	if err != nil {
		t.Fatalf("discover init PId: %v", err)
	}

	// Ensure that the init process is running in a different PID namespace than the host.
	testNamespaceMismatch(t, "pid", initPID)

	statusPath := fmt.Sprintf("/proc/%d/status", initPID)

	data, err := os.ReadFile((statusPath))
	if err != nil {
		t.Fatalf("read init status: %v:", err)
	}

	// Parse NSPid from the last namespacePids because
	// Because NSpid lists the PID as seen from the outer namespace through progressively nested PID namespaces.
	// The innermost value is therefore the container-visible PID.
	var nspid int
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				nspid, _ = strconv.Atoi(fields[len(fields)-1])
			}
		}
	}

	if nspid == 0 {
		t.Fatalf("failed to parse NSpid from init status")
	}

	if nspid != 1 {
		t.Fatalf("expected init NSPid to be 1, got %d", nspid)
	}

}

func TestProcMountMatchPIDNamespace(t *testing.T) {
	requireRoot(t)

	cmd := exec.Command(
		buildMinictr(t),
		"run",
		requireRootfs(t),
		"--",
		"/bin/sh",
		"-c",
		`test "$(readlink /proc/1/ns/pid)" = "$(readlink /proc/self/ns/pid)"`,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("proc does not match container PID namespace: %v\n%s", err, output)
	}
}

func TestMountBinds(t *testing.T) {
	requireRoot(t)

	// create a temporary source directory
	tempDir := t.TempDir()

	// create a test file inside the temporary source directory
	path := filepath.Join(tempDir, "test-data")
	if err := os.WriteFile(path, []byte("some content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Bind the temporary source directory into the container at /mnt/data
	bindStr := fmt.Sprintf("%s:/mnt/data", tempDir)
	cmd := exec.Command(
		buildMinictr(t),
		"run",
		requireRootfs(t),
		"--bind",
		bindStr,
		"--",
		"/bin/sh",
		"-c",
		`test -f /mnt/data/test-data &&  test "$(cat /mnt/data/test-data)" = "some content"`,
	)

	output, err := cmd.CombinedOutput()

	if err != nil {
		t.Fatalf("bind mount check failed: %v\n%s", err, output)
	}
}

func TestSignalForwarding(t *testing.T) {
	requireRoot(t)

	tests := []struct {
		name     string
		signal   syscall.Signal
		expected int
	}{
		{
			name:     "forward SIGTERM",
			signal:   syscall.SIGTERM,
			expected: 128 + int(syscall.SIGTERM),
		},
		{
			name:     "forward SIGINT",
			signal:   syscall.SIGINT,
			expected: 128 + int(syscall.SIGINT),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Start a container running "sleep 30" to keep it alive for inspection.
			cmd := startSleepInMinictr(t, nil)

			t.Cleanup(func() {
				stopMinictr(t, cmd)
			})

			// Wait until container is discovered to make sure the chain of processes is established.
			initPID, err := waitForDirectChild(cmd.Process.Pid, 5*time.Second)
			if err != nil {
				t.Fatalf("find container init: %v", err)
			}

			// wait until the workload is discovered to make sure the chain of processes is fully established.
			_, err = waitForDirectChild(initPID, 5*time.Second)
			if err != nil {
				t.Fatalf("find container workload: %v", err)
			}

			// Send the signal to the container process.
			err = cmd.Process.Signal(tt.signal)
			if err != nil {
				t.Fatalf("failed to send signal: %v", err)
			}

			// Reap the signal and ensure it is 128 + the signal.
			err = cmd.Wait()

			exitError, ok := err.(*exec.ExitError)

			if !ok {
				t.Fatalf("expected ExitError, got %T: %v", err, err)
			}

			if got := exitError.ExitCode(); got != tt.expected {
				t.Fatalf("exit code = %d, want %d\n%s", got, tt.expected, err)
			}

		})
	}

}

func TestCgroupPlacement(t *testing.T) {
	requireRoot(t)

	// Start a container running "sleep 30" to keep it alive for inspection.
	cmd := startSleepInMinictr(t, nil)

	t.Cleanup(func() {
		stopMinictr(t, cmd)
	})

	// Discover the supervisor's direct child from Linux process state.
	// That child is the re-exec'd minictr init process.
	supervisorPID := cmd.Process.Pid
	initPID, err := waitForDirectChild(supervisorPID, 5*time.Second)
	if err != nil {
		t.Fatalf("find container init: %v", err)
	}
	workloadPID, err := waitForDirectChild(initPID, 5*time.Second)
	if err != nil {
		t.Fatalf("find container workload: %v", err)
	}

	// Extract host, container and workload cgroup data.
	path := fmt.Sprintf("/proc/%d/cgroup", supervisorPID)
	hostCg, err := extractCgroupData(path)
	if err != nil {
		t.Fatalf("read host cgroup: %v", err)
	}

	path = fmt.Sprintf("/proc/%d/cgroup", workloadPID)
	workloadCg, err := extractCgroupData(path)
	if err != nil {
		t.Fatalf("read container workload cgroup: %v", err)
	}

	path = fmt.Sprintf("/proc/%d/cgroup", initPID)
	containerCg, err := extractCgroupData(path)
	if err != nil {
		t.Fatalf("read container cgroup: %v", err)
	}

	// Ensure host is separated from container and workload cgroups.
	if hostCg == containerCg {
		t.Fatalf("container init cgroup matches host cgroup")
	}

	if hostCg == workloadCg {
		t.Fatalf("container workload cgroup matches host cgroup")
	}

	if containerCg != workloadCg {
		t.Fatalf("container init cgroup does not match container workload cgroup")
	}
}

func TestCGroupConfiguration(t *testing.T) {
	requireRoot(t)

	tests := []struct {
		testName          string
		configName        string
		configFile        string
		configValueStr    string
		configActualValue string
	}{
		{
			testName:          "cpu limit",
			configName:        "--cpu",
			configFile:        "cpu.max",
			configValueStr:    "0.5",
			configActualValue: "50000 100000",
		},
		{
			testName:          "pid limit",
			configName:        "--pids",
			configFile:        "pids.max",
			configValueStr:    "50",
			configActualValue: "50",
		},
		{
			testName:          "memory limit",
			configName:        "--memory",
			configFile:        "memory.max",
			configValueStr:    "12M",
			configActualValue: "12582912",
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			// Test logic for each cgroup configuration goes here.

			// Start a container running "sleep 30" to keep it alive for inspection.
			cmd := startSleepInMinictr(t, &struct{ key, value string }{key: tt.configName, value: tt.configValueStr})

			t.Cleanup(func() {
				stopMinictr(t, cmd)
			})

			supervisorPID := cmd.Process.Pid

			// Discover the supervisor's direct child from Linux process state.
			// That child is the re-exec'd minictr init process.
			initPID, err := waitForDirectChild(supervisorPID, 5*time.Second)
			if err != nil {
				t.Fatalf("find container init: %v", err)
			}

			initPID, err = waitForDirectChild(initPID, 5*time.Second)
			if err != nil {
				t.Fatalf("find container workload: %v", err)
			}
			cgName, err := cgroupPathForPID(initPID)
			if err != nil {
				t.Fatalf("get cgroup path for init process: %v", err)
			}
			cgPath := fmt.Sprintf("/sys/fs/cgroup/%s/%s", cgName, tt.configFile)

			max, err := extractCgroupData(cgPath)
			if err != nil {
				t.Fatalf("extract cgroup data: %v", err)
			}

			if max != tt.configActualValue {
				t.Fatalf("expected cgroup value %s, got %s", tt.configActualValue, max)
			}
		})
	}
}

func testNamespaceIsolation(t *testing.T, namespace string) {
	requireRoot(t)

	// Start a container running "sleep 30" to keep it alive for inspection.
	cmd := startSleepInMinictr(t, nil)

	supervisorPID := cmd.Process.Pid

	t.Cleanup(func() {
		stopMinictr(t, cmd)
	})

	// Discover the supervisor's direct child from Linux process state.
	// That child is the re-exec'd minictr init process.
	initPID, err := waitForDirectChild(supervisorPID, 5*time.Second)
	if err != nil {
		t.Fatalf("find container init: %v", err)
	}

	testNamespaceMismatch(t, namespace, initPID)
}

func extractCgroupData(path string) (string, error) {
	cgroupPath := path
	cgroupData, err := os.ReadFile(cgroupPath)
	if err != nil {
		return "", fmt.Errorf("read container cgroup: %w", err)
	}

	if len(cgroupData) == 0 {
		return "", fmt.Errorf("container cgroup is empty")
	}

	// Make sure to trim any leading or trailing whitespace from the cgroup data.
	return strings.TrimSpace(string(cgroupData)), nil
}

func cgroupPathForPID(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", fmt.Errorf("read cgroup for pid %d: %w", pid, err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}

		// cgroup v2 unified hierarchy entry
		if parts[0] == "0" && parts[1] == "" {
			return parts[2], nil
		}
	}

	return "", fmt.Errorf("cgroup v2 path not found for pid %d", pid)
}

func testNamespaceMismatch(t *testing.T, namespace string, initPID int) {
	hostNS, err := os.Stat(fmt.Sprintf("/proc/self/ns/%s", namespace))
	if err != nil {
		t.Fatalf("stat host %s namespace: %v", namespace, err)
	}

	containerNSPath := fmt.Sprintf("/proc/%d/ns/%s", initPID, namespace)

	containerNS, err := os.Stat(containerNSPath)
	if err != nil {
		t.Fatalf("stat container %s namespace: %v", namespace, err)
	}

	if os.SameFile(hostNS, containerNS) {
		t.Fatalf(
			"container init PID %d shares the host %s namespace",
			initPID,
			namespace,
		)
	}
}

func waitForDirectChild(parentPID int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		children, err := directChildren(parentPID)
		if err != nil {
			return 0, err
		}

		switch len(children) {
		case 0:
			// Child may not have been created yet.
		case 1:
			return children[0], nil
		default:
			return 0, fmt.Errorf(
				"expected one direct child of PID %d, found %v",
				parentPID,
				children,
			)
		}

		time.Sleep(10 * time.Millisecond)
	}

	return 0, fmt.Errorf(
		"timed out waiting for child of PID %d",
		parentPID,
	)
}

func directChildren(parentPID int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	var children []int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			// Not a numeric /proc entry.
			continue
		}

		statusPath := filepath.Join(
			"/proc",
			entry.Name(),
			"status",
		)

		data, err := os.ReadFile(statusPath)
		if err != nil {
			// Processes can disappear while /proc is being scanned.
			continue
		}

		ppid, ok := parsePPID(string(data))
		if !ok {
			continue
		}

		if ppid == parentPID {
			children = append(children, pid)
		}
	}

	return children, nil
}

func parsePPID(status string) (int, bool) {
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0, false
		}

		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, false
		}

		return ppid, true
	}

	return 0, false
}

func stopMinictr(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd.Process == nil || cmd.ProcessState != nil {
		return
	}

	// Use the normal runtime shutdown path first so minictr gets a chance
	// to forward SIGTERM into the container.
	err := cmd.Process.Signal(syscall.SIGTERM)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("signal minictr: %v", err)
	}

	done := make(chan struct{})

	go func() {
		if cmd.Process == nil {
			return
		}
		// A signal-derived non-zero exit is expected during cleanup.
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		return

	case <-time.After(3 * time.Second):
		// Defensive fallback so a broken runtime does not hang the test suite.
		_ = cmd.Process.Kill()
		<-done
	}
}
