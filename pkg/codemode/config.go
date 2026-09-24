// Package codemode is the experimental "code mode" spike (issue #325): the
// model sends one execute_code call holding a short JavaScript program, the
// program calls upstream tools and filters their results inside a sandbox,
// and only its final value goes back into the context window.
//
// It is a throw-away prototype, not a shipped feature (see
// docs/design/code-mode.md for the design, the measurements and the go /
// no-go recommendation):
//
//   - The sandbox (sandbox.go, worker.go) and its JavaScript runtime (goja)
//     are only compiled with `-tags codemode`. The default binary contains
//     only this file and available_off.go, so its size and behavior do not
//     change.
//   - Even in a codemode build it stays off until `code_mode.enabled: true`.
//   - Every tool call a program makes is sent back through the proxy's full
//     middleware pipeline (pkg/mcp/middleware_codemode.go): policy, tool
//     pinning, redaction, injection guard, response governor, telemetry.
//
// This file holds the `code_mode:` config block, which has no build tag so
// pkg/migrate can parse and validate it in every build.
package codemode

import (
	"fmt"
	"time"
)

// Defaults of the `code_mode:` block.
const (
	// DefaultTimeout bounds one execute_code call, wall clock, tool calls
	// included. The sandbox process is killed when it runs out.
	DefaultTimeout = 30 * time.Second
	// DefaultCPUTime bounds the CPU time of the sandbox process (the
	// kernel's RLIMIT_CPU on Unix). Time spent waiting for tool calls is
	// not CPU time.
	DefaultCPUTime = 5 * time.Second
	// DefaultMaxMemoryMB is the sandbox process's memory limit.
	DefaultMaxMemoryMB = 256
	// DefaultMaxCalls caps the tool calls of one program.
	DefaultMaxCalls = 32
	// DefaultMaxConcurrentCalls caps the tool calls of one program in
	// flight at once (Promise.all).
	DefaultMaxConcurrentCalls = 4
	// DefaultMaxOutputBytes caps the program's result (what reaches the
	// model): 64 KiB, about 16k tokens.
	DefaultMaxOutputBytes = 64 << 10
	// DefaultMaxCodeBytes caps the program's source.
	DefaultMaxCodeBytes = 64 << 10
	// DefaultMaxCallResultBytes caps one tool result handed to the
	// program.
	DefaultMaxCallResultBytes = 4 << 20

	// MinMaxMemoryMB is the smallest memory limit: below it the Go
	// runtime of the sandbox process itself does not fit.
	MinMaxMemoryMB = 64
	// MaxTimeout is the largest timeout accepted.
	MaxTimeout = 10 * time.Minute
)

// Config is the `code_mode:` block. Off by default; a binary built without
// `-tags codemode` ignores it (with a warning when enabled).
type Config struct {
	// Enabled lists the execute_code tool and serves it. Off by default.
	Enabled bool `yaml:"enabled"`
	// Timeout bounds one execute_code call (a Go duration). Unset means
	// DefaultTimeout.
	Timeout string `yaml:"timeout,omitempty"`
	// CPUTime bounds the sandbox's CPU time (a Go duration, whole
	// seconds on Unix). Unset means DefaultCPUTime.
	CPUTime string `yaml:"cpu_time,omitempty"`
	// MaxMemoryMB is the sandbox's memory limit in MiB. Unset (0) means
	// DefaultMaxMemoryMB.
	MaxMemoryMB int `yaml:"max_memory_mb,omitempty"`
	// MaxCalls caps the tool calls of one program. Unset (0) means
	// DefaultMaxCalls.
	MaxCalls int `yaml:"max_calls,omitempty"`
	// MaxConcurrentCalls caps the tool calls in flight at once. Unset (0)
	// means DefaultMaxConcurrentCalls.
	MaxConcurrentCalls int `yaml:"max_concurrent_calls,omitempty"`
	// MaxOutputBytes caps the program's result. Unset (0) means
	// DefaultMaxOutputBytes.
	MaxOutputBytes int `yaml:"max_output_bytes,omitempty"`
}

