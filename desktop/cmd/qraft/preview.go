package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
	"github.com/Gingoo-TvT/Qraft/desktop/internal/desktopui"
)

// Source-build preview uses exactly the assets and API adapter packaged in the
// Windows executable. It owns one loopback listener and exits on Ctrl+C/SIGTERM.
func serveWorkbench(manager *app.Manager) error {
	s, e := desktopui.Start(desktopui.Options{Manager: manager, Assets: desktopui.BundledAssets()})
	if e != nil {
		return e
	}
	defer s.Close()
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(done)
	if e = writeReport(map[string]any{"pid": os.Getpid(), "url": s.URL(), "version": app.Version}); e != nil {
		return e
	}
	fmt.Println("Qraft workbench preview is ready; Ctrl+C stops its listener.")
	<-done
	return nil
}
