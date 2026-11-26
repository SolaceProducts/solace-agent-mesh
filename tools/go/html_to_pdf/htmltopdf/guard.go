// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"context"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type requestGuard struct {
	documentPath string
	resourceDir  string
	blocked      atomic.Int64

	mu               sync.Mutex
	refusedDocuments map[string]struct{}
}

func newRequestGuard(htmlFileURL string) (*requestGuard, error) {
	path, ok := fileURLPath(htmlFileURL)
	if !ok {
		return nil, errNotAFileURL(htmlFileURL)
	}
	return &requestGuard{
		documentPath:     path,
		resourceDir:      filepath.Dir(path),
		refusedDocuments: map[string]struct{}{},
	}, nil
}

type errNotAFileURL string

func (e errNotAFileURL) Error() string { return "document URL is not a file URL: " + string(e) }

func (g *requestGuard) allows(rawURL string, resourceType network.ResourceType) bool {
	if resourceType == network.ResourceTypeDocument {
		path, ok := fileURLPath(rawURL)
		return ok && path == g.documentPath
	}
	if strings.HasPrefix(rawURL, "https://") || strings.HasPrefix(rawURL, "data:") {
		return true
	}
	path, ok := fileURLPath(rawURL)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(g.resourceDir, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (g *requestGuard) BlockedRequests() int64 { return g.blocked.Load() }

func (g *requestGuard) refusedDocument(rawURL string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.refusedDocuments[rawURL]
	return ok
}

func (g *requestGuard) recordRefusal(ev fetch.EventRequestPaused) {
	g.blocked.Add(1)
	if ev.ResourceType != network.ResourceTypeDocument {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refusedDocuments[withoutFragment(ev.Request.URL)] = struct{}{}
}

func withoutFragment(rawURL string) string {
	base, _, _ := strings.Cut(rawURL, "#")
	return base
}

func (g *requestGuard) watch(tabCtx context.Context) {
	events := chromedp.Events(tabCtx, fetch.RequestPaused)
	go func() {
		for ev, err := range events {
			if err != nil {
				return
			}
			g.answer(tabCtx, ev)
		}
	}()
}

func (g *requestGuard) answer(tabCtx context.Context, ev fetch.EventRequestPaused) {
	if g.allows(ev.Request.URL, ev.ResourceType) {
		_, _ = chromedp.Call(tabCtx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: ev.RequestID})
		return
	}
	g.recordRefusal(ev)
	_, _ = chromedp.Call(tabCtx, fetch.FailRequest, fetch.FailRequestParams{
		RequestID:   ev.RequestID,
		ErrorReason: network.ErrorReasonBlockedByClient,
	})
}

func interceptAllRequests() chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		_, err := cdp.Call(ctx, t, fetch.Enable, fetch.EnableParams{
			Patterns: []*fetch.RequestPattern{{URLPattern: "*"}},
		})
		return chromedp.Void{}, err
	}
}

func fileURLPath(rawURL string) (string, bool) {
	rest, ok := strings.CutPrefix(rawURL, "file://")
	if !ok {
		return "", false
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	unescaped, err := url.PathUnescape(rest)
	if err != nil {
		return "", false
	}
	if runtime.GOOS == "windows" && len(unescaped) > 2 && unescaped[0] == '/' && unescaped[2] == ':' {
		unescaped = unescaped[1:]
	}
	return filepath.Clean(filepath.FromSlash(unescaped)), true
}
