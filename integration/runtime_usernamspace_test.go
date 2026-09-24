//go:build linux && integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUserNamespaceIDs(t *testing.T) {
	rootfs := requireRootfs(t)
	binary := buildMinictr(t)

	tests := []struct {
		name string
		key  string
	}{
		{
			name: "UID",
			key:  "u",
		},
		{
			name: "GID",
			key:  "g",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(
				binary,
				"run",
				rootfs,
				"--",
				"/bin/sh",
				"-c",
				"test $(id -"+tt.key+") = 0",
			)

			output, err := cmd.CombinedOutput()

			if err != nil {
				t.Fatalf("%s is not 0: %s", tt.name, output)
			}

		})
	}
}

func TestUserNamespaceMappings(t *testing.T) {
	//	requireNonRoot(t)

	cmd := startSleepInMinictr(t, nil)

	t.Cleanup(func() {
		stopMinictr(t, cmd)
	})

	initPID, err := waitForDirectChild(cmd.Process.Pid, 5*time.Second)
	if err != nil {
		t.Fatalf("find container init: %v", err)
	}

	uidMap, err := readSingleIDMap(fmt.Sprintf("/proc/%d/uid_map", initPID))
	if err != nil {
		t.Fatalf("read UID map: %v", err)
	}

	if uidMap.inside != 0 {
		t.Fatalf("UID map inside ID = %d, want 0", uidMap.inside)
	}

	if uidMap.outside != os.Geteuid() {
		t.Fatalf(
			"UID map outside ID = %d, want %d",
			uidMap.outside,
			os.Geteuid(),
		)
	}

	if uidMap.length != 1 {
		t.Fatalf("UID map length = %d, want 1", uidMap.length)
	}

	gidMap, err := readSingleIDMap(fmt.Sprintf("/proc/%d/gid_map", initPID))
	if err != nil {
		t.Fatalf("read GID map: %v", err)
	}

	if gidMap.inside != 0 {
		t.Fatalf("GID map inside ID = %d, want 0", gidMap.inside)
	}

	if gidMap.outside != os.Getegid() {
		t.Fatalf(
			"GID map outside ID = %d, want %d",
			gidMap.outside,
			os.Getegid(),
		)
	}

	if gidMap.length != 1 {
		t.Fatalf("GID map length = %d, want 1", gidMap.length)
	}
}
func TestUserNamespaceHostCredentials(t *testing.T) {
	//	requireNonRoot(t)

	cmd := startSleepInMinictr(t, nil)

	t.Cleanup(func() {
		stopMinictr(t, cmd)
	})

	initPID, err := waitForDirectChild(cmd.Process.Pid, 5*time.Second)
	if err != nil {
		t.Fatalf("find container init: %v", err)
	}

	statusPath := fmt.Sprintf("/proc/%d/status", initPID)

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read init status: %v", err)
	}

	uids, err := parseStatusIDs(string(data), "Uid:")
	if err != nil {
		t.Fatalf("parse init UIDs: %v", err)
	}

	for _, uid := range uids {
		if uid != os.Geteuid() {
			t.Fatalf(
				"host-visible init UID = %d, want %d; all UIDs: %v",
				uid,
				os.Geteuid(),
				uids,
			)
		}
	}

	gids, err := parseStatusIDs(string(data), "Gid:")
	if err != nil {
		t.Fatalf("parse init GIDs: %v", err)
	}

	for _, gid := range gids {
		if gid != os.Getegid() {
			t.Fatalf(
				"host-visible init GID = %d, want %d; all GIDs: %v",
				gid,
				os.Getegid(),
				gids,
			)
		}
	}
}

func parseStatusIDs(status, prefix string) ([]int, error) {
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf(
				"%s expected 4 IDs, got line %q",
				prefix,
				line,
			)
		}

		ids := make([]int, 0, 4)

		for _, field := range fields[1:] {
			id, err := strconv.Atoi(field)
			if err != nil {
				return nil, fmt.Errorf(
					"parse %s value %q: %w",
					prefix,
					field,
					err,
				)
			}

			ids = append(ids, id)
		}

		return ids, nil
	}

	return nil, fmt.Errorf("%s not found in /proc status", prefix)
}

func TestUserNamespaceFileOwnership(t *testing.T) {
	//requireNonRoot(t)

	hostDir := t.TempDir()
	hostFile := filepath.Join(hostDir, "from-container")

	bindStr := fmt.Sprintf("%s:/mnt/data", hostDir)

	cmd := exec.Command(
		buildMinictr(t),
		"run",
		requireRootfs(t),
		"--bind",
		bindStr,
		"--",
		"/bin/sh",
		"-c",
		"touch /mnt/data/from-container",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"create file from container: %v\n%s",
			err,
			output,
		)
	}

	info, err := os.Stat(hostFile)
	if err != nil {
		t.Fatalf("stat container-created file: %v", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("file stat has unexpected type %T", info.Sys())
	}

	if stat.Uid != uint32(os.Geteuid()) {
		t.Fatalf(
			"host file UID = %d, want %d",
			stat.Uid,
			os.Geteuid(),
		)
	}

	if stat.Gid != uint32(os.Getegid()) {
		t.Fatalf(
			"host file GID = %d, want %d",
			stat.Gid,
			os.Getegid(),
		)
	}
}

type idMap struct {
	inside  int
	outside int
	length  int
}

func readSingleIDMap(path string) (idMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return idMap{}, err
	}

	fields := strings.Fields(string(data))
	if len(fields) != 3 {
		return idMap{}, fmt.Errorf(
			"expected one ID mapping with 3 fields, got %q",
			strings.TrimSpace(string(data)),
		)
	}

	inside, err := strconv.Atoi(fields[0])
	if err != nil {
		return idMap{}, fmt.Errorf("parse inside ID: %w", err)
	}

	outside, err := strconv.Atoi(fields[1])
	if err != nil {
		return idMap{}, fmt.Errorf("parse outside ID: %w", err)
	}

	length, err := strconv.Atoi(fields[2])
	if err != nil {
		return idMap{}, fmt.Errorf("parse mapping length: %w", err)
	}

	return idMap{
		inside:  inside,
		outside: outside,
		length:  length,
	}, nil
}
