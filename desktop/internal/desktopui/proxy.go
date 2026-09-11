package desktopui

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"strings"
)

func externalURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return nil, fmt.Errorf("只能打开不含登录凭据的 HTTP 或 HTTPS 链接")
	}
	return u, nil
}
func apiPath(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" ||
		(!strings.HasPrefix(u.Path, "/api/v1/") && u.Path != "/health") ||
		strings.Contains(u.Path, "/../") || strings.Contains(u.Path, "\\") {
		return nil, fmt.Errorf("无效的服务 API 路径")
	}
	return u, nil
}
func (s *Server) backend() (*url.URL, error) {
	raw, e := s.options.Manager.ServerURL()
	if e != nil {
		return nil, e
	}
	return externalURL(raw)
}
func (s *Server) jar(target *url.URL) *cookiejar.Jar {
	key := target.Scheme + "://" + target.Host
	s.mu.Lock()
	defer s.mu.Unlock()
	jar := s.jars[key]
	if jar == nil {
		jar, _ = cookiejar.New(nil)
		s.jars[key] = jar
	}
	return jar
}
func (s *Server) proxy(w http.ResponseWriter, r *http.Request, path string) {
	relative, e := apiPath(path)
	if e != nil {
		s.fail(w, e, 404)
		return
	}
	target, e := s.backend()
	if e != nil {
		s.fail(w, e, 503)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		req.URL.Path = relative.Path
		req.URL.RawPath = ""
		req.URL.RawQuery = r.URL.RawQuery
		director(req)
		req.Host = target.Host
		// Each service has its own cookie jar. Local UI cookies never cross services.
		req.Header.Del("Cookie")
		req.Header.Del("Origin")
		req.Header.Del("Referer")
		for _, cookie := range s.jar(target).Cookies(req.URL) {
			req.AddCookie(cookie)
		}
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		s.jar(target).SetCookies(res.Request.URL, res.Cookies())
		res.Header.Del("Set-Cookie")
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			location, e := res.Location()
			if e != nil {
				return e
			}
			if location.Scheme != target.Scheme || location.Host != target.Host {
				return fmt.Errorf("服务要求跳转登录，请使用可直接访问的 API 地址")
			}
			if _, e := apiPath(location.RequestURI()); e != nil {
				return fmt.Errorf("服务要求网页登录，请先在客户端连接可访问的 API 服务")
			}
			res.Header.Set("Location", s.prefix+"/bridge"+location.RequestURI())
		}
		return nil
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		s.fail(w, fmt.Errorf("无法读取服务响应，请检查连接设置：%w", e), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
