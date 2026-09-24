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

func TestProcMountMatchPIDNamespace(t *testing.T) {
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
