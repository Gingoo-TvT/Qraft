//go:build !windows

package main

import (
	"fmt"
	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

func runtimeVersion() string { return "Windows only" }
func fatal(e error)          { fmt.Println(e) }
func runWindow(*app.Manager, string) error {
	return fmt.Errorf("桌面窗口仅支持 Windows；Linux 请使用 Web 界面")
}
