package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func Run(runWith []string) error {
	fmt.Println("Parent Process ID:", os.Getpid())

	args := append([]string{"init"}, runWith...)

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
