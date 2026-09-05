package runtime

import (
	"errors"
	"fmt"
	"minictr/internal/cgroup"
	"minictr/internal/config"
	"minictr/internal/minictr"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func Run(runWith []string, cfg *config.Config) (retErr error) {
	fmt.Println("Parent Process ID:", os.Getpid())

	cmd := createCommand(runWith)

	cg, err := createCgroup(cfg)

	if err != nil {
		return err
	}

	defer func() {
		if cleanupErr := cg.Remove(); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()

	return runCommand(cmd, cg)
}

func createCgroup(cfg *config.Config) (cg *cgroup.Cgroup, retErr error) {
	// Create a Cgroup to limit execution resources
	cg, err := cgroup.Create(
		fmt.Sprintf("minictr-%d", os.Getpid()),
	)

	if err != nil {
		return nil, err
	}

	// If anything below fails, creation was incomplete,
	// so remove the cgroup before returning.
	defer func() {
		if retErr != nil {
			if cleanupErr := cg.Remove(); cleanupErr != nil {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()

	if cfg.PidsMax > 0 {
		err = cg.SetPidsMax(cfg.PidsMax)
		if err != nil {
			return cg, fmt.Errorf("set pids.max: %w", err)
		}
	}

	if cfg.MemoryMax > 0 {
		err = cg.SetMemoryMax(cfg.MemoryMax)
		if err != nil {
			return cg, fmt.Errorf("set memory.max: %w", err)
		}
	}
	if cfg.CpuMax > 0 {
		err = cg.SetCpuMax(cfg.CpuMax)
		if err != nil {
			return cg, fmt.Errorf("set cpu.max: %w", err)
		}
	}

	return cg, nil
}

func createCommand(runWith []string) *exec.Cmd {
	args := append([]string{"init"}, runWith...)

	// Create a new child process the Unix way,
	// using the same executable but with the "init" argument.
	cmd := exec.Command("/proc/self/exe", args...)

	// Set the standard input, output, and error to the current process's.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Ensure isolation by setting the appropriate clone flags for UTS, PID, and mount namespaces.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS,
	}

	return cmd
}

func runCommand(cmd *exec.Cmd, cg *cgroup.Cgroup) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start child process: %w", err)
	}

	fmt.Println("Child Process ID:", cmd.Process.Pid)

	if err := cg.AddProcess(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	// Handle signals and forward them to the child process.
	done := make(chan struct{})
	signals := minictr.HandleLinuxSignals(cmd, done)

	// Ensure closing of the signals
	// signals.Stop does not close the channel it only stops receiving signals.
	defer func() {
		signal.Stop(signals)
		close(done)
	}()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait for child process: %w", err)
	}

	return nil
}
