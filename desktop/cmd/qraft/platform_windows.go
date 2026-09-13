//go:build windows

package main

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
	"github.com/Gingoo-TvT/Qraft/desktop/internal/desktopui"
	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var shell32 = windows.NewLazySystemDLL("shell32.dll")
var dialog32 = windows.NewLazySystemDLL("comdlg32.dll")
var windowProc uintptr

func utf(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }
func messageBox(owner uintptr, text string, flags uintptr) uintptr {
	r, _, _ := user32.NewProc("MessageBoxW").Call(owner, uintptr(unsafe.Pointer(utf(text))), uintptr(unsafe.Pointer(utf("Qraft"))), flags)
	return r
}
func fatal(e error) {
	_ = writeReport(map[string]any{"error": e.Error()})
	if !*smoke {
		messageBox(0, e.Error(), 0x10)
	}
}
func runtimeVersion() string {
	v, e := webviewloader.GetInstalledVersion()
	if e != nil {
		return ""
	}
	return v
}
func openExternal(url string) error {
	r, _, _ := shell32.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(utf("open"))), uintptr(unsafe.Pointer(utf(url))), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("无法打开系统浏览器（%d）", r)
	}
	return nil
}
func runWindow(manager *app.Manager, dir string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	restoreDPI, err := enablePerMonitorDPI()
	if err != nil {
		return err
	}
	defer restoreDPI()
	if runtimeVersion() == "" {
		if !*smoke && messageBox(0, "需要 Microsoft Edge WebView2 Runtime 才能显示桌面界面。现在打开微软官方下载页？安装后重新启动 Qraft。", 0x24) == 6 {
			_ = openExternal("https://developer.microsoft.com/microsoft-edge/webview2/")
		}
		return fmt.Errorf("尚未安装 WebView2 Runtime")
	}
	name := fmt.Sprintf("Local\\Qraft-%x", sha256.Sum256([]byte(strings.ToLower(dir))))
	mutex, e := windows.CreateMutex(nil, false, utf(name))
	if e != nil {
		if e == windows.ERROR_ALREADY_EXISTS {
			return fmt.Errorf("此数据目录的 Qraft 客户端已在运行")
		}
		return e
	}
	defer windows.CloseHandle(mutex)
	workArea := primaryWorkArea()
	width, height := initialWindowSize(systemDPI(), int(workArea.right-workArea.left), int(workArea.bottom-workArea.top))
	w := webview.NewWithOptions(webview.WebViewOptions{DataPath: filepath.Join(dir, "browser", "workbench"), AutoFocus: true, WindowOptions: webview.WindowOptions{Title: "Qraft · v" + app.Version, Width: uint(width), Height: uint(height), Center: true}})
	if w == nil {
		return fmt.Errorf("无法创建桌面窗口，请检查 WebView2 Runtime")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	setMinimum := func(width, height int) { w.SetSize(width, height, webview.HintMin) }
	positionInitialWindow(hwnd, setMinimum)
	old, _, _ := user32.NewProc("SetWindowLongPtrW").Call(hwnd, ^uintptr(3), syscall.NewCallback(func(h uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmDPIChanged && lp != 0 {
			resizeForDPI(h, int(wp&0xffff), suggestedDPIRect(lp), setMinimum)
			return 0
		}
		if msg == 0x10 && manager.Busy() {
			messageBox(h, "当前操作尚未完成，请等待结果后再关闭。更新检查或下载可在关于页取消。", 0x40)
			return 0
		}
		r, _, _ := user32.NewProc("CallWindowProcW").Call(windowProc, h, uintptr(msg), wp, lp)
		return r
	}))
	windowProc = old
	done := make(chan struct{})
	defer close(done)
	// Common dialogs must run on the same Windows UI thread as their owner.
	onUI := func(fn func() (string, error)) (string, error) {
		type reply struct {
			path string
			err  error
		}
		result := make(chan reply, 1)
		w.Dispatch(func() { p, e := fn(); result <- reply{p, e} })
		select {
		case r := <-result:
			return r.path, r.err
		case <-done:
			return "", fmt.Errorf("客户端已关闭")
		}
	}
	var ready sync.Once
	server, e := desktopui.Start(desktopui.Options{
		Manager: manager, Assets: desktopui.BundledAssets(), Native: true,
		ChooseBundle: func() (string, error) { return onUI(func() (string, error) { return chooseFile(hwnd) }) },
		ChooseSave: func(name string) (string, error) {
			return onUI(func() (string, error) { return chooseSave(hwnd, name) })
		},
		OpenExternal:      openExternal,
		RequestUpdateExit: func() { w.Dispatch(w.Terminate) },
		Ready: func(report map[string]any) {
			if !*smoke {
				return
			}
			ready.Do(func() {
				report["native_window"] = true
				report["process_dpi_awareness"] = processDPIAwareness()
				clientRect := windowClientRect(hwnd)
				report["client_width"] = clientRect.right - clientRect.left
				report["client_height"] = clientRect.bottom - clientRect.top
				report["window_dpi"] = windowDPI(hwnd)
				report["per_monitor_v2"] = windowUsesPerMonitorV2(hwnd)
				report["webview2"] = runtimeVersion()
				report["version"] = app.Version
				report["bundled_assets"] = true
				_ = writeReport(report)
				w.Dispatch(w.Terminate)
			})
		},
	})
	if e != nil {
		return e
	}
	defer server.Close()
	if *smoke {
		user32.NewProc("ShowWindow").Call(hwnd, 0)
		go func() {
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				ready.Do(func() {
					_ = writeReport(map[string]any{"error": "native workbench ready timeout"})
					w.Dispatch(w.Terminate)
				})
			}
		}()
	}
	// The entire workbench is embedded in this executable. Only API calls leave
	// the loopback UI server; service HTML never receives desktop privileges.
	w.Navigate(server.URL())
	w.Run()
	return nil
}

