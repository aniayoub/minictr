package main

import (
	"errors"
	"fmt"
	"minictr/internal/config"
	"minictr/internal/container"
	"minictr/internal/runtime"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: minictr <init|run> <rootfs> [runtime-options] -- <command> [command-args...]")
		os.Exit(1)
	}

	// We pass the arguments starting the rootfs to the config parser.
	// The first 2 arguments usage is scoped to the main function itself.
	config, err := config.Parse(os.Args[2:])

	if err != nil {
		fmt.Println("Error parsing config:", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := runtime.Run(os.Args[2:], config); err != nil {
			var exitErr *exec.ExitError

			// Check if the error is an ExitError and exit with the appropriate code.
			if errors.As(err, &exitErr) {

				// Extract the exit status from the ExitError. If it's available, use it; otherwise, fall back to the generic exit code.
				status, ok := exitErr.Sys().(syscall.WaitStatus)
				if ok && status.Signaled() {
					fmt.Println("Process terminated by signal:", status.Signal())
					os.Exit(128 + int(status.Signal()))
				}

				os.Exit(exitErr.ExitCode())
			}

			// If the error is not an ExitError, print it and exit with code 1.
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "init":

		if err := container.Init(config); err != nil {
			fmt.Println("Error initializing container:", err)
			os.Exit(1)
		}

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
