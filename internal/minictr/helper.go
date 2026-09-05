package minictr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func HandleLinuxSignals(cmd *exec.Cmd, done <-chan struct{}) chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(
		signals,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGQUIT,
	)
	go func() {
		for {
			select {
			case sig := <-signals:
				if cmd.Process != nil {
					fmt.Printf("Forwarded signal %v to process %d\n", sig, cmd.Process.Pid)
					if err := cmd.Process.Signal(sig); err != nil {
						fmt.Printf("Failed to forward signal %v to child process: %v\n", sig, err)
					}

				}
			case <-done:
				return
			}
		}
	}()
	return signals
}

func AdjustStopSignal(err error) int {
	var exitErr *exec.ExitError

	// Check if the error is an ExitError and exit with the appropriate code.
	if errors.As(err, &exitErr) {

		// Extract the exit status from the ExitError. If it's available, use it; otherwise, fall back to the generic exit code.
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok && status.Signaled() {
			fmt.Println("Process terminated by signal:", status.Signal())
			return (128 + int(status.Signal()))
		}

		return exitErr.ExitCode()
	}

	return 1

}