// OPENFILENAMEW returns only a path explicitly selected in a native dialog.
type openFileName struct {
	Size            uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefaultExt      *uint16
	CustomData      uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        uintptr
	Reserved2       uint32
	FlagsEx         uint32
}

func chooseFile(owner uintptr) (string, error) {
	return fileDialog(owner, "", "选择 Qraft 后端镜像包", "Qraft 后端包 (*.tar.gz)\x00*.tar.gz\x00所有文件\x00*.*\x00\x00", false)
}
func chooseSave(owner uintptr, name string) (string, error) {
	return fileDialog(owner, name, "保存 Qraft 导出文件", "所有文件\x00*.*\x00\x00", true)
}
func fileDialog(owner uintptr, name, title, pattern string, save bool) (string, error) {
	buffer := make([]uint16, 32768)
	copy(buffer, utf16.Encode([]rune(name)))
	filter := utf16.Encode([]rune(pattern))
	ofn := openFileName{Owner: owner, Filter: &filter[0], File: &buffer[0], MaxFile: uint32(len(buffer)), Title: utf(title), Flags: 0x800 | 0x80000 | 0x8}
	proc := "GetOpenFileNameW"
	if save {
		proc = "GetSaveFileNameW"
		ofn.Flags |= 0x2 // Ask before overwriting the selected file.
		if ext := strings.TrimPrefix(filepath.Ext(name), "."); ext != "" {
			ofn.DefaultExt = utf(ext)
		}
	} else {
		ofn.Flags |= 0x1000
	}
	ofn.Size = uint32(unsafe.Sizeof(ofn))
	r, _, _ := dialog32.NewProc(proc).Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		code, _, _ := dialog32.NewProc("CommDlgExtendedError").Call()
		if code != 0 {
			return "", fmt.Errorf("文件选择器错误：%x", code)
		}
		return "", nil
	}
	return windows.UTF16ToString(buffer), nil
}
