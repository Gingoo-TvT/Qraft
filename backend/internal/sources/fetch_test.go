package sources

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

type fixtureResolver struct {
	mu      sync.Mutex
	answers [][]netip.Addr
	calls   int
}

func (r *fixtureResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.calls
	r.calls++
	if index >= len(r.answers) {
		index = len(r.answers) - 1
	}
	return r.answers[index], nil
}

type fixtureDialer struct {
	target    string
	mu        sync.Mutex
	addresses []string
}

func (d *fixtureDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.target)
}
func sourceFixture(t *testing.T, handler http.HandlerFunc) (*Fetcher, *fixtureResolver, *fixtureDialer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	resolver := &fixtureResolver{answers: [][]netip.Addr{{netip.MustParseAddr("8.8.8.8")}}}
	dialer := &fixtureDialer{target: server.Listener.Addr().String()}
	f := NewFetcher()
	f.client.Transport = sourceTransport(resolver, dialer)
	return f, resolver, dialer
}
func assertSourceError(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
	if !strings.Contains(err.Error(), "粘贴题面") {
		t.Fatalf("missing paste fallback: %v", err)
	}
}

func TestFetchPinsValidatedIPAndRejectsDNSRebinding(t *testing.T) {
	f, resolver, dialer := sourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("caller credentials reached source")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<title>Public source</title><main><h1>Example</h1><p>Compute a+b.</p></main>")
	})
	resolver.answers = append(resolver.answers, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	doc, err := f.Fetch(context.Background(), "http://fixture.example/problem")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindProblem || doc.FetchedAt.IsZero() || len(doc.Items) != 1 {
		t.Fatalf("document=%+v", doc)
	}
	if resolver.calls != 1 || len(dialer.addresses) != 1 || dialer.addresses[0] != "8.8.8.8:80" {
		t.Fatalf("resolver=%d dial=%v", resolver.calls, dialer.addresses)
	}
	_, err = f.Fetch(context.Background(), "http://fixture.example/problem")
	assertSourceError(t, err, "SOURCE_UNSAFE_URL")
	if len(dialer.addresses) != 1 {
		t.Fatal("private rebound IP was dialed")
	}
}

func TestFetchRejectsUnsafeURLsBeforeConnecting(t *testing.T) {
	f, _, dialer := sourceFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unsafe request reached network") })
	for _, raw := range []string{
		"file:///etc/passwd", "ftp://example.com/test", "http://user:pass@example.com/",
		"http://127.0.0.1/", "http://[::1]/", "http://[::ffff:127.0.0.1]/",
		"http://169.254.169.254/", "http://10.0.0.1/", "http://localhost/",
		"http://host.local/", "http://100.100.100.200/", "http://example.com:8080/",
	} {
		_, err := f.Fetch(context.Background(), raw)
		assertSourceError(t, err, "SOURCE_UNSAFE_URL")
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("unsafe URLs caused dials: %v", dialer.addresses)
	}
}

func TestFetchRevalidatesRedirectTargets(t *testing.T) {
	for _, target := range []string{"http://127.0.0.1/private", "http://redirect.example/private"} {
		t.Run(target, func(t *testing.T) {
			f, resolver, dialer := sourceFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) })
			resolver.answers = append(resolver.answers, []netip.Addr{netip.MustParseAddr("192.168.1.5")})
			_, err := f.Fetch(context.Background(), "http://fixture.example/start")
			assertSourceError(t, err, "SOURCE_UNSAFE_URL")
			if len(dialer.addresses) != 1 {
				t.Fatalf("unsafe redirect dialed: %v", dialer.addresses)
			}
		})
	}
}

func TestFetchRejectsMixedDNSAndSpecialRanges(t *testing.T) {
	for _, raw := range []string{"0.0.0.1", "100.64.0.1", "198.18.0.1", "192.0.2.1", "240.0.0.1", "224.1.1.1", "::", "fc00::1", "fe80::1", "2001:db8::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Errorf("reserved IP accepted: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(raw)) {
			t.Errorf("public IP rejected: %s", raw)
		}
	}
	f, resolver, dialer := sourceFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("mixed DNS reached network") })
	resolver.answers = [][]netip.Addr{{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}}
	_, err := f.Fetch(context.Background(), "http://fixture.example/")
	assertSourceError(t, err, "SOURCE_UNSAFE_URL")
	if len(dialer.addresses) != 0 {
		t.Fatal("mixed DNS was dialed")
	}
}

func TestFetchLimitsDecodedBodyAndRejectsBlockedPages(t *testing.T) {
	tests := []struct {
		name, code string
		handler    http.HandlerFunc
	}{
		{"gzip expansion", "SOURCE_TOO_LARGE", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte(strings.Repeat("a", maxBodyBytes+1)))
			_ = gz.Close()
		}},
		{"forbidden", "SOURCE_ACCESS_DENIED", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }},
		{"challenge", "SOURCE_ACCESS_DENIED", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<title>Just a moment...</title><form id='challenge-form'>verify</form>")
		}},
		{"login", "SOURCE_ACCESS_DENIED", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<title>Sign in</title><input type='password'>")
		}},
		{"binary", "SOURCE_UNSUPPORTED_FORMAT", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			fmt.Fprint(w, "archive")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, _ := sourceFixture(t, tt.handler)
			_, err := f.Fetch(context.Background(), "http://fixture.example/")
			assertSourceError(t, err, tt.code)
		})
	}
}

func TestFetchAtCoderContestLoadsOnlyTaskDirectory(t *testing.T) {
	f, _, dialer := sourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contests/abc999/tasks" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<title>Tasks</title><table><tr><td><a href='/contests/abc999/tasks/abc999_a'>A - Test</a></td></tr></table>")
	})
	doc, err := f.Fetch(context.Background(), "http://atcoder.jp/contests/abc999")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindCollection || len(doc.Items) != 1 || doc.Items[0].Statement != "" || len(dialer.addresses) != 1 {
		t.Fatalf("collection=%+v dials=%v", doc, dialer.addresses)
	}
}

func TestFetchRedirectLimitAndCancellation(t *testing.T) {
	f, _, _ := sourceFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/again", http.StatusFound) })
	_, err := f.Fetch(context.Background(), "http://fixture.example/start")
	assertSourceError(t, err, "SOURCE_REDIRECT_LIMIT")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = f.Fetch(ctx, "http://fixture.example/start")
	assertSourceError(t, err, "SOURCE_TIMEOUT")
}
