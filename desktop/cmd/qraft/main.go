package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

var workspace = flag.String("workspace", "", "Connect the bundled workbench to this service")
var serveUI = flag.Bool("serve-ui", false, "Preview the bundled desktop UI on an ephemeral loopback port")
var dataDir = flag.String("data-dir", "", "Client data directory")
var portable = flag.Bool("portable", false, "Keep client settings beside executable")
var diagnose = flag.Bool("diagnose", false, "Write a diagnostic JSON report without opening a window")
var probe = flag.String("probe", "", "Probe the integration API without changing settings")
var output = flag.String("output", "", "Diagnostic or smoke-test report path")
var smoke = flag.Bool("smoke-test", false, "Verify native window, page loading and local bridge, then exit")

func main() {
	flag.Parse()
	dir, e := resolveDataDir()
	if e != nil {
		fatal(e)
		return
	}
	manifest, _ := app.PackagedManifest()
	manager := app.NewManager(dir, app.CommandRunner{}, manifest)
	if *diagnose || *probe != "" {
		report := map[string]any{"client_version": app.Version, "data_dir": dir, "webview2": runtimeVersion()}
		if state, e := manager.Snapshot(); e == nil {
			report["state"] = state
		} else {
			report["error"] = e.Error()
		}
		if *probe != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			c, e := app.CheckConnection(ctx, *probe)
			cancel()
			report["connection"] = c
			if e != nil {
				report["connection_error"] = e.Error()
			}
		}
		if e = writeReport(report); e != nil {
			fatal(e)
		}
		return
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		fatal(fmt.Errorf("客户端目录不可写：%w", e))
		return
	}
	if _, e = app.LoadConfig(dir); e != nil {
		fatal(e)
		return
	}
	if *workspace != "" {
		c, err := app.LoadConfig(dir)
		if err != nil {
			fatal(err)
			return
		}
		c.Mode = "remote"
		c.ServerURL = *workspace
		if err = manager.Save(c); err != nil {
			fatal(err)
			return
		}
	}
	if *serveUI {
		if e = serveWorkbench(manager); e != nil {
			fatal(e)
		}
		return
	}
	if e = runWindow(manager, dir); e != nil {
		fatal(e)
	}
}
func resolveDataDir() (string, error) {
	if *dataDir != "" {
		return filepath.Abs(*dataDir)
	}
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	_, markerErr := os.Stat(filepath.Join(filepath.Dir(exe), "portable.flag"))
	if *portable || markerErr == nil {
		return filepath.Join(filepath.Dir(exe), "data-qraft"), nil
	}
	base, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	// Keep browser caches and configuration local to this machine on Windows.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		base = local
	}
	return filepath.Join(base, "Qraft"), nil
}
func writeReport(v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if *output != "" {
		return os.WriteFile(*output, append(b, '\n'), 0600)
	}
	_, e = fmt.Println(string(b))
	return e
}
