package container

import (
	"fmt"
	"minictr/internal/config"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func Init(config *config.Config) error {
	fmt.Println("Container init PID:", os.Getpid())

	rootfs := config.Rootfs
	command := config.Command[0]
	commandArgs := config.Command[0:]

	if err := setHostname(config.Hostname); err != nil {
		return err
	}

	if err := makeMountsPrivate(); err != nil {
		return err
	}

	if err := pivotRoot(rootfs); err != nil {
		return err
	}

	if err := mountProc(); err != nil {
		return err
	}

	return execWorkload(command, commandArgs)
}

func setHostname(hostname string) error {
	if err := unix.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("set hostname: %w", err)
	}

	fmt.Println("Hostname set to:", hostname)

	return nil
}

func makeMountsPrivate() error {
	if err := unix.Mount(
		"",
		"/",
		"",
		unix.MS_PRIVATE|unix.MS_REC,
		"",
	); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}

	fmt.Println("Mounts set to private")
	return nil
}

func pivotRoot(rootfs string) error {
	rootfs, err := filepath.Abs(rootfs)
	if err != nil {
		return fmt.Errorf("resolve rootfs path: %w", err)
	}

	// pivot_root requires the new root to be a mount point.
	// Bind-mounting rootfs onto itself makes it one.
	if err := unix.Mount(
		rootfs,
		rootfs,
		"",
		unix.MS_BIND|unix.MS_REC,
		"",
	); err != nil {
		return fmt.Errorf("bind mount rootfs: %w", err)
	}

	// The kernel temporarily moves the old root here
	// during pivot_root.
	putOld := filepath.Join(rootfs, ".pivot_root")

	if err := os.MkdirAll(putOld, 0700); err != nil {
		return fmt.Errorf("create old-root directory: %w", err)
	}

	if err := unix.PivotRoot(rootfs, putOld); err != nil {
		return fmt.Errorf("pivot root: %w", err)
	}

	// We are now inside the new root filesystem.
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir to new root: %w", err)
	}

	// Remove access to the old host root.
	if err := unix.Unmount("/.pivot_root", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old root: %w", err)
	}

	if err := os.Remove("/.pivot_root"); err != nil {
		return fmt.Errorf("remove old-root directory: %w", err)
	}

	fmt.Println("Pivot root successful")
	return nil
}

func mountProc() error {
	if err := unix.Mount(
		"proc",
		"/proc",
		"proc",
		0,
		"",
	); err != nil {
		return fmt.Errorf("mount proc: %w", err)
	}

	fmt.Println("Proc mounted")
	return nil
}

func execWorkload(command string, args []string) error {
	fmt.Println("Executing workload:", command, args)
	// If Exec succeeds, this function never returns.
	if err := unix.Exec(command, args, os.Environ()); err != nil {
		return fmt.Errorf("exec workload: %w", err)
	}

	return nil
}
