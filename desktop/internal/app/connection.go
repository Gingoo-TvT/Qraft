package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Connection struct {
	URL            string `json:"url"`
	ReleaseVersion string `json:"release_version"`
	SchemaVersion  string `json:"schema_version"`
	ProblemSets    bool   `json:"problem_sets"`
	Ready          bool   `json:"ready"`
}

func CheckConnection(ctx context.Context, raw string) (Connection, error) {
	base, err := NormalizeURL(raw)
	result := Connection{URL: base}
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/integration/capabilities", nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Qraft-Desktop/"+Version)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("无法连接 Qraft，请确认服务地址和网络: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return result, fmt.Errorf("服务需要身份验证，请提供允许该客户端访问 API 的服务地址")
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return result, fmt.Errorf("服务返回重定向，请填写最终服务地址；网页登录网关需要另外提供可访问的 API 地址")
	}
	if res.StatusCode != http.StatusOK {
		return result, fmt.Errorf("服务返回 HTTP %d，请使用同时提供 Web 界面与 API 的统一地址", res.StatusCode)
	}
	var payload struct {
		SchemaVersion  string `json:"schema_version"`
		ReleaseVersion string `json:"release_version"`
		ProblemSets    struct {
			Enabled bool `json:"enabled"`
		} `json:"problem_sets"`
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	if err := dec.Decode(&payload); err != nil || payload.SchemaVersion != "algoforge.integration-capabilities.v1" {
		return result, fmt.Errorf("该地址未返回 Qraft 能力接口，请检查是否误填了 API 调试端口或其他网站")
	}
	if strings.TrimSpace(payload.ReleaseVersion) == "" {
		return result, fmt.Errorf("服务版本信息缺失，请更新 Qraft 后端")
	}
	result.ReleaseVersion, result.SchemaVersion = payload.ReleaseVersion, payload.SchemaVersion
	result.ProblemSets, result.Ready = payload.ProblemSets.Enabled, true
	return result, nil
}
