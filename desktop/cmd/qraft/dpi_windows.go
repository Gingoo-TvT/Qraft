//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	wmDPIChanged      = 0x02E0
)

type windowRect struct {
	left, top, right, bottom int32
}

// Called on the locked UI thread before any window or DPI-dependent API.
// Keeping this in the executable also covers plain "go build", without a
// separate resource-generation step or changes to the WebView dependency.
func enablePerMonitorDPI() (func(), error) {
	process := user32.NewProc("SetProcessDpiAwarenessContext")
	if process.Find() == nil {
		// A manifest or the host may already have set the process default.
		// The UI thread below must still explicitly use Per Monitor v2.
		_, _, _ = process.Call(perMonitorAwareV2)
	}
	thread := user32.NewProc("SetThreadDpiAwarenessContext")
	if err := thread.Find(); err != nil {
		return nil, fmt.Errorf("此客户端需要支持 Per Monitor v2 的 Windows 10 或更新系统：%w", err)
	}
	previous, _, err := thread.Call(perMonitorAwareV2)
	if previous == 0 {
		return nil, fmt.Errorf("无法启用窗口 DPI 缩放：%w", err)
	}
	return func() { _, _, _ = thread.Call(previous) }, nil
}

func systemDPI() int {
	value, _, _ := user32.NewProc("GetDpiForSystem").Call()
	if value == 0 {
		return 96
	}
	return int(value)
}

func windowDPI(hwnd uintptr) int {
	value, _, _ := user32.NewProc("GetDpiForWindow").Call(hwnd)
	if value == 0 {
		return 96
	}
	return int(value)
}

func windowUsesPerMonitorV2(hwnd uintptr) bool {
	context, _, _ := user32.NewProc("GetWindowDpiAwarenessContext").Call(hwnd)
	equal, _, _ := user32.NewProc("AreDpiAwarenessContextsEqual").Call(context, perMonitorAwareV2)
	return equal != 0
}

// Windows supplies a physical-pixel rectangle when scaling changes, including
// movement between monitors. SetWindowPos produces WM_SIZE; forwarding that
// message to the WebView host updates its controller bounds. WebView2 tracks
// the parent monitor's rasterization scale itself.
func resizeForDPI(hwnd uintptr, dpi int, rect windowRect, setMinimum func(int, int)) {
	width, height := int(rect.right-rect.left), int(rect.bottom-rect.top)
	setMinimum(minimumWindowSize(dpi, width, height))
	_, _, _ = user32.NewProc("SetWindowPos").Call(hwnd, 0,
		uintptr(rect.left), uintptr(rect.top), uintptr(width), uintptr(height),
		0x0004|0x0010) // SWP_NOZORDER | SWP_NOACTIVATE
}

var copyNativeMemory = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlMoveMemory")

func suggestedDPIRect(lp uintptr) windowRect {
	// LPARAM is also used for packed integers, so the WndProc must keep it as
	// uintptr. Only WM_DPICHANGED supplies this native RECT address. Copy its
	// contents through Win32 instead of converting the address to a Go pointer.
	var rect windowRect
	_, _, _ = copyNativeMemory.Call(uintptr(unsafe.Pointer(&rect)), lp, unsafe.Sizeof(rect))
	return rect
}

func primaryWorkArea() windowRect {
	var rect windowRect
	ok, _, _ := user32.NewProc("SystemParametersInfoW").Call(0x0030, 0, uintptr(unsafe.Pointer(&rect)), 0) // SPI_GETWORKAREA
	if ok == 0 {
		width, _, _ := user32.NewProc("GetSystemMetrics").Call(0)
		height, _, _ := user32.NewProc("GetSystemMetrics").Call(1)
		return windowRect{right: int32(width), bottom: int32(height)}
	}
	return rect
}

func windowWorkArea(hwnd uintptr) windowRect {
	type monitorInfo struct {
		size          uint32
		monitor, work windowRect
		flags         uint32
	}
	monitor, _, _ := user32.NewProc("MonitorFromWindow").Call(hwnd, 2) // MONITOR_DEFAULTTONEAREST
	info := monitorInfo{}
	info.size = uint32(unsafe.Sizeof(info))
	ok, _, _ := user32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return primaryWorkArea()
	}
	return info.work
}

func positionInitialWindow(hwnd uintptr, setMinimum func(int, int)) {
	work := windowWorkArea(hwnd)
	dpi := windowDPI(hwnd)
	width, height := initialWindowSize(dpi, int(work.right-work.left), int(work.bottom-work.top))
	left, top := work.left+(work.right-work.left-int32(width))/2, work.top+(work.bottom-work.top-int32(height))/2
	resizeForDPI(hwnd, dpi, windowRect{left, top, left + int32(width), top + int32(height)}, setMinimum)
}

func processDPIAwareness() int {
	var awareness uint32
	hresult, _, _ := windows.NewLazySystemDLL("shcore.dll").NewProc("GetProcessDpiAwareness").Call(0, uintptr(unsafe.Pointer(&awareness)))
	if hresult != 0 {
		return -1
	}
	return int(awareness) // 0 unaware, 1 system, 2 per-monitor
}

func windowClientRect(hwnd uintptr) windowRect {
	var rect windowRect
	_, _, _ = user32.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return rect
}
