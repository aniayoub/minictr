package main

import (
	"fmt"
	"minictr/internal/config"
	"minictr/internal/container"
	"minictr/internal/runtime"
	"os"
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
			fmt.Println("Error running container:", err)
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
