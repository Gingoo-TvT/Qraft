//go:build windows

package main

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
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
	// Windows caps a top-level window at the monitor's maximum tracking size.
	// Use a valid suggested rectangle even on a small CI display; the pure
	// geometry tests cover the fixed design sizes across 100–200% scaling.
	work := windowWorkArea(hwnd)
	width, height := min(1200, int(work.right-work.left)-64), min(900, int(work.bottom-work.top)-64)
	if width <= 0 || height <= 0 {
		t.Fatalf("invalid monitor work area: %+v", work)
	}
	want := windowRect{left: work.left + 32, top: work.top + 32, right: work.left + 32 + int32(width), bottom: work.top + 32 + int32(height)}
	// Use native memory and SendMessageW, matching the lifetime and integer
	// LPARAM of a Windows-provided WM_DPICHANGED rectangle.
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	nativeRect, _, err := kernel32.NewProc("LocalAlloc").Call(0, unsafe.Sizeof(want))
	if nativeRect == 0 {
		t.Fatal(err)
	}
	defer kernel32.NewProc("LocalFree").Call(nativeRect)
	_, _, _ = copyNativeMemory.Call(nativeRect, uintptr(unsafe.Pointer(&want)), unsafe.Sizeof(want))
	minW, minH := 0, 0
	var copied windowRect
	var previousProc uintptr
	callback := syscall.NewCallback(func(h uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmDPIChanged {
			copied = suggestedDPIRect(lp)
			resizeForDPI(h, int(wp&0xffff), copied, func(w, h int) { minW, minH = w, h })
			return 0
		}
		if msg == 0x8000 { // An ordinary message may carry a small integer LPARAM.
			runtime.GC()
			return lp
		}
		result, _, _ := user32.NewProc("CallWindowProcW").Call(previousProc, h, uintptr(msg), wp, lp)
		return result
	})
	previousProc, _, err = user32.NewProc("SetWindowLongPtrW").Call(hwnd, ^uintptr(3), callback)
	if previousProc == 0 {
		t.Fatal(err)
	}
	defer user32.NewProc("SetWindowLongPtrW").Call(hwnd, ^uintptr(3), previousProc)
	_, _, _ = user32.NewProc("SendMessageW").Call(hwnd, wmDPIChanged, 144|144<<16, nativeRect)
	if copied != want {
		t.Fatalf("DPI message copied %+v, want %+v", copied, want)
	}
	integer, _, _ := user32.NewProc("SendMessageW").Call(hwnd, 0x8000, 0, 1)
	if integer != 1 {
		t.Fatalf("integer LPARAM changed: %d", integer)
	}
	var got windowRect
	ok, _, err := user32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&got)))
	if ok == 0 {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("rectangle=%+v, want %+v", got, want)
	}
	// 850×620 design pixels become 1275×930 physical pixels at 150%.
	if minW != min(1275, width) || minH != min(930, height) {
		t.Fatalf("minimum=%dx%d, want %dx%d at 150%% DPI within suggested bounds", minW, minH, min(1275, width), min(930, height))
	}
	positionInitialWindow(hwnd, func(w, h int) { minW, minH = w, h })
	work = windowWorkArea(hwnd)
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
