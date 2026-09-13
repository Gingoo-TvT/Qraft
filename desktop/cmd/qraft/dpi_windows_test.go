//go:build windows

package main

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestNativeWindowDPIAwarenessAndSuggestedResize(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	restore, err := enablePerMonitorDPI()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	if awareness := processDPIAwareness(); awareness != 2 {
		t.Fatalf("process DPI awareness=%d, want per-monitor (2)", awareness)
	}

	// A hidden, task-owned Win32 window exercises the real DPI APIs without
	// WebView, a service connection or changing the user's display settings.
	hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0,
		uintptr(unsafe.Pointer(utf("STATIC"))), uintptr(unsafe.Pointer(utf("Qraft DPI test"))),
		0x00CF0000, 0, 0, 640, 480, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	if !windowUsesPerMonitorV2(hwnd) {
		t.Fatal("native window is not Per Monitor v2 aware")
	}
	if dpi := windowDPI(hwnd); dpi < 96 {
		t.Fatalf("unexpected window DPI: %d", dpi)
	}
	t.Logf("process awareness=%d, window DPI=%d, Per Monitor v2=%v", processDPIAwareness(), windowDPI(hwnd), windowUsesPerMonitorV2(hwnd))
	want := windowRect{left: 120, top: 80, right: 1320, bottom: 980}
	minW, minH := 0, 0
	resizeForDPI(hwnd, 144, want, func(w, h int) { minW, minH = w, h })
	var got windowRect
	ok, _, err := user32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&got)))
	if ok == 0 {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("rectangle=%+v, want %+v", got, want)
	}
	if minW != 1200 || minH != 900 {
		t.Fatalf("minimum=%dx%d, must fit suggested bounds at 150%% DPI", minW, minH)
	}
	positionInitialWindow(hwnd, func(w, h int) { minW, minH = w, h })
	work := windowWorkArea(hwnd)
	ok, _, err = user32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&got)))
	if ok == 0 {
		t.Fatal(err)
	}
	if got.left < work.left || got.top < work.top || got.right > work.right || got.bottom > work.bottom {
		t.Fatalf("initial window %+v exceeds monitor work area %+v", got, work)
	}
	if minW > int(got.right-got.left) || minH > int(got.bottom-got.top) {
		t.Fatalf("minimum=%dx%d exceeds initial window %+v", minW, minH, got)
	}
}
