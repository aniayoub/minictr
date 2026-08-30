package runtime

import (
	"fmt"
	"minictr/internal/cgroup"
	"minictr/internal/config"
	"os"
	"os/exec"
	"syscall"
)

func Run(runWith []string, cfg *config.Config) error {
	fmt.Println("Parent Process ID:", os.Getpid())

	args := append([]string{"init"}, runWith...)

	cmd := createCommand(args)

	cg, err := createCgroup(cfg)
	if err != nil {
		return err
	}

	defer cg.Remove()

	if err := runCommand(cmd, cg); err != nil {
		return err
	}

	return nil
}

func createCgroup(cfg *config.Config) (*cgroup.Cgroup, error) {
	// Create a Cgroup to limit execution resources
	cg, err := cgroup.Create(
		fmt.Sprintf("minictr-%d", os.Getpid()),
	)

	if err != nil {
		return nil, err
	}

	if cfg.PidsMax > 0 {
		err = cg.SetPidsMax(cfg.PidsMax)
		if err != nil {
			return nil, fmt.Errorf("set pids.max: %w", err)
		}
	}

	if cfg.MemoryMax > 0 {
		err = cg.SetMemoryMax(cfg.MemoryMax)
		if err != nil {
			return nil, fmt.Errorf("set memory.max: %w", err)
		}
	}
	if cfg.CpuMax > 0 {
		err = cg.SetCpuMax(cfg.CpuMax)
		if err != nil {
			return nil, fmt.Errorf("set cpu.max: %w", err)
		}
	}

	return cg, nil
}

func createCommand(args []string) *exec.Cmd {
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
		return err
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait for child process: %w", err)
	}

	return nil
}
