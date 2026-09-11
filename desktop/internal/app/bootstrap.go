package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type savedEmbeddingSelection struct {
	Configured     bool   `json:"configured"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	Dimensions     int    `json:"dimensions"`
	TimeoutSec     int    `json:"timeout_sec"`
	ModelVersionID string `json:"model_version_id"`
}

var versionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func savedSelection(ctx context.Context, base, selected string) (savedEmbeddingSelection, error) {
	var selection savedEmbeddingSelection
	endpoint := base + "/api/v1/embedding/saved-runtime-settings"
	if selected != "" {
		if !versionIDPattern.MatchString(selected) {
			return selection, fmt.Errorf("去重模型版本 ID 格式不正确")
		}
		endpoint += "?model_version_id=" + url.QueryEscape(selected)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return selection, e
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return selection, e
	}
	defer res.Body.Close()
	var body struct {
		Data  savedEmbeddingSelection `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); e != nil {
		return selection, fmt.Errorf("无法读取已保存的去重配置")
	}
	if res.StatusCode != 200 {
		return selection, fmt.Errorf("读取去重配置失败（HTTP %d）：%s；存在多个版本时请填写目标版本 ID", res.StatusCode, body.Error.Message)
	}
	return body.Data, nil
}
func (s savedEmbeddingSelection) validate() error {
	u, e := url.Parse(s.BaseURL)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("保存的去重服务地址无效")
	}
	if s.Dimensions != 1536 || s.TimeoutSec < 1 || !versionIDPattern.MatchString(s.ModelVersionID) || strings.TrimSpace(s.Model) == "" {
		return fmt.Errorf("保存的去重模型身份不完整")
	}
	for _, value := range []string{s.BaseURL, s.Model} {
		if strings.ContainsAny(value, "\r\n\x00$'\"#") {
			return fmt.Errorf("去重配置包含不能写入运行环境的字符")
		}
	}
	return nil
}
func (m *Manager) writeSelection(s savedEmbeddingSelection) error {
	if e := s.validate(); e != nil {
		return e
	}
	path := filepath.Join(m.runtimeDir(), ".env")
	env, e := readEnv(path)
	if e != nil {
		return e
	}
	env["ALGOFORGE_EMBEDDING_ENABLED"] = "true"
	env["ALGOFORGE_EMBEDDING_BASE_URL"] = s.BaseURL
	env["ALGOFORGE_EMBEDDING_MODEL"] = s.Model
	env["ALGOFORGE_EMBEDDING_DIMENSIONS"] = fmt.Sprint(s.Dimensions)
	env["ALGOFORGE_EMBEDDING_TIMEOUT_SEC"] = fmt.Sprint(s.TimeoutSec)
	env["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"] = s.ModelVersionID
	return writeRuntimeEnv(path, env)
}
func (m *Manager) startLocal(ctx context.Context, selected string, apply bool) (string, error) {
	if e := m.ownership(ctx); e != nil {
		return "", e
	}
	if e := m.checkImages(ctx); e != nil {
		return "", e
	}
	c, e := LoadConfig(m.dir)
	if e != nil {
		return "", e
	}
	if c.Mode != "local" {
		return "", fmt.Errorf("请先保存本机部署模式")
	}
	if e = m.checkLocalPort(ctx, c.LocalPort); e != nil {
		return "", e
	}
	if e = m.prepare(c.LocalPort); e != nil {
		return "", e
	}
	if apply {
		m.message("等待生成任务收尾后应用去重运行配置；已有向量和启用指针保持不变。")
		if out, e := m.compose(ctx, "stop", "worker"); e != nil {
			return out, e
		}
	}
	m.message("正在启动配置工作区、数据库与沙箱。")
	out, e := m.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "360", "--pull", "never", "caddy", "sandbox")
	if e != nil {
		return out, e
	}
	base := fmt.Sprintf("http://localhost:%d", c.LocalPort)
	if _, e = CheckConnection(ctx, base); e != nil {
		return out, e
	}
	env, e := readEnv(filepath.Join(m.runtimeDir(), ".env"))
	if e != nil {
		return out, e
	}
	if apply || env["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"] == "" {
		selection, e := savedSelection(ctx, base, strings.TrimSpace(selected))
		if e != nil {
			return out, e
		}
		if !selection.Configured {
			return "配置工作区已启动：" + base + "。请先在「模型配置」保存模型 API，在「去重服务」测试并保存去重配置，然后返回客户端再次启动服务。生成 worker 尚未启动。", nil
		}
		if e = m.writeSelection(selection); e != nil {
			return out, e
		}
	}
	m.message("去重运行配置已绑定，正在启动完整生成服务。")
	// Reconcile the whole stack: both API and worker receive the saved runtime
	// identity. Compose recreates them when their environment changes.
	out, e = m.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "360", "--pull", "never", "--remove-orphans")
	if e != nil {
		return out, e
	}
	return "本地完整服务已就绪：" + base + "。首次配置或更换去重模型后，请在「去重服务」完成页面要求的回填与启用；客户端不会自动切换向量指针。关闭客户端后后台任务继续运行。", nil
}
