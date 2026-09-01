//go:build linux

package cgroup

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

const cgroupRoot = "/sys/fs/cgroup"

func requireCgroupV2(t *testing.T) {
	t.Helper()

	if _, err := os.Stat(cgroupRoot + "/cgroup.controllers"); err != nil {
		t.Fatalf("cgroup v2 is required: %v", err)
	}

}

func newTestCgroup(t *testing.T) *Cgroup {
	t.Helper()

	requireCgroupV2(t)

	name := fmt.Sprintf("minictr-test-%d-%d", os.Getpid(), time.Now().UnixNano())

	cg, err := Create(name)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("insufficient permissions to create cgroup")
		}
		t.Fatalf("create test cgroup: %v", err)
	}

	t.Cleanup(func() {
		if err := cg.Remove(); err != nil {
			t.Errorf("remove test cgroup: %v", err)
		}
	})

	return cg
}

func TestCgroupCreateAndRemove(t *testing.T) {
	cg := newTestCgroup(t)

	if _, err := os.Stat(cg.Path); err != nil {
		t.Fatalf("cgroup path does not exist: %v", err)
	}
}
