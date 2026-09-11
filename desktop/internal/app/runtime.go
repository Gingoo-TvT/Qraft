package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BuildManifestBase64 is supplied by the release packager. Source builds retain
// remote mode, but cannot start an unverified local backend.
var BuildManifestBase64 string

//go:embed runtime
var runtimeFiles embed.FS

type Image struct {
	Tag string `json:"tag"`
	ID  string `json:"id"`
}
type RuntimeManifest struct {
	Version         string  `json:"version"`
	SourceRevision  string  `json:"source_revision"`
	BundleSHA256    string  `json:"bundle_sha256"`
	Images          []Image `json:"images"`
	SandboxImage    string  `json:"sandbox_image"`
	ToolchainDigest string  `json:"toolchain_digest"`
	SeccompDigest   string  `json:"seccomp_digest"`
}

func PackagedManifest() (RuntimeManifest, error) {
	var m RuntimeManifest
	b, e := base64.StdEncoding.DecodeString(BuildManifestBase64)
	if e != nil || len(b) == 0 {
		return m, fmt.Errorf("该客户端未包含正式后端清单，请下载完整 Release")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if e = m.Validate(); e != nil {
		return m, e
	}
	return m, nil
}
func digest(s string) bool {
	b, e := hex.DecodeString(strings.TrimPrefix(s, "sha256:"))
	return e == nil && len(b) == 32
}
func (m RuntimeManifest) Validate() error {
	if m.Version != BackendVersion || !digest(m.BundleSHA256) || len(m.Images) < 5 || len(m.SourceRevision) != 40 {
		return fmt.Errorf("后端清单不完整或与客户端版本不匹配")
	}
	seen := map[string]bool{}
	for _, im := range m.Images {
		if !strings.HasPrefix(im.Tag, "qraft-desktop/") || !strings.HasSuffix(im.Tag, ":"+BackendVersion) || !strings.HasPrefix(im.ID, "sha256:") || !digest(im.ID) || seen[im.Tag] {
			return fmt.Errorf("无效的固定镜像清单")
		}
		seen[im.Tag] = true
	}
	if !digest(m.SandboxImage) || !digest(m.ToolchainDigest) || !digest(m.SeccompDigest) {
		return fmt.Errorf("缺少 sandbox 工具链身份")
	}
	return nil
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func readEnv(path string) (map[string]string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			m[k] = v
		}
	}
	return m, nil
}
func (m *Manager) prepare(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("本地端口必须在 1024–65535 之间")
	}
	if e := m.manifest.Validate(); e != nil {
		return e
	}
	dir := m.runtimeDir()
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	envPath := filepath.Join(dir, ".env")
	env, e := readEnv(envPath)
	if os.IsNotExist(e) {
		env = map[string]string{}
		for _, k := range []string{"POSTGRES_PASSWORD", "TEMPORAL_DB_PASSWORD", "MINIO_SECRET_KEY", "JWT_SECRET", "ALGOFORGE_SETTINGS_ENCRYPTION_KEY", "QRAFT_DESKTOP_OWNER"} {
			v, e := randomSecret()
			if e != nil {
				return e
			}
			env[k] = v
		}
	} else if e != nil {
		return e
	}
	if env["QRAFT_DESKTOP_OWNER"] == "" || env["ALGOFORGE_SETTINGS_ENCRYPTION_KEY"] == "" {
		return fmt.Errorf("本地配置不完整；请恢复原 .env，避免已有模型密钥无法解密")
	}
	// A fresh API must open the configuration workspace before an embedding
	// provider exists. Only a saved, bound model identity enables generation.
	env["ALGOFORGE_EMBEDDING_ENABLED"] = "false"
	if env["ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"] != "" {
		env["ALGOFORGE_EMBEDDING_ENABLED"] = "true"
	}
	env["LOCAL_PORT"] = fmt.Sprint(port)
	env["SANDBOX_IMAGE_DIGEST"] = m.manifest.SandboxImage
	env["ALGOFORGE_S3_SANDBOX_IMAGE_DIGEST"] = m.manifest.SandboxImage
	env["ALGOFORGE_S3_SANDBOX_TOOLCHAIN_MANIFEST_DIGEST"] = m.manifest.ToolchainDigest
	env["ALGOFORGE_S3_SANDBOX_SECCOMP_POLICY_DIGEST"] = m.manifest.SeccompDigest
	// Preserve the durable credentials when preparing again or upgrading.
	if e = writeRuntimeEnv(envPath, env); e != nil {
		return e
	}
	return fs.WalkDir(runtimeFiles, "runtime", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if path == "runtime" {
				return nil
			}
			dest := filepath.Join(dir, strings.TrimPrefix(path, "runtime/"))
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
			// Container users must traverse bind-mounted configuration directories.
			return os.Chmod(dest, 0755)
		}
		b, e := runtimeFiles.ReadFile(path)
		if e != nil {
			return e
		}
		dest := filepath.Join(dir, strings.TrimPrefix(path, "runtime/"))
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		return atomicWriteMode(dest, b, 0644)
	})
}
func atomicWrite(path string, b []byte) error { return atomicWriteMode(path, b, 0600) }
func atomicWriteMode(path string, b []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".write-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func (m *Manager) importImages(ctx context.Context, path string) (string, error) {
	if e := m.manifest.Validate(); e != nil {
		return "", e
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	// Pin the exact file bytes before handing the archive to Docker.
	h := sha256.New()
	_, e = io.Copy(h, f)
	f.Close()
	if e != nil {
		return "", e
	}
	if hex.EncodeToString(h.Sum(nil)) != m.manifest.BundleSHA256 {
		return "", fmt.Errorf("后端包 SHA256 与 V%s 不匹配，请下载对应 Release 的后端包", m.manifest.Version)
	}
	m.message("正在导入后端镜像；大文件可能需要数分钟。")
	out, e := m.runner.Run(ctx, "", "load", "--input", path)
	if e != nil {
		return out, e
	}
	if e = m.checkImages(ctx); e != nil {
		return out, e
	}
	return "后端镜像已导入并验证，可以启动本地服务。", nil
}
func (m *Manager) checkImages(ctx context.Context) error {
	for _, im := range m.manifest.Images {
		out, e := m.runner.Run(ctx, "", "image", "inspect", "--format", "{{.Id}}", im.Tag)
		if e != nil || strings.TrimSpace(out) != im.ID {
			return fmt.Errorf("镜像 %s 缺失或版本不一致，请重新导入对应后端包", im.Tag)
		}
	}
	return nil
}

func writeRuntimeEnv(path string, env map[string]string) error {
	keys := []string{"POSTGRES_PASSWORD", "TEMPORAL_DB_PASSWORD", "MINIO_SECRET_KEY", "JWT_SECRET", "ALGOFORGE_SETTINGS_ENCRYPTION_KEY", "QRAFT_DESKTOP_OWNER", "LOCAL_PORT", "SANDBOX_IMAGE_DIGEST", "ALGOFORGE_S3_SANDBOX_IMAGE_DIGEST", "ALGOFORGE_S3_SANDBOX_TOOLCHAIN_MANIFEST_DIGEST", "ALGOFORGE_S3_SANDBOX_SECCOMP_POLICY_DIGEST", "ALGOFORGE_EMBEDDING_ENABLED", "ALGOFORGE_EMBEDDING_BASE_URL", "ALGOFORGE_EMBEDDING_MODEL", "ALGOFORGE_EMBEDDING_DIMENSIONS", "ALGOFORGE_EMBEDDING_TIMEOUT_SEC", "ALGOFORGE_EMBEDDING_EXPECTED_STATEMENT_MODEL_VERSION_ID"}
	var lines []string
	for _, key := range keys {
		if env[key] != "" {
			lines = append(lines, key+"="+env[key])
		}
	}
	return atomicWrite(path, []byte(strings.Join(lines, "\n")+"\n"))
}
