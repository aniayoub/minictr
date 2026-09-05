package container

import (
	"fmt"
	"minictr/internal/config"
	"minictr/internal/minictr"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func Init(config *config.Config) (int, error) {
	fmt.Println("Container init PID:", os.Getpid())

	rootfs := config.Rootfs
	command := config.Command[0]
	commandArgs := config.Command[0:]

	if err := setHostname(config.Hostname); err != nil {
		return 1, err
	}

	if err := makeMountsPrivate(); err != nil {
		return 1, err
	}

	if err := mountBinds(rootfs, config.BindMounts); err != nil {
		return 1, err
	}

	if err := pivotRoot(rootfs); err != nil {
		return 1, err
	}

	if err := mountProc(); err != nil {
		return 1, err
	}

	code, err := superviseWorkload(command, commandArgs)
	if err != nil {
		return code, err
	}
	return code, nil
}

func setHostname(hostname string) error {
	if err := unix.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("set hostname: %w", err)
	}

	fmt.Println("Hostname set to:", hostname)

	return nil
}

func mountBinds(rootfs string, bindMounts []config.BindMount) error {
	fmt.Println("Mounting bind mounts:", bindMounts)
	for _, bind := range bindMounts {
		source, err := filepath.Abs(bind.Source)
		if err != nil {
			return fmt.Errorf("resolve source path: %w", err)
		}

		target := filepath.Clean(bind.Target)

		if !filepath.IsAbs(target) {
			return fmt.Errorf("target path must be absolute: %s", target)
		}

		// /data -> /rootfs/data
		hostTarget := filepath.Join(
			rootfs,
			strings.TrimPrefix(target, "/"),
		)

		if err := os.MkdirAll(hostTarget, 0755); err != nil {
			return fmt.Errorf("create target directory: %w", err)
		}

		if err := unix.Mount(
			source,
			hostTarget,
			"",
			unix.MS_BIND|unix.MS_REC,
			"",
		); err != nil {
			return fmt.Errorf("bind mount %s to %s: %w", source, hostTarget, err)
		}

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

func superviseWorkload(command string, args []string) (int, error) {

	// Given the current setup, the PID will be 1 for this process, which is the init process inside the container.
	fmt.Println("Container init pid:", os.Getpid())

	// args already include the command as the first element, so we skip it for cmdArgs.
	cmdArgs := args[1:]
	// Create a command instead of using exec.Command, this way we avoid the assignment of PID 1 to the workload process,
	// which would prevent it from receiving signals like SIGTERM.
	cmd := exec.Command(command, cmdArgs...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start workload: %w", err)
	}

	fmt.Println("Workload started with PID:", cmd.Process.Pid)

	done := make(chan struct{})
	signals := minictr.HandleLinuxSignals(cmd, done)

	// Ensure closing of the signals
	// signals.Stop does not close the channel it only stops receiving signals.
	defer func() {
		signal.Stop(signals)
		close(done)
	}()

	code, err := waitAndReap(cmd)
	if err != nil {
		return 1, fmt.Errorf("wait for workload: %w", err)
	}
	fmt.Println("Workload exited with code:", code)
	return code, nil
}

func waitAndReap(cmd *exec.Cmd) (int, error) {
	mainPid := cmd.Process.Pid

	for {
		var status unix.WaitStatus

		pid, err := unix.Wait4(-1, &status, 0, nil)

		if err != nil {
			// Restart the wait if it was interrupted by a signal.
			if err == unix.EINTR {
				continue
			}

			return 0, fmt.Errorf("wait for child process: %w", err)
		}

		if pid != mainPid {
			// TODO: Handle reaping of any other child processes that might have been spawned by the workload.
			// For now, we just log that we reaped a child process and continue waiting for the main workload process.
			fmt.Println("Reaped child process with PID:", pid)
			continue
		}

		// If the main workload process has exited, we return an error that encapsulates the exit status.
		// This allows the caller to determine the exit code or signal that caused the termination.
		if status.Exited() {
			return status.ExitStatus(), nil
		}

		if status.Signaled() {
			return 128 + int(status.Signal()), nil
		}

		return 1, fmt.Errorf("workload process exited with unknown status")
	}
}
