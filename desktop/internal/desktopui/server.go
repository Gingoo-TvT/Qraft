package desktopui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"github.com/Gingoo-TvT/Qraft/desktop/internal/app"
)

type Options struct {
	Manager           *app.Manager
	Assets            fs.FS
	Native            bool
	ChooseBundle      func() (string, error)
	ChooseSave        func(string) (string, error)
	OpenExternal      func(string) error
	RequestUpdateExit func()
	Ready             func(map[string]any)
}
type Server struct {
	options     Options
	prefix      string
	origin      string
	listener    net.Listener
	http        *http.Server
	cancel      context.CancelFunc
	done        chan struct{}
	mu          sync.Mutex
	jars        map[string]*cookiejar.Jar
	downloads   []Download
	downloading bool
}

func Start(options Options) (*Server, error) {
	if options.Manager == nil || options.Assets == nil {
		return nil, fmt.Errorf("桌面工作台缺少运行配置")
	}
	if _, e := fs.ReadFile(options.Assets, "index.html"); e != nil {
		return nil, fmt.Errorf("客户端未包含桌面工作台。源码构建请先在 frontend 执行 npm run build:desktop；普通用户请下载完整安装包")
	}
	if _, e := options.Manager.Preferences(); e != nil {
		return nil, e
	}
	bytes := make([]byte, 32)
	if _, e := rand.Read(bytes); e != nil {
		return nil, e
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{options: options, prefix: "/" + hex.EncodeToString(bytes), origin: "http://" + listener.Addr().String(), listener: listener, cancel: cancel, done: make(chan struct{}), jars: map[string]*cookiejar.Jar{}}
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { defer close(s.done); _ = s.http.Serve(listener) }()
	return s, nil
}
func (s *Server) URL() string { return s.origin + s.prefix + "/ui/" }
func (s *Server) Close() error {
	s.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e := s.http.Shutdown(ctx)
	if e != nil {
		_ = s.http.Close()
	}
	<-s.done
	return e
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != s.listener.Addr().String() || !strings.HasPrefix(r.URL.Path, s.prefix+"/") {
		http.NotFound(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		http.Error(w, "请求来源不匹配", http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, s.prefix)
	if strings.HasPrefix(path, "/bridge/") {
		s.proxy(w, r, strings.TrimPrefix(path, "/bridge"))
		return
	}
	if strings.HasPrefix(path, "/native/") {
		w.Header().Set("Cache-Control", "no-store")
		s.native(w, r, strings.TrimPrefix(path, "/native/"))
		return
	}
	if path == "/ui" || strings.HasPrefix(path, "/ui/") {
		s.serveAssets(w, r, path)
		return
	}
	http.NotFound(w, r)
}
func (s *Server) serveAssets(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", 405)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(path, "/ui"), "/")
	if relative != "" && relative != "index.html" {
		if fs.ValidPath(relative) {
			if stat, e := fs.Stat(s.options.Assets, relative); e == nil && !stat.IsDir() {
				clone := r.Clone(r.Context())
				u := *r.URL
				u.Path = "/" + relative
				clone.URL = &u
				http.FileServer(http.FS(s.options.Assets)).ServeHTTP(w, clone)
				return
			}
		}
		if strings.HasPrefix(relative, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	body, e := fs.ReadFile(s.options.Assets, "index.html")
	if e != nil {
		s.fail(w, e, 500)
		return
	}
	state, e := s.options.Manager.Snapshot()
	if e != nil {
		s.fail(w, e, 500)
		return
	}
	prefs, e := s.options.Manager.Preferences()
	if e != nil {
		s.fail(w, e, 500)
		return
	}
	boot, _ := json.Marshal(map[string]any{"base": s.prefix, "api_base": s.prefix + "/bridge", "ui_base": s.prefix + "/ui", "native": s.options.Native, "state": state, "preferences": prefs})
	nonce := strings.TrimPrefix(s.prefix, "/")
	insertion := fmt.Sprintf("<base href=%q><script nonce=%q>window.__ALGOFORGE_DESKTOP__=%s;</script>", s.prefix+"/ui/", nonce, boot)
	html := strings.Replace(string(body), "<!--ALGOFORGE_BOOTSTRAP-->", insertion, 1)
	if !strings.Contains(html, "window.__ALGOFORGE_DESKTOP__=") {
		s.fail(w, fmt.Errorf("工作台构建入口不匹配"), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-src 'none'; frame-ancestors 'none'; object-src 'none'; base-uri 'self'; form-action 'self'")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(html))
	}
}
func (s *Server) result(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": v})
}
func (s *Server) fail(w http.ResponseWriter, e error, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]string{"code": "DESKTOP_ERROR", "message": e.Error()}})
}
func decode(r *http.Request, v any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
func (s *Server) native(w http.ResponseWriter, r *http.Request, path string) {
	method := http.MethodPost
	if path == "state" || path == "downloads" {
		method = http.MethodGet
	}
	if r.Method != method {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var data any
	var err error
	switch path {
	case "state":
		state, e := s.options.Manager.Snapshot()
		if e != nil {
			err = e
			break
		}
		prefs, e := s.options.Manager.Preferences()
		err = e
		data = map[string]any{"state": state, "preferences": prefs}
	case "config":
		var c app.Config
		if err = decode(r, &c); err == nil {
			err = s.options.Manager.Save(c)
		}
	case "preferences":
		var p app.Preferences
		if err = decode(r, &p); err == nil {
			err = s.options.Manager.SavePreferences(p)
		}
	case "client-update":
		var p struct {
			Action string `json:"action"`
		}
		if err = decode(r, &p); err == nil {
			s.mu.Lock()
			if s.downloading {
				err = fmt.Errorf("请等待文件导出完成后再更新")
			} else {
				err = s.options.Manager.BeginClientUpdate(p.Action, s.options.RequestUpdateExit)
			}
			s.mu.Unlock()
		}
	case "operation":
		var p struct {
			Action string `json:"action"`
			Arg    string `json:"arg"`
		}
		if err = decode(r, &p); err == nil {
			err = s.options.Manager.Begin(p.Action, p.Arg)
		}
	case "probe":
		var p struct {
			URL string `json:"url"`
		}
		if err = decode(r, &p); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			defer cancel()
			saved, e := s.options.Manager.ServerURL()
			if e != nil {
				err = e
			} else if p.URL != saved {
				err = fmt.Errorf("请先保存连接设置，再检测该服务")
			} else {
				data, err = app.CheckConnection(ctx, saved)
			}
		}
	case "choose-bundle":
		if s.options.ChooseBundle == nil {
			err = fmt.Errorf("请在 Windows 软件中选择文件")
		} else {
			data, err = s.options.ChooseBundle()
		}
	case "open-external":
		var p struct {
			URL string `json:"url"`
		}
		if err = decode(r, &p); err == nil {
			err = s.openExternal(p.URL)
		}
	case "downloads":
		s.mu.Lock()
		data = append([]Download{}, s.downloads...)
		s.mu.Unlock()
	case "download":
		var p struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err = decode(r, &p); err == nil {
			data, err = s.download(r.Context(), p.Path, p.Name, r.Header.Get("Authorization"))
		}
	case "save-text":
		var p struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err = decode(r, &p); err == nil {
			data, err = s.saveText(p.Name, p.Content)
		}
	case "ready":
		var p map[string]any
		if err = decode(r, &p); err == nil && s.options.Ready != nil {
			s.options.Ready(p)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err, 400)
		return
	}
	s.result(w, data)
}
func (s *Server) openExternal(raw string) error {
	// URL validation is shared with the UI: paths are allowed, shell/file schemes are not.
	u, e := externalURL(raw)
	if e != nil {
		return e
	}
	if s.options.OpenExternal == nil {
		return fmt.Errorf("此入口仅在 Windows 软件中可用")
	}
	return s.options.OpenExternal(u.String())
}
