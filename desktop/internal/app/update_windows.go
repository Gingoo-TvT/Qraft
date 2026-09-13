//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func clientUpdateSupported() bool { return true }
func processUpdateIdentity(handle windows.Handle) (uint64, error) {
	var created, exit, kernel, user windows.Filetime
	if e := windows.GetProcessTimes(handle, &created, &exit, &kernel, &user); e != nil {
		return 0, e
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}
func currentUpdateProcessIdentity() (uint64, error) {
	handle, e := windows.GetCurrentProcess()
	if e != nil {
		return 0, e
	}
	return processUpdateIdentity(handle)
}
func prepareUpdateWait(p clientUpdatePlan) (func() error, error) {
	handle, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if e != nil {
		return nil, fmt.Errorf("无法确认原客户端进程：%w", e)
	}
	identity, e := processUpdateIdentity(handle)
	if e != nil || identity != p.ProcessIdentity {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("原客户端进程身份已变化")
	}
	return func() error {
		defer windows.CloseHandle(handle)
		state, e := windows.WaitForSingleObject(handle, uint32((30*time.Second)/time.Millisecond))
		if e != nil || state != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("原客户端尚未退出，未进行文件替换")
		}
		return nil
	}, nil
}
func installUpdatedClient(payload, dir string) error {
	test := ""
	if _, e := os.Stat(filepath.Join(dir, ".test-install")); e == nil {
		test = " /TEST"
	}
	cmd := exec.Command(payload)
	// NSIS explicitly requires an unquoted /D= directory as the last parameter.
	// This is a direct CreateProcess command line, not a shell invocation.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(payload) + " /S" + test + " /D=" + dir}
	if e := cmd.Run(); e != nil {
		return fmt.Errorf("安装程序未完成：%w", e)
	}
	return nil
}
