package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const releaseAPI = "https://api.github.com/repos/Gingoo-TvT/Qraft/releases/latest"
const releaseRoot = "https://github.com/Gingoo-TvT/Qraft/releases/"
const maxClientBytes = 512 << 20

type ClientUpdate struct {
	Status         string `json:"status"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	Bytes          int64  `json:"bytes"`
	Total          int64  `json:"total"`
}
type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type clientRelease struct {
	Tag              string         `json:"tag_name"`
	Draft            bool           `json:"draft"`
	Prerelease       bool           `json:"prerelease"`
	Assets           []releaseAsset `json:"assets"`
	Version          string
	Asset            releaseAsset
	Executable       releaseAsset
	SHA256           string
	ExecutableSHA256 string
	Kind             string
}

var stableVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func compareVersions(a, b string) (int, error) {
	left, right := stableVersion.FindStringSubmatch(a), stableVersion.FindStringSubmatch(b)
	if left == nil || right == nil {
		return 0, fmt.Errorf("仅支持正式的 major.minor.patch 版本")
	}
	for i := 1; i <= 3; i++ {
		x, e := strconv.ParseUint(left[i], 10, 64)
		if e != nil {
			return 0, e
		}
		y, e := strconv.ParseUint(right[i], 10, 64)
		if e != nil {
			return 0, e
		}
		if x > y {
			return 1, nil
		}
		if x < y {
			return -1, nil
		}
	}
	return 0, nil
}
func updateURLAllowed(raw string, redirect bool) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "api.github.com":
		return u.Path == "/repos/Gingoo-TvT/Qraft/releases/latest" && u.RawQuery == ""
	case "github.com":
		return strings.HasPrefix(u.Path, "/Gingoo-TvT/Qraft/releases/download/") && u.RawQuery == ""
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return redirect
	}
	return false
}
func (m *Manager) updateClient() *http.Client {
	if m.updateHTTP != nil {
		return m.updateHTTP
	}
	return &http.Client{Timeout: 20 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !updateURLAllowed(req.URL.String(), true) {
			return fmt.Errorf("更新下载重定向到不受信任的地址")
		}
		return nil
	}}
}
func (m *Manager) updateGet(ctx context.Context, raw string) (*http.Response, error) {
	if !updateURLAllowed(raw, false) {
		return nil, fmt.Errorf("无效的 Qraft 官方更新地址")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "Qraft/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	return m.updateClient().Do(req)
}
func updateHTTPError(res *http.Response) error {
	if res.StatusCode == 403 || res.StatusCode == 429 {
		return fmt.Errorf("GitHub 请求受限，请稍后重试")
	}
	return fmt.Errorf("GitHub 更新请求失败（HTTP %d）", res.StatusCode)
}
func (m *Manager) latestClient(ctx context.Context, kind string) (*clientRelease, error) {
	res, e := m.updateGet(ctx, releaseAPI)
	if e != nil {
		return nil, fmt.Errorf("无法连接 GitHub 检查更新：%w", e)
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, nil
	}
	if res.StatusCode != 200 {
		return nil, updateHTTPError(res)
	}
	var release clientRelease
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&release); e != nil {
		return nil, fmt.Errorf("GitHub 发布信息无效：%w", e)
	}
	if release.Draft || release.Prerelease || stableVersion.FindStringSubmatch(release.Tag) == nil {
		return nil, fmt.Errorf("最新发布不是可用的正式版本")
	}
	release.Version = strings.TrimPrefix(release.Tag, "v")
	newer, e := compareVersions(release.Version, Version)
	if e != nil {
		return nil, e
	}
	if newer <= 0 {
		return &release, nil
	}
	names := map[string]releaseAsset{}
	for _, asset := range release.Assets {
		if _, ok := names[asset.Name]; ok {
			return nil, fmt.Errorf("发布附件名称重复")
		}
		names[asset.Name] = asset
	}
	executable := "Qraft-" + release.Version + "-windows-x64.exe"
	name := executable
	if kind == "installed" {
		name = "Qraft-" + release.Version + "-windows-x64-setup.exe"
	}
	asset, ok := names[name]
	if !ok {
		return nil, fmt.Errorf("此版本尚未提供 Windows 更新包")
	}
	release.Executable, ok = names[executable]
	if !ok {
		return nil, fmt.Errorf("此版本尚未提供独立 EXE 校验信息")
	}
	sums, ok := names["SHA256SUMS.txt"]
	if !ok {
		return nil, fmt.Errorf("此版本缺少 SHA256SUMS.txt，暂时不能安全更新")
	}
	for _, a := range []releaseAsset{asset, release.Executable, sums} {
		expected := releaseRoot + "download/" + release.Tag + "/" + a.Name
		if a.URL != expected || a.Size <= 0 || a.Size > maxClientBytes {
			return nil, fmt.Errorf("发布附件地址或大小无效")
		}
	}
	if sums.Size > 1<<20 {
		return nil, fmt.Errorf("校验清单过大")
	}
	checks, e := m.updateGet(ctx, sums.URL)
	if e != nil {
		return nil, fmt.Errorf("下载校验清单失败：%w", e)
	}
	defer checks.Body.Close()
	if checks.StatusCode != 200 {
		return nil, updateHTTPError(checks)
	}
	b, e := io.ReadAll(io.LimitReader(checks.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return nil, fmt.Errorf("无法读取更新校验清单")
	}
	hashes, e := parseUpdateChecksums(string(b))
	if e != nil {
		return nil, e
	}
	release.SHA256 = hashes[name]
	release.ExecutableSHA256 = hashes[executable]
	if !digest(release.SHA256) || !digest(release.ExecutableSHA256) {
		return nil, fmt.Errorf("校验清单缺少此版本的更新包或 EXE")
	}
	for _, a := range []releaseAsset{asset, release.Executable} {
		if a.Digest != "" && a.Digest != "sha256:"+hashes[a.Name] {
			return nil, fmt.Errorf("GitHub 附件摘要与校验清单不一致")
		}
	}
	release.Asset = asset
	release.Kind = kind
	return &release, nil
}
func parseUpdateChecksums(body string) (map[string]string, error) {
	result := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 || len(parts[0]) != 64 || !digest(parts[0]) {
			return nil, fmt.Errorf("更新校验清单格式无效")
		}
		name := strings.TrimPrefix(parts[1], "*")
		if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || result[name] != "" {
			return nil, fmt.Errorf("更新校验清单包含重复或无效文件名")
		}
		result[name] = strings.ToLower(parts[0])
	}
	return result, scanner.Err()
}
func clientInstallKind() string {
	exe, e := os.Executable()
	if e != nil {
		return "standalone"
	}
	dir := filepath.Dir(exe)
	if _, e = os.Stat(filepath.Join(dir, "portable.flag")); e == nil {
		return "portable"
	}
	if strings.EqualFold(filepath.Base(exe), "Qraft.exe") {
		if _, e = os.Stat(filepath.Join(dir, "Uninstall.exe")); e == nil {
			return "installed"
		}
	}
	return "standalone"
}

// A client update reserves the same operation lock as Docker actions/config writes.
func (m *Manager) BeginClientUpdate(action string, exit func()) error {
	if action == "cancel" {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.updateCancel == nil || (m.op.Action != "update-check" && m.op.Action != "update-download") {
			return fmt.Errorf("当前没有可取消的更新检查或下载")
		}
		m.updateCancel()
		m.update.Message = "正在取消，请稍候…"
		return nil
	}
	if action != "check" && action != "download" && action != "install" {
		return fmt.Errorf("未知更新操作")
	}
	if action == "install" && exit == nil {
		return fmt.Errorf("请在 Windows 客户端中安装更新")
	}
	m.mu.Lock()
	if m.op.Busy {
		m.mu.Unlock()
		return fmt.Errorf("请等待当前服务或更新操作完成")
	}
	if action == "download" && (m.updateRelease == nil || m.updateRelease.SHA256 == "") {
		m.mu.Unlock()
		return fmt.Errorf("请先检查可用更新")
	}
	if action == "install" && (m.update.Status != "ready" || m.updatePayload == "") {
		m.mu.Unlock()
		return fmt.Errorf("请先完成更新包下载和校验")
	}
	m.op = Operation{Busy: true, Action: "update-" + action, Updated: time.Now().Format(time.RFC3339)}
	m.update.Error = ""
	m.update.Status = map[string]string{"check": "checking", "download": "downloading", "install": "installing"}[action]
	m.update.Message = map[string]string{"check": "正在检查 GitHub 正式发布…", "download": "正在下载并校验更新包…", "install": "正在准备关闭客户端并更新…"}[action]
	timeout := 20 * time.Minute
	if action == "check" {
		timeout = 25 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.updateCancel = cancel
	m.mu.Unlock()
	go func() {
		defer cancel()
		var err error
		switch action {
		case "check":
			release, e := m.latestClient(ctx, clientInstallKind())
			err = e
			if e == nil {
				m.mu.Lock()
				m.updateRelease = release
				m.updatePayload = ""
				m.update.Bytes = 0
				m.update.Total = 0
				if release == nil {
					m.update.Status = "no_release"
					m.update.LatestVersion = ""
					m.update.ReleaseURL = ""
					m.update.Message = "目前还没有可用的正式版本。"
				} else {
					m.update.LatestVersion = release.Version
					m.update.ReleaseURL = releaseRoot + "tag/" + release.Tag
					newer, _ := compareVersions(release.Version, Version)
					if newer > 0 {
						m.update.Status = "available"
						m.update.Message = "发现新版本，可以下载并更新。"
					} else {
						m.update.Status = "current"
						m.update.Message = "当前已是最新版本。"
					}
				}
				m.mu.Unlock()
			}
		case "download":
			err = m.downloadClient(ctx)
		case "install":
			m.mu.Lock()
			release, payload := m.updateRelease, m.updatePayload
			m.mu.Unlock()
			err = launchClientUpdate(m.dir, payload, release)
			if err == nil {
				exit()
				return
			} // Keep the reservation until the old process exits.
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.op.Busy = false
		m.updateCancel = nil
		m.op.Updated = time.Now().Format(time.RFC3339)
		if ctx.Err() == context.Canceled {
			m.update.Status = "idle"
			if m.updateRelease != nil && m.updateRelease.SHA256 != "" {
				m.update.Status = "available"
			}
			m.update.Error = ""
			m.update.Message = "已取消更新检查或下载，可以稍后重试。"
		} else if err != nil {
			if action == "install" {
				m.update.Status = "ready"
			} else {
				m.update.Status = "error"
			}
			m.update.Error = err.Error()
			m.update.Message = "更新未完成，可以重试；当前客户端和数据仍保留。"
		}
	}()
	return nil
}
func (m *Manager) downloadClient(ctx context.Context) error {
	m.mu.Lock()
	release := *m.updateRelease
	m.mu.Unlock()
	parent := filepath.Join(m.dir, "updates")
	if e := os.MkdirAll(parent, 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(parent, "pending-")
	if e != nil {
		return e
	}
	completed := false
	defer func() {
		if !completed {
			os.Remove(filepath.Join(stage, "download.part"))
			os.Remove(stage)
		}
	}()
	res, e := m.updateGet(ctx, release.Asset.URL)
	if e != nil {
		return fmt.Errorf("更新下载失败：%w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return updateHTTPError(res)
	}
	if res.ContentLength > 0 && res.ContentLength != release.Asset.Size {
		return fmt.Errorf("更新包大小与发布信息不符")
	}
	f, e := os.OpenFile(filepath.Join(stage, "download.part"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	h := sha256.New()
	m.mu.Lock()
	m.update.Bytes = 0
	m.update.Total = release.Asset.Size
	m.mu.Unlock()
	writer := &clientProgressWriter{m: m, w: io.MultiWriter(f, h)}
	n, e := io.Copy(writer, io.LimitReader(res.Body, release.Asset.Size+1))
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return fmt.Errorf("下载中断：%w", e)
	}
	if n != release.Asset.Size || hex.EncodeToString(h.Sum(nil)) != release.SHA256 {
		return fmt.Errorf("更新包 SHA256 或大小不符，已丢弃下载，请重试")
	}
	payload := filepath.Join(stage, release.Asset.Name)
	if e = os.Rename(f.Name(), payload); e != nil {
		return e
	}
	m.mu.Lock()
	m.updatePayload = payload
	m.update.Status = "ready"
	m.update.Message = "下载完成，SHA256 校验通过。请保存正在编辑的内容，再安装并重启。"
	m.mu.Unlock()
	completed = true
	return nil
}

type clientProgressWriter struct {
	m *Manager
	w io.Writer
}

func (p *clientProgressWriter) Write(b []byte) (int, error) {
	n, e := p.w.Write(b)
	p.m.mu.Lock()
	p.m.update.Bytes += int64(n)
	p.m.mu.Unlock()
	return n, e
}
