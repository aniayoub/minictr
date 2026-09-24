//go:build linux && integration

package integration

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

func TestUserNamespaceIsolation(t *testing.T) {
	testNamespaceIsolation(t, "user")
}

func testNamespaceIsolation(t *testing.T, namespace string) {

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
