package sandbox

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// ResourceLimits
// ---------------------------------------------------------------------------

// ResourceLimits specifies the resource constraints for a sandboxed execution.
type ResourceLimits struct {
	// Maximum wall-clock time allowed for execution.
	TimeLimit time.Duration `json:"time_limit"`

	// Maximum virtual memory in bytes.
	MemoryLimit int64 `json:"memory_limit"`

	// Maximum stack size in bytes.
	StackLimit int64 `json:"stack_limit"`

	// Maximum output size in bytes (stdout + stderr combined).
	OutputLimit int64 `json:"output_limit"`

	// Maximum number of processes/threads the program may spawn.
	MaxProcs int `json:"max_procs"`

	// Whether to allow network access inside the sandbox.
	Network bool `json:"network"`
}

// DefaultLimits returns a ResourceLimits with conservative defaults suitable
// for most competitive-programming problems: 2 seconds, 256 MB memory, 64 MB
// stack, 64 MB output, 1 process, no network.
func DefaultLimits() ResourceLimits {
	return ResourceLimits{
		TimeLimit:   2 * time.Second,
		MemoryLimit: 256 * 1024 * 1024, // 256 MiB
		StackLimit:  64 * 1024 * 1024,  // 64 MiB
		OutputLimit: 64 * 1024 * 1024,  // 64 MiB
		MaxProcs:    1,
		Network:     false,
	}
}

// WithTimeLimit returns a copy of the limits with the time limit overridden.
func (r ResourceLimits) WithTimeLimit(d time.Duration) ResourceLimits {
	r.TimeLimit = d
	return r
}

// WithMemoryLimit returns a copy of the limits with the memory limit overridden.
func (r ResourceLimits) WithMemoryLimit(bytes int64) ResourceLimits {
	r.MemoryLimit = bytes
	return r
}

// Validate checks that all limits are positive and within sane bounds.
func (r ResourceLimits) Validate() error {
	if r.TimeLimit <= 0 {
		return fmt.Errorf("time_limit must be positive, got %v", r.TimeLimit)
	}
	if r.TimeLimit > 5*time.Minute {
		return fmt.Errorf("time_limit must not exceed 5 minutes, got %v", r.TimeLimit)
	}
	if r.MemoryLimit <= 0 {
		return fmt.Errorf("memory_limit must be positive, got %d", r.MemoryLimit)
	}
	if r.MemoryLimit > 4*1024*1024*1024 { // 4 GiB
		return fmt.Errorf("memory_limit must not exceed 4 GiB, got %d", r.MemoryLimit)
	}
	if r.StackLimit < 0 {
		return fmt.Errorf("stack_limit must be non-negative, got %d", r.StackLimit)
	}
	if r.OutputLimit <= 0 {
		return fmt.Errorf("output_limit must be positive, got %d", r.OutputLimit)
	}
	if r.MaxProcs < 1 {
		return fmt.Errorf("max_procs must be at least 1, got %d", r.MaxProcs)
	}
	return nil
}

// ToNsjailArgs converts the resource limits into command-line arguments
// suitable for nsjail. The returned slice does not include the nsjail binary
// path or the program to execute -- those are the caller's responsibility.
func (r ResourceLimits) ToNsjailArgs() []string {
	timeLimitSec := int(r.TimeLimit.Seconds())
	if timeLimitSec < 1 {
		timeLimitSec = 1
	}

	memoryLimitMB := r.MemoryLimit / (1024 * 1024)
	if memoryLimitMB < 1 {
		memoryLimitMB = 1
	}

	stackLimitMB := r.StackLimit / (1024 * 1024)

	outputLimitMB := r.OutputLimit / (1024 * 1024)
	if outputLimitMB < 1 {
		outputLimitMB = 1
	}

	args := []string{
		"--time_limit", fmt.Sprintf("%d", timeLimitSec),
		"--rlimit_as", fmt.Sprintf("%d", memoryLimitMB),
		"--rlimit_fsize", fmt.Sprintf("%d", outputLimitMB),
		"--max_cpus", fmt.Sprintf("%d", r.MaxProcs),
	}

	if stackLimitMB > 0 {
		args = append(args, "--rlimit_stack", fmt.Sprintf("%d", stackLimitMB))
	}

	if !r.Network {
		args = append(args, "--disable_clone_newnet")
	}

	return args
}
