//go:build !windows

package app

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
