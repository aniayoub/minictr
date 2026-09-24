//go:build linux && integration && cgroup

package integration

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

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
