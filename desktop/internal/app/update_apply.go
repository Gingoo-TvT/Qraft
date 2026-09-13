package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type clientUpdatePlan struct {
	PID              int    `json:"pid"`
	ProcessIdentity  uint64 `json:"process_identity"`
	Executable       string `json:"executable"`
	OriginalSHA256   string `json:"original_sha256"`
	DataDir          string `json:"data_dir"`
	Payload          string `json:"payload"`
	PayloadSHA256    string `json:"payload_sha256"`
	ExecutableSHA256 string `json:"executable_sha256"`
	Kind             string `json:"kind"`
	Version          string `json:"version"`
}
type clientUpdateResult struct {
	Version string `json:"version"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

func fileSHA256(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func copyUpdateFile(src, dest string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	return e
}
func (m *Manager) loadUpdateResult() {
	var result clientUpdateResult
	b, e := os.ReadFile(filepath.Join(m.dir, "updates", "result.json"))
	if e == nil && len(b) < 16384 && json.Unmarshal(b, &result) == nil {
		m.update.Message = result.Message
		m.update.Error = result.Error
	}
}
func (p clientUpdatePlan) validate(planPath string) error {
	if p.PID <= 0 || p.ProcessIdentity == 0 || stableVersion.FindStringSubmatch(p.Version) == nil ||
		!digest(p.OriginalSHA256) || !digest(p.PayloadSHA256) || !digest(p.ExecutableSHA256) {
		return fmt.Errorf("更新计划不完整")
	}
	if p.Kind != "installed" && p.Kind != "portable" && p.Kind != "standalone" {
		return fmt.Errorf("更新类型无效")
	}
	for _, path := range []string{p.Executable, p.DataDir, p.Payload, planPath} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
			return fmt.Errorf("更新路径无效")
		}
	}
	stage := filepath.Dir(planPath)
	if filepath.Base(planPath) != "plan.json" || !strings.HasPrefix(filepath.Base(stage), "pending-") ||
		filepath.Dir(stage) != filepath.Join(p.DataDir, "updates") || filepath.Dir(p.Payload) != stage {
		return fmt.Errorf("更新文件必须来自本客户端的数据目录")
	}
	if strings.EqualFold(p.Executable, p.Payload) || !strings.EqualFold(filepath.Ext(p.Executable), ".exe") || !strings.EqualFold(filepath.Ext(p.Payload), ".exe") {
		return fmt.Errorf("更新程序路径无效")
	}
	return nil
}

// The helper is a copy of this executable, never a downloaded script.
func launchClientUpdate(dataDir, payload string, release *clientRelease) error {
	if !clientUpdateSupported() {
		return fmt.Errorf("安装更新仅支持 Windows 客户端")
	}
	if release == nil {
		return fmt.Errorf("缺少更新发布信息")
	}
	actual, e := fileSHA256(payload)
	if e != nil || actual != release.SHA256 {
		return fmt.Errorf("更新包已变化，请重新下载并校验")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	original, e := fileSHA256(exe)
	if e != nil {
		return e
	}
	identity, e := currentUpdateProcessIdentity()
	if e != nil {
		return e
	}
	plan := clientUpdatePlan{PID: os.Getpid(), ProcessIdentity: identity, Executable: exe, OriginalSHA256: original, DataDir: dataDir,
		Payload: payload, PayloadSHA256: release.SHA256, ExecutableSHA256: release.ExecutableSHA256, Kind: release.Kind, Version: release.Version}
	stage := filepath.Dir(payload)
	planPath := filepath.Join(stage, "plan.json")
	if e = plan.validate(planPath); e != nil {
		return e
	}
	helper := filepath.Join(stage, "Qraft-update-helper.exe")
	// Retries reuse only these task-owned files, never the original executable.
	_ = os.Remove(helper)
	_ = os.Remove(filepath.Join(stage, "ready"))
	if e = copyUpdateFile(exe, helper, 0700); e != nil {
		return e
	}
	b, _ := json.Marshal(plan)
	if e = atomicWrite(planPath, b); e != nil {
		return e
	}
	cmd := exec.Command(helper, "--apply-client-update", planPath)
	configureProcess(cmd)
	if e = cmd.Start(); e != nil {
		return fmt.Errorf("无法启动更新助手：%w", e)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(filepath.Join(stage, "ready")); e == nil {
			_ = cmd.Process.Release()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return fmt.Errorf("更新助手未能就绪，客户端将保持打开，请重试")
}

// RunClientUpdate is called before any browser, config or instance startup.
func RunClientUpdate(planPath string) error {
	if !clientUpdateSupported() {
		return fmt.Errorf("更新助手仅支持 Windows")
	}
	b, e := os.ReadFile(planPath)
	if e != nil {
		return e
	}
	if len(b) > 16384 {
		return fmt.Errorf("更新计划过大")
	}
	var plan clientUpdatePlan
	if e = json.Unmarshal(b, &plan); e != nil {
		return e
	}
	if e = plan.validate(planPath); e != nil {
		return e
	}
	helper, e := os.Executable()
	if e != nil || helper != filepath.Join(filepath.Dir(planPath), "Qraft-update-helper.exe") {
		return fmt.Errorf("只能由本机更新助手执行更新")
	}
	current, e := fileSHA256(helper)
	if e != nil || current != plan.OriginalSHA256 {
		return fmt.Errorf("更新助手身份不符")
	}
	// Open the exact old process before acknowledging readiness; PID reuse is rejected.
	wait, e := prepareUpdateWait(plan)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(filepath.Dir(planPath), "ready"), []byte("ready\n"), 0600); e != nil {
		return e
	}
	result := clientUpdateResult{Version: plan.Version}
	defer func() {
		data, _ := json.Marshal(result)
		_ = atomicWrite(filepath.Join(plan.DataDir, "updates", "result.json"), append(data, '\n'))
	}()
	if e = wait(); e != nil {
		result.Error = e.Error()
		result.Message = "更新未应用，原客户端未被替换。"
		return e
	}
	if e = applyClientUpdate(plan); e != nil {
		result.Error = e.Error()
		result.Message = "更新失败，原有数据已保留。请重试或手动下载安装包。"
		if actual, hashErr := fileSHA256(plan.Executable); hashErr == nil && actual == plan.OriginalSHA256 {
			result.Message = "更新失败，原客户端已保留，正在重新打开。"
			data, _ := json.Marshal(result)
			_ = atomicWrite(filepath.Join(plan.DataDir, "updates", "result.json"), data)
			if restartErr := startUpdatedClient(plan.Executable, plan.DataDir); restartErr == nil {
				result.Message = "更新失败，已重新打开原客户端，数据已保留。"
			}
		}
		return e
	}
	result.Message = "已更新至 v" + plan.Version + "，原有连接、偏好和数据目录已保留。"
	// Write before restart so the new window immediately shows the outcome.
	data, _ := json.Marshal(result)
	_ = atomicWrite(filepath.Join(plan.DataDir, "updates", "result.json"), data)
	if e = startUpdatedClient(plan.Executable, plan.DataDir); e != nil {
		result.Error = e.Error()
		result.Message = "更新已安装，但自动启动失败。请重新打开 Qraft。"
		return e
	}
	// Keep this helper and one previous-client.exe for recovery; discard the downloaded installer.
	_ = os.Remove(plan.Payload)
	_ = os.Remove(planPath)
	_ = os.Remove(filepath.Join(filepath.Dir(planPath), "ready"))
	return nil
}

func applyClientUpdate(p clientUpdatePlan) error {
	original, e := fileSHA256(p.Executable)
	if e != nil || original != p.OriginalSHA256 {
		return fmt.Errorf("原客户端文件已变化，已取消更新")
	}
	downloaded, e := fileSHA256(p.Payload)
	if e != nil || downloaded != p.PayloadSHA256 {
		return fmt.Errorf("更新包校验失败，已取消更新")
	}
	stage := filepath.Dir(p.Payload)
	backup := filepath.Join(stage, "previous-client.exe")
	if e = copyUpdateFile(p.Executable, backup, 0700); e != nil {
		return fmt.Errorf("无法保存原客户端，已取消更新：%w", e)
	}
	restore := func(cause error) error {
		// Keep the backup if recovery fails; never remove user data.
		_ = os.Remove(p.Executable)
		if err := copyUpdateFile(backup, p.Executable, 0700); err != nil {
			return fmt.Errorf("%v；恢复失败，原程序保存在 %s：%w", cause, backup, err)
		}
		return cause
	}
	if p.Kind == "installed" {
		if e = installUpdatedClient(p.Payload, filepath.Dir(p.Executable)); e != nil {
			return restore(e)
		}
	} else {
		// Stage on the destination filesystem; Rename cannot cross disks.
		next, e := os.CreateTemp(filepath.Dir(p.Executable), ".qraft-update-*.exe")
		if e != nil {
			return e
		}
		nextPath := next.Name()
		_ = next.Close()
		_ = os.Remove(nextPath)
		defer os.Remove(nextPath)
		if e = copyUpdateFile(p.Payload, nextPath, 0700); e != nil {
			return e
		}
		if e = os.Remove(p.Executable); e != nil {
			return e
		}
		if e = os.Rename(nextPath, p.Executable); e != nil {
			return restore(e)
		}
	}
	actual, e := fileSHA256(p.Executable)
	if e != nil || actual != p.ExecutableSHA256 {
		return restore(fmt.Errorf("安装后的客户端摘要不符"))
	}
	return nil
}

func startUpdatedClient(executable, dir string) error {
	cmd := exec.Command(executable, "--data-dir", dir)
	configureProcess(cmd)
	if e := cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}
