package desktopui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Download struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Status  string `json:"status"`
	Bytes   int64  `json:"bytes"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
	Created string `json:"created"`
}

func (s *Server) download(ctx context.Context, raw, name, authorization string) (Download, error) {
	return s.downloadRequest(ctx, raw, name, authorization, nil, "")
}

func (s *Server) downloadRequest(ctx context.Context, raw, name, authorization string, body json.RawMessage, csrf string) (Download, error) {
	relative, e := apiPath(raw)
	if e != nil {
		return Download{}, e
	}
	if len(body) > 0 {
		segments := strings.Split(strings.Trim(relative.Path, "/"), "/")
		if len(body) > 900<<10 || !json.Valid(body) || len(segments) != 5 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "problem-sets" || segments[4] != "export.zip" || relative.Query().Get("mode") != "testing" || relative.Query().Get("format") != "generic" {
			return Download{}, fmt.Errorf("带参数的下载仅支持通用题集测试包，参数不得超过 900 KiB")
		}
	}
	return s.save(name, func() (io.ReadCloser, int64, error) {
		target, e := s.backend()
		if e != nil {
			return nil, 0, e
		}
		target.Path = relative.Path
		target.RawQuery = relative.RawQuery
		method := http.MethodGet
		if len(body) > 0 {
			method = http.MethodPost
		}
		req, e := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
		if e != nil {
			return nil, 0, e
		}
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("X-Qraft-Client", "1")
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		client := &http.Client{Jar: s.jar(target), Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, e := client.Do(req)
		if e != nil {
			return nil, 0, e
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			var reply struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(body, &reply)
			message := reply.Error.Message
			if message == "" {
				message = fmt.Sprintf("导出失败（HTTP %d）", res.StatusCode)
			}
			res.Body.Close()
			return nil, 0, fmt.Errorf("%s", message)
		}

		return res.Body, res.ContentLength, nil
	})
}

func (s *Server) saveText(name, content string) (Download, error) {
	return s.save(name, func() (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
	})
}

// Every export waits for an explicit native destination and replaces it only
// after the entire stream has arrived. Closing the client cancels API streams.
func (s *Server) save(name string, source func() (io.ReadCloser, int64, error)) (Download, error) {
	if s.options.ChooseSave == nil {
		return Download{}, fmt.Errorf("请在 Windows 软件中使用原生另存为")
	}
	s.mu.Lock()
	if s.downloading || s.options.Manager.Busy() {
		s.mu.Unlock()
		return Download{}, fmt.Errorf("已有文件、服务或更新操作正在进行，请等待完成")
	}
	s.downloading = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.downloading = false; s.mu.Unlock() }()
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || len(name) > 200 {
		name = "Qraft-export.zip"
	}
	dest, e := s.options.ChooseSave(name)
	if e != nil {
		return Download{}, e
	}
	if dest == "" {
		return Download{Status: "cancelled"}, nil
	}
	if !filepath.IsAbs(dest) {
		return Download{}, fmt.Errorf("保存目录必须是绝对路径")
	}
	idBytes := make([]byte, 12)
	if _, e = rand.Read(idBytes); e != nil {
		return Download{}, e
	}
	d := Download{ID: hex.EncodeToString(idBytes), Name: filepath.Base(dest), Path: dest, Status: "downloading", Created: time.Now().Format(time.RFC3339)}
	s.mu.Lock()
	s.downloads = append([]Download{d}, s.downloads...)
	if len(s.downloads) > 50 {
		s.downloads = s.downloads[:50]
	}
	s.mu.Unlock()
	finish := func(err error) (Download, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range s.downloads {
			if s.downloads[i].ID == d.ID {
				if err != nil {
					s.downloads[i].Status = "failed"
					s.downloads[i].Error = err.Error()
				} else {
					s.downloads[i].Status = "completed"
				}
				d = s.downloads[i]
				break
			}
		}
		return d, err
	}
	reader, total, e := source()
	if e != nil {
		return finish(e)
	}
	defer reader.Close()
	f, e := os.CreateTemp(filepath.Dir(dest), ".qraft-export-*.part")
	if e != nil {
		return finish(e)
	}
	defer os.Remove(f.Name())
	s.mu.Lock()
	for i := range s.downloads {
		if s.downloads[i].ID == d.ID {
			s.downloads[i].Total = total
		}
	}
	s.mu.Unlock()
	writer := &progressWriter{server: s, id: d.ID, file: f}
	_, copyErr := io.Copy(writer, reader)
	if copyErr == nil {
		copyErr = f.Sync()
	}
	if e = f.Close(); copyErr == nil {
		copyErr = e
	}
	if copyErr == nil {
		copyErr = os.Rename(f.Name(), dest)
	}
	return finish(copyErr)
}

type progressWriter struct {
	server *Server
	id     string
	file   *os.File
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, e := w.file.Write(p)
	w.server.mu.Lock()
	for i := range w.server.downloads {
		if w.server.downloads[i].ID == w.id {
			w.server.downloads[i].Bytes += int64(n)
			break
		}
	}
	w.server.mu.Unlock()
	return n, e
}
