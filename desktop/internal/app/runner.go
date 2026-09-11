package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}

type CommandRunner struct{ Docker string }

type cappedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := (1 << 20) - b.data.Len()
	if left > 0 {
		b.data.Write(p[:min(left, n)])
	}
	return n, nil
}
func (b *cappedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

func (r CommandRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	program := r.Docker
	if program == "" {
		program = "docker"
	}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	// Use the selected local context consistently; environment overrides must not
	// redirect a desktop operation to a remote Docker daemon.
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "DOCKER_HOST=") && !strings.HasPrefix(item, "DOCKER_CONTEXT=") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	configureProcess(cmd)
	var output cappedBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		if ctx.Err() != nil {
			return text, fmt.Errorf("操作超时或已取消: %w", ctx.Err())
		}
		return text, fmt.Errorf("Docker 命令未完成: %w", err)
	}
	return text, nil
}
