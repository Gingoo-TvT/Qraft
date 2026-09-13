package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const Project = "qraft-desktop"

type Operation struct {
	Busy    bool   `json:"busy"`
	Action  string `json:"action"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Updated string `json:"updated"`
}
type State struct {
	Configured bool         `json:"configured"`
	ServiceURL string       `json:"service_url"`
	Version    string       `json:"version"`
	DataDir    string       `json:"data_dir"`
	Config     Config       `json:"config"`
	Packaged   bool         `json:"packaged"`
	Operation  Operation    `json:"operation"`
	Update     ClientUpdate `json:"update"`
}
type Manager struct {
	dir           string
	runner        Runner
	manifest      RuntimeManifest
	mu            sync.Mutex
	endpointMu    sync.Mutex
	endpoint      string
	endpointUntil time.Time
	update        ClientUpdate
	updateRelease *clientRelease
	updatePayload string
	updateHTTP    *http.Client
	updateCancel  context.CancelFunc
	op            Operation
}

func NewManager(dir string, runner Runner, manifest RuntimeManifest) *Manager {
	m := &Manager{dir: dir, runner: runner, manifest: manifest}
	m.update = ClientUpdate{Status: "idle", CurrentVersion: Version}
	m.loadUpdateResult()
	return m
}
func (m *Manager) runtimeDir() string { return filepath.Join(m.dir, "backend") }
func (m *Manager) Snapshot() (State, error) {
	c, e := LoadConfig(m.dir)
	if e != nil {
		return State{}, e
	}
	configured := c.Mode == "local" || c.ServerURL != ""
	endpoint, _ := m.ServerURL()
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{Configured: configured, ServiceURL: endpoint, Version: Version, DataDir: m.dir, Config: c, Packaged: m.manifest.Validate() == nil, Operation: m.op, Update: m.update}, nil
}
func (m *Manager) Busy() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.op.Busy }
func (m *Manager) Save(c Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.op.Busy {
		return fmt.Errorf("请等待当前操作完成后再更改设置")
	}
	m.invalidateEndpoint()
	return SaveConfig(m.dir, c)
}
func (m *Manager) message(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.op.Message = s
	m.op.Updated = time.Now().Format(time.RFC3339)
}
func (m *Manager) Begin(action, arg string) error {
	switch action {
	case "check", "import", "start", "sync", "stop", "status", "logs":
	default:
		return fmt.Errorf("未知操作")
	}
	m.mu.Lock()
	if m.op.Busy {
		m.mu.Unlock()
		return fmt.Errorf("已有操作正在进行，请等待完成")
	}
	m.op = Operation{Busy: true, Action: action, Message: "正在处理…", Updated: time.Now().Format(time.RFC3339)}
	m.mu.Unlock()
	go func() {
		// The window cannot close while an owned command is running. Worker shutdown
		// retains the server's 40-minute drain deadline.
		timeout := 10 * time.Minute
		if action == "import" {
			timeout = 40 * time.Minute
		}
		if action == "stop" || action == "sync" {
			timeout = 45 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		out, e := m.Run(ctx, action, arg)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.op.Busy = false
		m.op.Message = out
		m.op.Updated = time.Now().Format(time.RFC3339)
		if e != nil {
			m.op.Error = e.Error()
		}
	}()
	return nil
}

// Run is also used by the command-line diagnostics and integration tests.
func (m *Manager) Run(ctx context.Context, action, arg string) (string, error) {
	if action == "start" || action == "sync" || action == "stop" {
		m.invalidateEndpoint()
		defer m.invalidateEndpoint()
	}
	if e := m.prerequisites(ctx); e != nil {
		return "", e
	}
	switch action {
	case "check":
		return "Docker Linux 容器、cgroup v2 和 Compose 均可用。", nil
	case "import":
		return m.importImages(ctx, arg)
	case "start", "sync":
		return m.startLocal(ctx, arg, action == "sync")
	case "stop":
		if e := m.ownership(ctx); e != nil {
			return "", e
		}
		if _, e := os.Stat(filepath.Join(m.runtimeDir(), ".env")); e != nil {
			return "", fmt.Errorf("尚未部署此目录的本地后端")
		}
		m.message("正在安全停止；正在执行的生成任务最多需要 40 分钟完成收尾。")
		// Stop the worker before its dependencies. Never remove volumes or force kill.
		out, e := m.compose(ctx, "stop", "worker")
		if e != nil {
			return out, e
		}
		rest, e := m.compose(ctx, "stop")
		return out + "\n" + rest, e
	case "status", "logs":
		if e := m.ownership(ctx); e != nil {
			return "", e
		}
		if _, e := os.Stat(filepath.Join(m.runtimeDir(), "compose.yml")); e != nil {
			return "此目录尚未部署本地后端。", nil
		}
		if action == "logs" {
			out, e := m.compose(ctx, "logs", "--no-color", "--tail", "80", "api", "worker", "sandbox")
			return m.redact(out), e
		}
		return m.compose(ctx, "ps", "-a")
	default:
		return "", fmt.Errorf("未知操作")
	}
}
func (m *Manager) prerequisites(ctx context.Context) error {
	endpoint, e := m.runner.Run(ctx, "", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	if e != nil {
		return fmt.Errorf("未找到可用 Docker，请先安装并启动 Docker Desktop，启用 Linux 容器")
	}
	if !strings.HasPrefix(endpoint, "npipe://") && !strings.HasPrefix(endpoint, "unix://") {
		return fmt.Errorf("本地部署仅支持本机 Docker；请将 Docker context 切回本机，远程服务请使用连接模式")
	}
	info, e := m.runner.Run(ctx, "", "info", "--format", "{{.OSType}} {{.Architecture}} {{.CgroupVersion}}")
	if e != nil {
		return fmt.Errorf("Docker 尚未就绪，请启动 Docker Desktop 并等待引擎运行")
	}
	fields := strings.Fields(info)
	if len(fields) != 3 || fields[0] != "linux" || (fields[1] != "x86_64" && fields[1] != "amd64") || fields[2] != "2" {
		return fmt.Errorf("本地后端需要 x64 Linux 容器与 cgroup v2；当前：%s", info)
	}
	if _, e = m.runner.Run(ctx, "", "compose", "version", "--short"); e != nil {
		return fmt.Errorf("缺少 Docker Compose v2，请更新 Docker Desktop")
	}
	return nil
}

// Containers can be gone while their data remains. Every persistent resource
// must belong to this client directory before Compose is allowed to reuse it.
func (m *Manager) ownership(ctx context.Context) error {
	env, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	for _, kind := range []string{"container", "volume", "network"} {
		var args []string
		format := "{{json .Labels}}"
		if kind == "container" {
			args = []string{"ps", "-a", "--filter", "label=com.docker.compose.project=" + Project, "--format", "{{.ID}}"}
			format = "{{json .Config.Labels}}"
		} else {
			args = []string{kind, "ls", "--filter", "name=" + Project + "_", "--format", "{{.Name}}"}
		}
		out, err := m.runner.Run(ctx, "", args...)
		if err != nil {
			return err
		}
		for _, id := range strings.Fields(out) {
			if kind != "container" && !strings.HasPrefix(id, Project+"_") {
				continue
			}
			labelsJSON, err := m.runner.Run(ctx, "", kind, "inspect", "--format", format, id)
			if err != nil {
				return err
			}
			var labels map[string]string
			if json.Unmarshal([]byte(labelsJSON), &labels) != nil || env["QRAFT_DESKTOP_OWNER"] == "" || labels["io.qraft.desktop.owner"] != env["QRAFT_DESKTOP_OWNER"] {
				return fmt.Errorf("本机资源 %s 属于其他数据目录或归属不明；请使用原客户端数据目录，已有数据不会被接管", id)
			}
		}
	}
	return nil
}
func (m *Manager) compose(ctx context.Context, args ...string) (string, error) {
	base := []string{"compose", "--project-name", Project, "--env-file", filepath.Join(m.runtimeDir(), ".env"), "--file", filepath.Join(m.runtimeDir(), "compose.yml")}
	return m.runner.Run(ctx, m.runtimeDir(), append(base, args...)...)
}
func (m *Manager) redact(out string) string {
	env, e := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if e != nil {
		return out
	}
	for k, v := range env {
		if len(v) > 8 && (strings.Contains(k, "SECRET") || strings.Contains(k, "PASSWORD") || strings.Contains(k, "KEY")) {
			out = strings.ReplaceAll(out, v, "[已隐藏]")
		}
	}
	return out
}
