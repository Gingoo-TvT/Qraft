// Package sources fetches public question statements without credentials or
// browser execution. It never downloads or imports judge data.
package sources

import (
	"context"
	"errors"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	KindProblem    = "problem"
	KindCollection = "collection"
	maxURLBytes    = 8192
	maxBodyBytes   = 2 << 20
	maxMembers     = 100
	fetchTimeout   = 20 * time.Second
)

type Document struct {
	URL       string    `json:"url"`
	FinalURL  string    `json:"final_url"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Items     []Item    `json:"items"`
	FetchedAt time.Time `json:"fetched_at"`
	Warnings  []string  `json:"warnings,omitempty"`
}

type Item struct {
	Difficulty      *domain.SourceDifficulty `json:"difficulty,omitempty"`
	ID              string                   `json:"id"`
	URL             string                   `json:"url"`
	Title           string                   `json:"title"`
	Statement       string                   `json:"statement,omitempty"`
	Editorial       string                   `json:"editorial,omitempty"`
	StatementSHA256 string                   `json:"statement_sha256,omitempty"`
	Warnings        []string                 `json:"warnings,omitempty"`
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }
func sourceError(code, message string) error {
	return &Error{Code: code, Message: message + "；也可以粘贴题面继续。"}
}

type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type networkDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Fetcher struct{ client *http.Client }

// NewFetcher creates a client that pins each connection to a validated public IP.
// Proxy environment variables, cookies and caller authentication are not used.
func NewFetcher() *Fetcher {
	return &Fetcher{client: &http.Client{
		Timeout:   fetchTimeout,
		Transport: sourceTransport(net.DefaultResolver, &net.Dialer{Timeout: 5 * time.Second}),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return sourceError("SOURCE_REDIRECT_LIMIT", "页面跳转次数过多")
			}
			if _, err := validateURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return sourceError("SOURCE_UNSAFE_URL", "不允许从 HTTPS 跳转到不安全连接")
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Referer")
			return nil
		},
	}}
}

func Fetch(ctx context.Context, rawURL string) (*Document, error) {
	return NewFetcher().Fetch(ctx, rawURL)
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Document, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	original := u.String()
	nowcoder := isNowcoderTask(u)
	// The public app endpoint preserves equations and bounds stripped from SEO HTML.
	// Use the same bounded, credential-free, SSRF-checked transport.
	if nowcoder {
		u.Path = "/practice/terminal/" + path.Base(strings.TrimRight(u.Path, "/"))
		u.RawPath = ""
		u.RawQuery = ""
	}
	// AtCoder's contest home does not list its tasks. Its public /tasks page is
	// the collection document; this is one fetch, not recursive crawling.
	if isAtCoder(u.Hostname()) && atCoderContestRoot.MatchString(u.Path) {
		u.Path = strings.TrimRight(u.Path, "/") + "/tasks"
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, sourceError("SOURCE_INVALID_URL", "链接格式无效")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/markdown,text/plain;q=0.8")
	req.Header.Set("User-Agent", "Qraft-SourcePreview/1.0")
	if nowcoder {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := f.client.Do(req)
	if err != nil {
		var underlying *Error
		if errors.As(err, &underlying) {
			return nil, underlying
		}
		if ctx.Err() != nil {
			return nil, sourceError("SOURCE_TIMEOUT", "获取页面已超时或取消")
		}
		return nil, sourceError("SOURCE_FETCH_FAILED", "无法连接来源站点或验证其证书")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, sourceError("SOURCE_ACCESS_DENIED", "来源站点要求登录、限制访问或启用了反爬验证")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, sourceError("SOURCE_FETCH_FAILED", fmt.Sprintf("来源站点返回 HTTP %d", resp.StatusCode))
	}
	if resp.ContentLength > maxBodyBytes {
		return nil, sourceError("SOURCE_TOO_LARGE", "页面正文超过 2 MiB 限制")
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mediaType {
	case "application/json":
		if !nowcoder {
			return nil, sourceError("SOURCE_UNSUPPORTED_FORMAT", "链接不是题面页面")
		}
	case "", "text/html", "application/xhtml+xml", "text/plain", "text/markdown", "text/x-markdown":
	default:
		return nil, sourceError("SOURCE_UNSUPPORTED_FORMAT", "链接不是 HTML、Markdown 或纯文本页面")
	}
	// Go's transport decompresses gzip first; this limit applies to decoded
	// content, so compressed pages cannot bypass the body limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, sourceError("SOURCE_FETCH_FAILED", "读取页面正文失败")
	}
	if len(body) > maxBodyBytes {
		return nil, sourceError("SOURCE_TOO_LARGE", "页面正文超过 2 MiB 限制")
	}
	if nowcoder {
		doc, err := parseNowcoderAPI(original, body)
		if err != nil {
			return nil, err
		}
		doc.FetchedAt = time.Now().UTC()
		stampDifficulties(doc)
		return doc, nil
	}
	if mediaType == "" {
		mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
		if mediaType != "text/html" && mediaType != "text/plain" {
			return nil, sourceError("SOURCE_UNSUPPORTED_FORMAT", "无法识别为网页或文本")
		}
	}
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		reader, err := charset.NewReader(strings.NewReader(string(body)), resp.Header.Get("Content-Type"))
		if err != nil {
			return nil, sourceError("SOURCE_INVALID_CONTENT", "无法识别网页字符编码")
		}
		body, err = io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
		if err != nil || len(body) > maxBodyBytes {
			return nil, sourceError("SOURCE_TOO_LARGE", "转换后的正文超过大小限制")
		}
	}
	document, err := parseDocument(original, resp.Request.URL.String(), mediaType, body)
	if err != nil {
		return nil, err
	}
	document.FetchedAt = time.Now().UTC()
	stampDifficulties(document)
	return document, nil
}

func validateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLBytes {
		return nil, sourceError("SOURCE_INVALID_URL", "请输入有效的公开网页链接")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, sourceError("SOURCE_UNSAFE_URL", "仅支持不含账号密码的 HTTP 或 HTTPS 公开链接")
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return nil, sourceError("SOURCE_UNSAFE_URL", "只允许标准网页端口 80 或 443")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicIP(ip) {
			return nil, sourceError("SOURCE_UNSAFE_URL", "不允许访问本机、内网或保留地址")
		}
	} else if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.Contains(host, "%") {
		return nil, sourceError("SOURCE_UNSAFE_URL", "不允许访问本机或内部域名")
	}
	u.Fragment = ""
	return u, nil
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func sourceTransport(resolver ipResolver, dialer networkDialer) *http.Transport {
	return &http.Transport{
		Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, sourceError("SOURCE_UNSAFE_URL", "来源地址无效")
			}
			var ips []netip.Addr
			if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
				ips = []netip.Addr{ip}
			} else {
				ips, err = resolver.LookupNetIP(ctx, "ip", host)
				if err != nil || len(ips) == 0 {
					return nil, sourceError("SOURCE_FETCH_FAILED", "无法解析来源站点")
				}
			}
			// Reject mixed public/private answers too. The validated numeric IP,
			// never the hostname, is passed to DialContext (DNS rebinding safe).
			for _, ip := range ips {
				if !publicIP(ip) {
					return nil, sourceError("SOURCE_UNSAFE_URL", "来源域名指向本机、内网或保留地址")
				}
			}
			for _, ip := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
				if dialErr == nil {
					return conn, nil
				}
				err = dialErr
				if ctx.Err() != nil {
					break
				}
			}
			return nil, err
		},
	}
}
