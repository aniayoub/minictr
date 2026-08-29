package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: minictr <init|run> <rootfs> <command> [args...]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := run(); err != nil {
			fmt.Println("Error running container:", err)
			os.Exit(1)
		}

	case "init":
		if err := containerInit(); err != nil {
			fmt.Println("Error initializing container:", err)
			os.Exit(1)
		}

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("Parent Process ID:", os.Getpid())

	args := append([]string{"init"}, os.Args[2:]...)

	cmd := exec.Command("/proc/self/exe", args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start child process: %w", err)
	}

	fmt.Println("Child Process ID:", cmd.Process.Pid)

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait for child process: %w", err)
	}

	return nil
}

func containerInit() error {
	fmt.Println("Container init PID:", os.Getpid())

	rootfs := os.Args[2]
	command := os.Args[3]
	commandArgs := os.Args[3:]

	if err := setHostname("minictr"); err != nil {
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

	return nil
}

func execWorkload(command string, args []string) error {
	// If Exec succeeds, this function never returns.
	if err := unix.Exec(command, args, os.Environ()); err != nil {
		return fmt.Errorf("exec workload: %w", err)
	}

	return nil
}
