package config

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expected    *Config
		expectError bool
	}{
		{
			name: "valid minimal config",
			args: []string{"/myrootfs", "--", "sh"},
			expected: &Config{
				Rootfs:    "/myrootfs",
				Command:   []string{"sh"},
				Hostname:  "minictr", // default value
				PidsMax:   0,
				MemoryMax: 0,
				CpuMax:    0,
			},
			expectError: false,
		},
		{
			name: "valid config with flags",
			args: []string{"/myrootfs", "-hostname", "my-container", "-memory", "512M", "-pids", "100", "--", "echo", "hello"},
			expected: &Config{
				Rootfs:    "/myrootfs",
				Command:   []string{"echo", "hello"},
				Hostname:  "my-container",
				PidsMax:   100,
				MemoryMax: 512 * 1024 * 1024,
				CpuMax:    0,
			},
			expectError: false,
		},
		{
			name: "cpu flag fractional",
			args: []string{"/myrootfs", "-cpu", "0.5", "--", "sh"},
			expected: &Config{
				Rootfs:   "/myrootfs",
				Command:  []string{"sh"},
				Hostname: "minictr",
				CpuMax:   50_000, // 0.5 * CPUPeriodMicros
			},
			expectError: false,
		},
		{
			name: "cpu flag rounding",
			args: []string{"/myrootfs", "-cpu", "0.29", "--", "sh"},
			expected: &Config{
				Rootfs:   "/myrootfs",
				Command:  []string{"sh"},
				Hostname: "minictr",
				CpuMax:   29_000, // 0.29 * CPUPeriodMicros
			},
			expectError: false,
		},
		{
			name: "cpu flag whole number",
			args: []string{"/myrootfs", "-cpu", "2", "--", "sh"},
			expected: &Config{
				Rootfs:   "/myrootfs",
				Command:  []string{"sh"},
				Hostname: "minictr",
				CpuMax:   200_000,
			},
			expectError: false,
		},
		{
			name: "cpu flag negative",
			args: []string{"/myrootfs", "-cpu", "-2", "--", "sh"},
			expected: &Config{
				Rootfs:   "/myrootfs",
				Command:  []string{"sh"},
				Hostname: "minictr",
				CpuMax:   100_000,
			},
			expectError: true,
		},
		{
			name: "single bind mount",
			args: []string{"/myrootfs", "-bind", "/src:/dst", "--", "sh"},
			expected: &Config{
				Rootfs:     "/myrootfs",
				Command:    []string{"sh"},
				Hostname:   "minictr",
				BindMounts: []BindMount{{Source: "/src", Target: "/dst"}},
			},
			expectError: false,
		},
		{
			name: "multiple bind mounts",
			args: []string{"/myrootfs", "-bind", "/src1:/dst1", "-bind", "/src2:/dst2", "--", "sh"},
			expected: &Config{
				Rootfs:   "/myrootfs",
				Command:  []string{"sh"},
				Hostname: "minictr",
				BindMounts: []BindMount{
					{Source: "/src1", Target: "/dst1"},
					{Source: "/src2", Target: "/dst2"},
				},
			},
			expectError: false,
		},
		{
			name: "all flags combined",
			args: []string{
				"/myrootfs",
				"-hostname", "box",
				"-memory", "256M",
				"-pids", "50",
				"-cpu", "1.5",
				"-bind", "/a:/b",
				"--",
				"sh", "-c", "echo hi",
			},
			expected: &Config{
				Rootfs:     "/myrootfs",
				Command:    []string{"sh", "-c", "echo hi"},
				Hostname:   "box",
				PidsMax:    50,
				MemoryMax:  256 * 1024 * 1024,
				CpuMax:     150_000,
				BindMounts: []BindMount{{Source: "/a", Target: "/b"}},
			},
			expectError: false,
		},
		{
			name:        "missing rootfs (empty args)",
			args:        []string{},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "missing separator '--'",
			args:        []string{"/myrootfs", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "missing command after separator",
			args:        []string{"/myrootfs", "--"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "invalid memory value",
			args:        []string{"/myrootfs", "-memory", "not-a-number", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "memory overflow",
			args:        []string{"/myrootfs", "-memory", "999999999999999999999999", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "negative memory value",
			args:        []string{"/myrootfs", "-memory", "-1G", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "negative pids value",
			args:        []string{"/myrootfs", "-pids", "-1", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "fractional pids value",
			args:        []string{"/myrootfs", "-pids", "1.5", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "invalid bind mount format",
			args:        []string{"/myrootfs", "-bind", "/src-no-colon", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "unknown flag",
			args:        []string{"/myrootfs", "-unknown", "value", "--", "sh"},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.args)

			if (err != nil) != tt.expectError {
				t.Fatalf("Parse() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError && !reflect.DeepEqual(cfg, tt.expected) {
				t.Errorf("Parse() got = %+v, want = %+v", cfg, tt.expected)
			}
		})
	}
}

func TestParseBytes(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    int64
		expectError bool
	}{
		{name: "bytes without suffix", input: "1024", expected: 1024, expectError: false},
		{name: "zero value", input: "0", expected: 0, expectError: false},
		{name: "kilobytes lowercase", input: "2k", expected: 2 * 1024, expectError: false},
		{name: "kilobytes uppercase", input: "2K", expected: 2 * 1024, expectError: false},
		{name: "megabytes uppercase", input: "512M", expected: 512 * 1024 * 1024, expectError: false},
		{name: "megabytes lowercase", input: "128m", expected: 128 * 1024 * 1024, expectError: false},
		{name: "gigabytes uppercase", input: "1G", expected: 1024 * 1024 * 1024, expectError: false},
		{name: "gigabytes lowercase", input: "3g", expected: 3 * 1024 * 1024 * 1024, expectError: false},
		{name: "gigabytes with spaces", input: " 1G ", expected: 1024 * 1024 * 1024, expectError: false},
		{name: "invalid characters", input: "abcM", expected: 0, expectError: true},
		{name: "suffix only no number", input: "M", expected: 0, expectError: true},
		{name: "empty string", input: "", expected: 0, expectError: true},
		{name: "decimal value", input: "1.5M", expected: 0, expectError: true},
		{name: "overflow value", input: "9999999999G", expected: 0, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := parseBytes(&tt.input)

			if (err != nil) != tt.expectError {
				t.Fatalf("parseBytes() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError && val != tt.expected {
				t.Errorf("parseBytes() got = %d, want = %d", val, tt.expected)
			}
		})
	}
}

func TestBindMountFlagSet(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expected    BindMount
		expectError bool
	}{
		{name: "valid source and target", value: "/src:/dst", expected: BindMount{Source: "/src", Target: "/dst"}, expectError: false},
		{name: "nested paths", value: "/foo/bar:/baz/qux", expected: BindMount{Source: "/foo/bar", Target: "/baz/qux"}, expectError: false},
		{name: "no colon separator", value: "/src-no-colon", expectError: true},
		{name: "empty source", value: ":/dst", expectError: true},
		{name: "empty target", value: "/src:", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bindMountFlag
			err := b.Set(tt.value)

			if (err != nil) != tt.expectError {
				t.Fatalf("Set() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError {
				if len(b) != 1 {
					t.Fatalf("Set() expected 1 mount, got %d", len(b))
				}
				if b[0] != tt.expected {
					t.Errorf("Set() got = %+v, want = %+v", b[0], tt.expected)
				}
			}
		})
	}
}

func TestBindMountFlagSetAccumulates(t *testing.T) {
	var b bindMountFlag

	if err := b.Set("/src1:/dst1"); err != nil {
		t.Fatalf("first Set() unexpected error: %v", err)
	}
	if err := b.Set("/src2:/dst2"); err != nil {
		t.Fatalf("second Set() unexpected error: %v", err)
	}

	if len(b) != 2 {
		t.Fatalf("expected 2 mounts after 2 Set() calls, got %d", len(b))
	}
	if b[0] != (BindMount{Source: "/src1", Target: "/dst1"}) {
		t.Errorf("first mount: got %+v", b[0])
	}
	if b[1] != (BindMount{Source: "/src2", Target: "/dst2"}) {
		t.Errorf("second mount: got %+v", b[1])
	}
}

func TestBindMountFlagString(t *testing.T) {
	var b bindMountFlag
	if got := b.String(); got != "" {
		t.Errorf("String() got = %q, want empty string", got)
	}
}
