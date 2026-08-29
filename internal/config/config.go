package config

import (
	"errors"
	"flag"
	"fmt"
)

type Config struct {
	Hostname string
	Rootfs   string
	Command  []string
}

func Parse(args []string) (*Config, error) {

	if len(args) == 0 {
		return nil, errors.New("rootfs is required")
	}

	// Initialize the config with the rootfs since rootfs is clearly the first argument in the args slice.
	config := &Config{
		Rootfs: args[0],
	}

	// Identify the index of the "--" separator in the args slice.
	separatorIndex := -1
	for i, arg := range args {
		if arg == "--" {
			separatorIndex = i
			break
		}
	}

	if separatorIndex == -1 {
		return nil, errors.New("missing '--' before command and its arguments")
	}

	if separatorIndex+1 >= len(args) {
		return config, errors.New("command is required")
	}

	runtimeArgs := args[1:separatorIndex]
	config.Command = args[separatorIndex+1:]

	flags := flag.NewFlagSet("minictr", flag.ContinueOnError)

	flags.StringVar(
		&config.Hostname,
		"hostname",
		"minictr",
		"Container hostname",
	)

	if err := flags.Parse(runtimeArgs); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	return config, nil
}
