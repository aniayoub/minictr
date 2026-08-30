package config

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const CPUPeriodMicros int64 = 100_000

type Config struct {
	// Command to execute inside the container
	Command []string

	// Runtime Parameters
	Hostname   string
	Rootfs     string
	BindMounts []BindMount

	PidsMax   int64
	MemoryMax int64
	CpuMax    int64
}

func Parse(args []string) (*Config, error) {

	if len(args) == 0 {
		return nil, errors.New("rootfs is required")
	}

	config := &Config{}

	// Identify the index of the "--" separator in the args slice.
	separatorIndex, err := identifySepratorIndex(args)
	if err != nil {
		return nil, err
	}

	config.parseCommand(args, separatorIndex)

	if err := config.parseRuntimeOptions(args, separatorIndex); err != nil {
		return nil, err
	}

	return config, nil
}

func identifySepratorIndex(args []string) (int, error) {
	separatorIndex := -1
	for i, arg := range args {
		if arg == "--" {
			separatorIndex = i
			break
		}
	}

	if separatorIndex == -1 {
		return separatorIndex, errors.New("missing '--' before command and its arguments")
	}

	if separatorIndex+1 >= len(args) {
		return separatorIndex, errors.New("command is required")
	}

	return separatorIndex, nil
}

func (c *Config) parseCommand(args []string, separatorIndex int) {
	c.Command = args[separatorIndex+1:]
}

func (c *Config) parseRuntimeOptions(args []string, separatorIndex int) error {
	c.Rootfs = args[0]

	runtimeArgs := args[1:separatorIndex]

	flags := flag.NewFlagSet("minictr", flag.ContinueOnError)

	// Parse hostname
	flags.StringVar(
		&c.Hostname,
		"hostname",
		"minictr",
		"Container hostname",
	)

	// Parse bind mounts
	flags.Var(
		(*bindMountFlag)(&c.BindMounts),
		"bind",
		"Bind mount in the format source:target",
	)

	// Parse pids max
	flags.Int64Var(
		&c.PidsMax,
		"pids",
		0,
		"Maximum number of PIDs in the container",
	)

	// Parse memory max
	var memoryMaxStr string
	flags.StringVar(
		&memoryMaxStr,
		"memory",
		"",
		"Maximum memory in bytes (e.g., 512M, 1G)",
	)

	// Parse CPU max
	var cpuMax float64
	flags.Float64Var(
		&cpuMax,
		"cpu",
		float64(0),
		"Maximum CPU allocation, e.g. 0.5, 1, 2",
	)

	if err := flags.Parse(runtimeArgs); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	if memoryMaxStr != "" {
		memoryMax, err := parseBytes(&memoryMaxStr)
		if err != nil {
			return fmt.Errorf("failed to parse memory max: %w", err)
		}
		c.MemoryMax = memoryMax
	}

	if cpuMax > 0 {
		c.CpuMax = int64(math.Round(cpuMax * float64(CPUPeriodMicros)))
	}

	if err := c.validateConfig(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	return nil
}

func parseBytes(valueAdr *string) (int64, error) {
	value := strings.TrimSpace(strings.ToUpper(*valueAdr))

	multiplier := int64(1)

	switch {
	case strings.HasSuffix(value, "K"):
		multiplier = 1024
		value = strings.TrimSuffix(value, "K")
	case strings.HasSuffix(value, "M"):
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "M")
	case strings.HasSuffix(value, "G"):
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "G")
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value: %w", err)
	}

	// Make sure the parsed number does not cause an over flow before return
	if n > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("memory value too large")
	}

	return n * multiplier, nil
}

func (c *Config) validateConfig() error {
	if c.Rootfs == "" {
		return fmt.Errorf("rootfs is required")
	}

	if len(c.Command) == 0 {
		return fmt.Errorf("command is required")
	}

	// Check positive values for resource limits
	if c.PidsMax < 0 {
		return fmt.Errorf("pids max must be positive")
	}

	if c.MemoryMax < 0 {
		return fmt.Errorf("memory max must be positive")
	}

	if c.CpuMax < 0 {
		return fmt.Errorf("cpu max must be positive")
	}

	return nil
}
