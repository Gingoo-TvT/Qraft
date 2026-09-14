package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const Version = "2.2.1"

// Client-only patches can keep the previously published, verified backend bundle.
const BackendVersion = "2.2.0"

type Config struct {
	SchemaVersion int    `json:"schema_version"`
	Mode          string `json:"mode"`
	ServerURL     string `json:"server_url"`
	LocalPort     int    `json:"local_port"`
}

func DefaultConfig() Config {
	return Config{SchemaVersion: 1, Mode: "remote", ServerURL: "", LocalPort: 18180}
}

func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("请输入完整的 http:// 或 https:// 服务地址")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("服务地址不能包含用户名、密码、查询参数或片段")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("服务端口必须在 1–65535 之间")
		}
	}
	if strings.Trim(u.Path, "/") != "" {
		return "", fmt.Errorf("请填写服务根地址，暂不支持路径前缀部署")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func (c Config) Validate() (Config, error) {
	if c.SchemaVersion != 1 {
		return c, fmt.Errorf("不支持的配置版本 %d，请使用匹配的客户端", c.SchemaVersion)
	}
	if c.Mode != "remote" && c.Mode != "local" {
		return c, fmt.Errorf("请选择远程连接或本地部署")
	}
	if c.LocalPort < 1024 || c.LocalPort > 65535 {
		return c, fmt.Errorf("本地端口必须在 1024–65535 之间")
	}
	if c.Mode == "local" {
		c.ServerURL = ""
		return c, nil
	}
	var err error
	c.ServerURL, err = NormalizeURL(c.ServerURL)
	return c, err
}

func LoadConfig(dir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("读取客户端配置: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("配置文件损坏，请保留文件并恢复备份: %w", err)
	}
	return c.Validate()
}

func SaveConfig(dir string, c Config) error {
	c, err := c.Validate()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("数据目录不可写: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "settings.json"))
}
