package cgroup

import (
	"fmt"
	"minictr/internal/config"
	"os"
	"path/filepath"
	"strconv"
)

const root = "/sys/fs/cgroup"

type Cgroup struct {
	Path string
}

func Create(name string) (*Cgroup, error) {
	path := filepath.Join(root, name)

	if err := os.Mkdir(path, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup directory: %w", err)
	}

	return &Cgroup{Path: path}, nil
}

func (c *Cgroup) SetPidsMax(max int64) error {
	path := filepath.Join(c.Path, "pids.max")

	if err := os.WriteFile(
		path,
		[]byte(strconv.FormatInt(max, 10)),
		0644,
	); err != nil {
		return fmt.Errorf("set pids.max: %w", err)
	}

	return nil
}

func (c *Cgroup) SetMemoryMax(bytes int64) error {
	path := filepath.Join(c.Path, "memory.max")

	if err := os.WriteFile(
		path,
		[]byte(strconv.FormatInt(bytes, 10)),
		0644,
	); err != nil {
		return fmt.Errorf("set memory.max: %w", err)
	}

	return nil
}

func (c *Cgroup) SetCpuMax(max int64) error {
	path := filepath.Join(c.Path, "cpu.max")

	if err := os.WriteFile(
		path,
		[]byte(strconv.FormatInt(max, 10)+" "+strconv.FormatInt(config.CPUPeriodMicros, 10)),
		0644,
	); err != nil {
		return fmt.Errorf("set cpu.max: %w", err)
	}

	return nil
}

func (c *Cgroup) AddProcess(pid int) error {
	path := filepath.Join(c.Path, "cgroup.procs")

	if err := os.WriteFile(
		path,
		[]byte(strconv.Itoa(pid)),
		0644,
	); err != nil {
		return fmt.Errorf("add process to cgroup: %w", err)
	}

	return nil
}

func (c *Cgroup) Remove() error {
	if err := os.Remove(c.Path); err != nil {
		return fmt.Errorf("remove cgroup: %w", err)
	}

	return nil
}
