package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) invalidateEndpoint() {
	m.endpointMu.Lock()
	defer m.endpointMu.Unlock()
	m.endpoint, m.endpointUntil = "", time.Time{}
}

// Only a gateway owned by this directory may enable local API routing.
func (m *Manager) ownedGateway(ctx context.Context, port int) error {
	env, err := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if err != nil || env["QRAFT_DESKTOP_OWNER"] == "" {
		return fmt.Errorf("本机后端尚未启动，请先导入对应后端包并启动服务")
	}
	out, err := m.runner.Run(ctx, "", "ps", "--filter", "label=com.docker.compose.project="+Project, "--filter", "label=com.docker.compose.service=caddy", "--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	ids := strings.Fields(out)
	if len(ids) != 1 {
		return fmt.Errorf("本机后端尚未启动或网关不唯一，请在本地后端查看状态")
	}
	raw, err := m.runner.Run(ctx, "", "container", "inspect", "--format", `{"labels":{{json .Config.Labels}},"ports":{{json .NetworkSettings.Ports}}}`, ids[0])
	if err != nil {
		return err
	}
	var gateway struct {
		Labels map[string]string `json:"labels"`
		Ports  map[string][]struct {
			HostIP   string
			HostPort string
		} `json:"ports"`
	}
	if json.Unmarshal([]byte(raw), &gateway) != nil || gateway.Labels["io.qraft.desktop.owner"] != env["QRAFT_DESKTOP_OWNER"] {
		return fmt.Errorf("本机网关不属于此客户端，已停止连接")
	}
	for _, bind := range gateway.Ports["80/tcp"] {
		if bind.HostIP == "127.0.0.1" && bind.HostPort == fmt.Sprint(port) {
			return nil
		}
	}
	return fmt.Errorf("本机网关与所选端口不一致，请检查本地后端设置")
}
func (m *Manager) checkLocalPort(ctx context.Context, port int) error {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return listener.Close()
	}
	if err := m.ownedGateway(ctx, port); err == nil {
		return nil
	}
	return fmt.Errorf("本地端口 %d 已被其他程序占用，请选择其他端口；不会连接或接管该服务", port)
}
func (m *Manager) ServerURL() (string, error) {
	c, err := LoadConfig(m.dir)
	if err != nil {
		return "", err
	}
	if c.Mode != "local" {
		if c.ServerURL == "" {
			return "", fmt.Errorf("请先选择自行部署或填写已有服务地址")
		}
		return c.ServerURL, nil
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", c.LocalPort)
	m.endpointMu.Lock()
	defer m.endpointMu.Unlock()
	if m.endpoint == base && time.Now().Before(m.endpointUntil) {
		return base, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.ownedGateway(ctx, c.LocalPort); err != nil {
		m.endpoint = ""
		return "", err
	}
	m.endpoint, m.endpointUntil = base, time.Now().Add(time.Second)
	return base, nil
}
