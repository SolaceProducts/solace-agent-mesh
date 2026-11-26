// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func TestRequestGuard_Allows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture paths are POSIX")
	}
	guard, err := newRequestGuard("file:///tmp/html2pdf-1/input.html")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		url  string
		typ  network.ResourceType
		want bool
	}{
		{"the input document", "file:///tmp/html2pdf-1/input.html", network.ResourceTypeDocument, true},
		{"a navigation to another local file", "file:///etc/hosts", network.ResourceTypeDocument, false},
		{"an iframe to an https page", "https://example.com/", network.ResourceTypeDocument, false},
		{"an iframe to the staged dir", "file:///tmp/html2pdf-1/other.html", network.ResourceTypeDocument, false},
		{"a staged image", "file:///tmp/html2pdf-1/chart.png", network.ResourceTypeImage, true},
		{"a staged image with an escaped name", "file:///tmp/html2pdf-1/my%20chart.png", network.ResourceTypeImage, true},
		{"a local file outside the staged dir", "file:///tmp/other/chart.png", network.ResourceTypeImage, false},
		{"a traversal out of the staged dir", "file:///tmp/html2pdf-1/../secrets.png", network.ResourceTypeImage, false},
		{"a sibling dir sharing the prefix", "file:///tmp/html2pdf-12/chart.png", network.ResourceTypeImage, false},
		{"a file URL with a host", "file://localhost/tmp/html2pdf-1/chart.png", network.ResourceTypeImage, false},
		{"an https image", "https://cdn.example.com/a.png", network.ResourceTypeImage, true},
		{"an https font", "https://fonts.example.com/a.woff2", network.ResourceTypeFont, true},
		{"a data URI", "data:image/png;base64,AAAA", network.ResourceTypeImage, true},
		{"an http image", "http://cdn.example.com/a.png", network.ResourceTypeImage, false},
		{"a link-local http endpoint", "http://169.254.169.254/latest/", network.ResourceTypeStylesheet, false},
		{"a websocket", "ws://example.com/", network.ResourceTypeWebSocket, false},
		{"a blob", "blob:null/abc", network.ResourceTypeImage, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guard.allows(tc.url, tc.typ); got != tc.want {
				t.Errorf("allows(%q, %s) = %v, want %v", tc.url, tc.typ, got, tc.want)
			}
		})
	}
}

func TestRequestGuard_DecodesRequestPausedFromTheWire(t *testing.T) {
	guard, err := newRequestGuard("file:///tmp/html2pdf-1/input.html")
	if err != nil {
		t.Fatal(err)
	}
	wire := `{"requestId":"interception-1","request":{"url":"https://example.com/frame","method":"GET","headers":{},"initialPriority":"VeryHigh","referrerPolicy":"strict-origin-when-cross-origin"},"frameId":"F1","resourceType":"Document","networkId":"n1"}`

	decoded, err := cdproto.UnmarshalMessage(&cdproto.Message{
		Method: cdproto.EventFetchRequestPaused,
		Params: []byte(wire),
	})
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}
	ev, ok := decoded.(*fetch.EventRequestPaused)
	if !ok {
		t.Fatalf("decoded %T, want *fetch.EventRequestPaused", decoded)
	}
	if ev.RequestID != "interception-1" || ev.Request == nil || ev.Request.URL != "https://example.com/frame" {
		t.Fatalf("decoded event = %+v", ev)
	}
	if ev.ResourceType != network.ResourceTypeDocument {
		t.Fatalf("resource type = %q, want %q", ev.ResourceType, network.ResourceTypeDocument)
	}
	if guard.allows(ev.Request.URL, ev.ResourceType) {
		t.Fatal("the guard allowed a navigation to an https page")
	}
	guard.recordRefusal(*ev)
	if guard.BlockedRequests() != 1 || !guard.refusedDocument("https://example.com/frame") {
		t.Errorf("blocked = %d, refused = %v: the refusal was not recorded", guard.BlockedRequests(), guard.refusedDocument("https://example.com/frame"))
	}
}

func TestNewRequestGuard_RejectsNonFileURL(t *testing.T) {
	if _, err := newRequestGuard("https://example.com/input.html"); err == nil {
		t.Fatal("expected an error for a non-file document URL")
	}
}