// IsEnabled reports whether code mode is switched on. A nil block is off.
func (c *Config) IsEnabled() bool { return c != nil && c.Enabled }

// Validate checks the block. A nil block is valid (off).
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if err := validateDuration("code_mode.timeout", c.Timeout, MaxTimeout); err != nil {
		return err
	}
	if err := validateDuration("code_mode.cpu_time", c.CPUTime, MaxTimeout); err != nil {
		return err
	}
	if c.CPUTime != "" {
		if d, _ := time.ParseDuration(c.CPUTime); d < time.Second {
			return fmt.Errorf("code_mode.cpu_time must be at least 1s, got %s", c.CPUTime)
		}
	}
	if c.MaxMemoryMB != 0 && c.MaxMemoryMB < MinMaxMemoryMB {
		return fmt.Errorf("code_mode.max_memory_mb must be at least %d, got %d", MinMaxMemoryMB, c.MaxMemoryMB)
	}
	for _, f := range []struct {
		name string
		v    int
	}{
		{"code_mode.max_memory_mb", c.MaxMemoryMB},
		{"code_mode.max_calls", c.MaxCalls},
		{"code_mode.max_concurrent_calls", c.MaxConcurrentCalls},
		{"code_mode.max_output_bytes", c.MaxOutputBytes},
	} {
		if f.v < 0 {
			return fmt.Errorf("%s must be >= 0, got %d", f.name, f.v)
		}
	}
	return nil
}

func validateDuration(field, value string, maxValue time.Duration) error {
	if value == "" {
		return nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s: invalid duration %q: %w", field, value, err)
	}
	if d <= 0 || d > maxValue {
		return fmt.Errorf("%s must be > 0 and <= %s, got %s", field, maxValue, value)
	}
	return nil
}

// Limits are the effective limits of one execute_code call.
type Limits struct {
	Timeout            time.Duration `json:"timeout"`
	CPUTime            time.Duration `json:"cpu_time"`
	MaxMemoryMB        int           `json:"max_memory_mb"`
	MaxCalls           int           `json:"max_calls"`
	MaxConcurrentCalls int           `json:"max_concurrent_calls"`
	MaxOutputBytes     int           `json:"max_output_bytes"`
	MaxCodeBytes       int           `json:"max_code_bytes"`
	MaxCallResultBytes int           `json:"max_call_result_bytes"`
}

// Limits returns the block's limits with the defaults filled in. A nil
// block gives the defaults.
func (c *Config) Limits() Limits {
	l := Limits{
		Timeout:            DefaultTimeout,
		CPUTime:            DefaultCPUTime,
		MaxMemoryMB:        DefaultMaxMemoryMB,
		MaxCalls:           DefaultMaxCalls,
		MaxConcurrentCalls: DefaultMaxConcurrentCalls,
		MaxOutputBytes:     DefaultMaxOutputBytes,
		MaxCodeBytes:       DefaultMaxCodeBytes,
		MaxCallResultBytes: DefaultMaxCallResultBytes,
	}
	if c == nil {
		return l
	}
	if d, err := time.ParseDuration(c.Timeout); err == nil && d > 0 {
		l.Timeout = d
	}
	if d, err := time.ParseDuration(c.CPUTime); err == nil && d > 0 {
		l.CPUTime = d
	}
	if c.MaxMemoryMB > 0 {
		l.MaxMemoryMB = c.MaxMemoryMB
	}
	if c.MaxCalls > 0 {
		l.MaxCalls = c.MaxCalls
	}
	if c.MaxConcurrentCalls > 0 {
		l.MaxConcurrentCalls = c.MaxConcurrentCalls
	}
	if c.MaxOutputBytes > 0 {
		l.MaxOutputBytes = c.MaxOutputBytes
	}
	return l
}
