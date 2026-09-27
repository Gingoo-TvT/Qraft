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
	// Reject requests initiated by a page from a previously selected service.
	// In particular, never forward its in-memory CSRF token to the new service.
	if selected := r.Header.Get("X-Qraft-Service"); selected != "" && strings.TrimRight(selected, "/") != strings.TrimRight(target.String(), "/") {
		s.fail(w, fmt.Errorf("服务连接已切换，请重新打开当前页面"), http.StatusConflict)
		return
	}
	// Invited reviews are anonymous even when the desktop has an admin session.
	publicReview := relative.Path == "/api/v1/public/rating/review"
	invitationRead := relative.Path == "/api/v1/auth/invitation"
	authWrite := relative.Path == "/api/v1/auth/login" || relative.Path == "/api/v1/auth/register" || relative.Path == "/api/v1/auth/bootstrap" || relative.Path == "/api/v1/auth/reset-password"
	anonymous := publicReview || invitationRead || authWrite
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
		req.Header.Del("X-Qraft-Service")
		req.Header.Del("Origin")
		req.Header.Del("Referer")
		if anonymous {
			req.Header.Del("Authorization")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("X-Actor")
			req.Header.Del("X-CSRF-Token")
		} else {
			for _, cookie := range s.jar(target).Cookies(req.URL) {
				req.AddCookie(cookie)
			}
		}
		if !publicReview {
			req.Header.Del("X-Qraft-Review-Token")
		}
		if !invitationRead {
			req.Header.Del("X-Qraft-Invitation-Token")
		}
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		if !publicReview && !invitationRead {
			s.jar(target).SetCookies(res.Request.URL, res.Cookies())
		}
		res.Header.Del("Set-Cookie")
		if relative.Path == "/api/v1/auth/logout" && res.StatusCode >= 200 && res.StatusCode < 300 {
			s.mu.Lock()
			delete(s.jars, target.Scheme+"://"+target.Host)
			s.mu.Unlock()
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			// Never replay an invitation token at a redirected endpoint.
			if anonymous {
				return fmt.Errorf("账号或评价接口不支持跳转，请检查服务地址")
			}
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
