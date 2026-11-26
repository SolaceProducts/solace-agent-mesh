// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mermaidtool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
	"github.com/chromedp/chromedp"
)

//go:embed assets/mermaid.min.js
var mermaidJS string

type errChromiumNotFound struct{ inner error }

func (e *errChromiumNotFound) Error() string { return e.inner.Error() }

type svgRenderer interface {
	RenderAll(ctx context.Context, syntax string, scale int, svgOnly bool) (*RenderResult, error)
}

type RenderResult struct {
	SVG   string
	PNG   []byte
	Title string
	Desc  string
}

type chromedpRenderer struct{}

type renderOutput struct {
	SVG    string  `json:"svg"`
	Title  string  `json:"title"`
	Desc   string  `json:"desc"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

const blankPage = `<!doctype html>
<html><head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background:white">
<div id="container"></div>
</body></html>`

const renderScript = `(async (source) => {
	if (typeof mermaid === 'undefined') { throw new Error('mermaid library not loaded'); }
	mermaid.initialize({ startOnLoad: false, securityLevel: 'loose', theme: 'default' });
	const { svg } = await mermaid.render('diag_' + Date.now(), source);
	const container = document.getElementById('container');
	container.innerHTML = svg;
	const el = container.querySelector('svg');
	if (!el) { throw new Error('no svg produced'); }
	const box = el.getBBox ? el.getBBox() : { width: el.clientWidth, height: el.clientHeight };
	let width = Math.ceil(box.width || el.clientWidth || 800);
	let height = Math.ceil(box.height || el.clientHeight || 600);
	const titleNode = el.querySelector('title');
	const descNode = el.querySelector('desc');
	return {
		svg: new XMLSerializer().serializeToString(el),
		title: titleNode ? titleNode.textContent || '' : '',
		desc: descNode ? descNode.textContent || '' : '',
		width: width,
		height: height,
	};
})(%s)`

var (
	browserMu        sync.Mutex
	sharedAllocCtx   context.Context
	sharedAllocCxl   context.CancelFunc
	sharedBrowserCtx context.Context
	sharedBrowserCxl context.CancelFunc
)

func browserOpts(chromePath, homeDir string) []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.NoSandbox,
		chromedp.Flag("headless", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Env(
			"HOME="+homeDir,
			"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
			"XDG_CACHE_HOME="+filepath.Join(homeDir, ".cache"),
		),
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-breakpad", true),
	)
}

var browserHome = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "mermaid-home-*")
	if err != nil {
		return "", err
	}
	for _, sub := range []string{".config", ".cache"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
})

func warmBrowser() (context.Context, error) {
	browserMu.Lock()
	defer browserMu.Unlock()

	if sharedBrowserCtx != nil && sharedBrowserCtx.Err() == nil {
		return sharedBrowserCtx, nil
	}
	if sharedBrowserCxl != nil {
		sharedBrowserCxl()
		sharedAllocCxl()
		sharedBrowserCtx, sharedAllocCtx = nil, nil
	}

	chromePath, err := chromium.Find()
	if err != nil {
		return nil, &errChromiumNotFound{inner: err}
	}
	homeDir, err := browserHome()
	if err != nil {
		return nil, fmt.Errorf("creating writable HOME for chromium: %w", err)
	}
	sharedAllocCtx, sharedAllocCxl = chromedp.NewExecAllocator(context.Background(), browserOpts(chromePath, homeDir)...)
	sharedBrowserCtx, sharedBrowserCxl = chromedp.NewContext(sharedAllocCtx)
	if err := chromedp.Do(sharedBrowserCtx); err != nil {
		sharedBrowserCxl()
		sharedAllocCxl()
		sharedBrowserCtx, sharedAllocCtx = nil, nil
		return nil, fmt.Errorf("launching chromium: %w", err)
	}
	return sharedBrowserCtx, nil
}

func (r *chromedpRenderer) RenderAll(ctx context.Context, syntax string, scale int, svgOnly bool) (*RenderResult, error) {
	if scale < 1 {
		scale = 1
	}
	if scale > 3 {
		scale = 3
	}

	browser, err := warmBrowser()
	if err != nil {
		return nil, err
	}

	tabCtx, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	stopPropagate := context.AfterFunc(ctx, cancelTab)
	defer stopPropagate()

	timeoutCtx, cancelTimeout := context.WithTimeout(tabCtx, 90*time.Second)
	defer cancelTimeout()

	syntaxJSON, err := json.Marshal(syntax)
	if err != nil {
		return nil, fmt.Errorf("marshaling mermaid syntax: %w", err)
	}
	script := fmt.Sprintf(renderScript, string(syntaxJSON))

	var out renderOutput
	var pngBytes []byte

	dataURL := "data:text/html;charset=utf-8," + blankPage

	err = chromedp.Do(timeoutCtx,
		chromedp.EmulateViewport(1024, 768, chromedp.EmulateScale(float64(scale))),
		chromedp.Navigate(dataURL),
		chromedp.WaitReady(chromedp.CSS("body")),
		chromedp.Evaluate[chromedp.Void](mermaidJS),
	)
	if err == nil {
		out, err = chromedp.Run(timeoutCtx, chromedp.Evaluate[renderOutput](script, chromedp.EvalAwaitPromise))
	}
	if err != nil {
		return nil, fmt.Errorf("rendering mermaid: %w", err)
	}
	if out.SVG == "" {
		return nil, fmt.Errorf("empty svg from renderer")
	}

	title := out.Title
	desc := out.Desc
	if title == "" || desc == "" {
		srcTitle, srcDesc := extractMetadata(syntax)
		if title == "" {
			title = srcTitle
		}
		if desc == "" {
			desc = srcDesc
		}
	}

	if svgOnly {
		return &RenderResult{SVG: out.SVG, Title: title, Desc: desc}, nil
	}

	width := int(math.Ceil(out.Width))
	height := int(math.Ceil(out.Height))
	if width < 1 {
		width = 800
	}
	if height < 1 {
		height = 600
	}

	err = chromedp.Do(timeoutCtx,
		chromedp.EmulateViewport(int64(width), int64(height), chromedp.EmulateScale(float64(scale))),
	)
	if err == nil {
		pngBytes, err = chromedp.Run(timeoutCtx, chromedp.Screenshot(chromedp.CSS("#container svg")))
	}
	if err != nil {
		return nil, fmt.Errorf("capturing screenshot: %w", err)
	}

	return &RenderResult{SVG: out.SVG, PNG: pngBytes, Title: title, Desc: desc}, nil
}
