package config

import (
	"fmt"
	"strings"
)

type BindMount struct {
	Source string
	Target string
}

type bindMountFlag []BindMount

func (b *bindMountFlag) String() string {
	return ""
}

func (b *bindMountFlag) Set(value string) error {
	// Split the value into source and target using the colon as a separator.
	source, target, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("invalid bind mount format: %s", value)
	}

	if source == "" || target == "" {
		return fmt.Errorf("source and target must not be empty: %s", value)
	}

	*b = append(*b, BindMount{
		Source: source,
		Target: target,
	})
	return nil
}
