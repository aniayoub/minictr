package main

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

func main() {

	// Extract the requested command from the command-line arguments
	if len(os.Args) < 3 {
		fmt.Println("Usage: minictr <init|run> <command> [args...]")
		return
	}

	switch os.Args[1] {
	case "init":
		// If the requested command is "init", run the container initialization logic
		err := containerInit()
		if err != nil {
			fmt.Println("Error initializing container:", err)
			return
		}
	case "run":
		// Run the child process with the requested command
		_, err := run()
		if err != nil {
			fmt.Println("Error running child process:", err)
			return
		}

	}

}

func run() (*exec.Cmd, error) {
	fmt.Println("Parent Process ID:", os.Getpid())

	// Create a new command to run the child process with the requested command,
	// But this time using "init" to distinguish it from the parent process.
	cmd := exec.Command("/proc/self/exe", append([]string{"init"}, os.Args[2:]...)...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Set the SysProcAttr to create a new UTS namespace for the child process.
	cmd.SysProcAttr = &unix.SysProcAttr{
		Cloneflags: unix.CLONE_NEWUTS | unix.CLONE_NEWPID,
	}

	err := cmd.Start()

	if err != nil {
		fmt.Println("Error starting child process:", err)
		return nil, err
	}

	fmt.Println("Child Process ID:", cmd.Process.Pid)

	err = cmd.Wait()
	if err != nil {
		fmt.Println("Error waiting for child process:", err)
		return nil, err
	}

	fmt.Println("New child process is created successfully.")

	return cmd, nil
}

func containerInit() error {

	fmt.Println("Container init PID:", os.Getpid())

	err := unix.Sethostname([]byte("minictr"))
	if err != nil {
		return fmt.Errorf("error setting hostname: %v", err)
	}
	err = unix.Exec(os.Args[2], os.Args[2:], os.Environ())
	if err != nil {
		return fmt.Errorf("error executing child process with unix.Exec: %v", err)
	}

	return nil
}