func TestOpenDocument_RealChromiumConfinesTheDocument(t *testing.T) {
	if _, err := chromium.Find(); err != nil {
		t.Skipf("no Chromium available: %v", err)
	}

	var httpHits, httpConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			httpConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	outsideDir := t.TempDir()
	outsideCSS := filepath.Join(outsideDir, "outside.css")
	if err := os.WriteFile(outsideCSS, []byte("body { color: red }"), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	htmlPath := filepath.Join(workDir, "input.html")
	doc := `<!doctype html><html><head>
<link rel="stylesheet" href="file://` + outsideCSS + `">
</head><body>
<p id="t">static</p>
<script>document.getElementById('t').textContent = 'scripted';</script>
<link rel="prerender" href="` + srv.URL + `/prerender">
<link rel="preconnect" href="` + srv.URL + `">
<img src="` + srv.URL + `/a.png">
<iframe src="` + srv.URL + `/frame"></iframe>
<iframe src="` + srv.URL + `/frame#section"></iframe>
<iframe sandbox srcdoc="<img src='` + srv.URL + `/sandboxed.png'>"></iframe>
<iframe sandbox src="data:text/html,<iframe src='` + srv.URL + `/nested'></iframe>"></iframe>
</body></html>`
	if err := os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	browser, err := warmBrowser()
	if err != nil {
		t.Fatalf("warmBrowser: %v", err)
	}
	tabCtx, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	logBrowserVersion(tabCtx, t)

	guard, err := openDocument(tabCtx, "file://"+htmlPath)
	if err != nil {
		t.Fatalf("openDocument: %v", err)
	}
	text, err := chromedp.Run(tabCtx, chromedp.Evaluate[string](`document.getElementById('t').textContent`))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if text != "static" {
		t.Errorf("paragraph text = %q, want %q: page script ran", text, "static")
	}
	if hits := httpHits.Load(); hits != 0 {
		t.Errorf("the http server received %d request(s), want 0", hits)
	}
	if conns := httpConns.Load(); conns != 0 {
		t.Errorf("the http server accepted %d connection(s), want 0", conns)
	}
	if blocked := guard.BlockedRequests(); blocked < 6 {
		t.Errorf("guard blocked %d request(s), want at least 6 (stylesheet, image, two iframes, two sandboxed frames)", blocked)
	}
}

func TestOpenDocument_RealChromiumUsesTheHTTPSProxyFromEnvironment(t *testing.T) {
	connects := openDocumentBehindHTTPSProxy(t, `<img src="https://example.invalid/a.png">`)
	proxyHostsSeen(t, connects, "example.invalid:443")
}

func TestOpenDocument_RealChromiumDoesNotPrerender(t *testing.T) {
	connects := openDocumentBehindHTTPSProxy(t, `<link rel="prerender" href="https://prerender.invalid/page">
<img src="https://img.invalid/a.png">`)
	if proxyHostsSeen(t, connects, "img.invalid:443")["prerender.invalid:443"] {
		t.Fatal("the prerender hint reached the proxy: a page was fetched outside the guard")
	}
}

func openDocumentBehindHTTPSProxy(t *testing.T, body string) <-chan string {
	t.Helper()
	chromePath, err := chromium.Find()
	if err != nil {
		t.Skipf("no Chromium available: %v", err)
	}

	connects := make(chan string, 64)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTPS_PROXY", proxy.URL)

	workDir := t.TempDir()
	htmlPath := filepath.Join(workDir, "input.html")
	doc := `<!doctype html><html><body>` + body + `</body></html>`
	if err = os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	homeDir, err := browserHome()
	if err != nil {
		t.Fatal(err)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), browserOpts(chromePath, homeDir)...)
	t.Cleanup(cancelAlloc)
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelTab)
	logBrowserVersion(tabCtx, t)

	if _, err := openDocument(tabCtx, "file://"+htmlPath); err != nil {
		t.Fatalf("openDocument: %v", err)
	}
	return connects
}

func proxyHostsSeen(t *testing.T, connects <-chan string, awaited string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for !seen[awaited] {
		select {
		case got := <-connects:
			seen[strings.TrimPrefix(got, "CONNECT ")] = true
		case <-deadline:
			t.Fatalf("no CONNECT %s reached the proxy named by HTTPS_PROXY", awaited)
		}
	}
	settle := time.After(2 * time.Second)
	for {
		select {
		case got := <-connects:
			seen[strings.TrimPrefix(got, "CONNECT ")] = true
		case <-settle:
			return seen
		}
	}
}

func logBrowserVersion(tabCtx context.Context, t *testing.T) {
	t.Helper()
	v, err := chromedp.CallBrowser(tabCtx, browser.GetVersion, struct{}{})
	if err != nil {
		t.Logf("browser version unavailable: %v", err)
		return
	}
	t.Logf("browser: %s", v.Product)
}
