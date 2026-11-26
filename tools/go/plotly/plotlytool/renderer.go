// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package plotlytool

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

//go:embed assets/plotly.min.js
var plotlyJS string

type errChromiumNotFound struct{ inner error }

func (e *errChromiumNotFound) Error() string { return e.inner.Error() }

type renderer interface {
	Render(ctx context.Context, figJSON, format string, scale int) ([]byte, error)
}

var defaultRenderer renderer = &chromedpRenderer{}

type chromedpRenderer struct{}

type renderOutput struct {
	DataURL string  `json:"dataURL"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
}

const blankPage = `<!doctype html>
<html><head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background:white">
<div id="gd"></div>
</body></html>`

const renderScript = `(async (figJSON, format, scale) => {
	if (typeof Plotly === 'undefined') { throw new Error('plotly library not loaded'); }
	const fig = JSON.parse(figJSON);
	const data = fig.data || [];
	const layout = fig.layout || {};
	const width = Math.ceil(layout.width || 800);
	const height = Math.ceil(layout.height || 600);
	const gd = document.getElementById('gd');
	gd.style.width = width + 'px';
	gd.style.height = height + 'px';
	await Plotly.newPlot(gd, data, layout, {staticPlot: true, responsive: false});
	const out = {width: width, height: height};
	if (format !== 'pdf') {
		out.dataURL = await Plotly.toImage(gd, {format: format, width: width, height: height, scale: scale});
	}
	return out;
})(%s, %s, %d)`

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
	dir, err := os.MkdirTemp("", "plotly-home-*")
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

func (r *chromedpRenderer) Render(ctx context.Context, figJSON, format string, scale int) ([]byte, error) {
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

	figLiteral, err := json.Marshal(figJSON)
	if err != nil {
		return nil, fmt.Errorf("marshaling figure: %w", err)
	}
	formatLiteral, err := json.Marshal(format)
	if err != nil {
		return nil, fmt.Errorf("marshaling format: %w", err)
	}
	script := fmt.Sprintf(renderScript, string(figLiteral), string(formatLiteral), scale)

	var out renderOutput
	dataURL := "data:text/html;charset=utf-8," + url.PathEscape(blankPage)

	err = chromedp.Do(timeoutCtx,
		chromedp.Navigate(dataURL),
		chromedp.WaitReady(chromedp.CSS("body")),
		chromedp.Evaluate[chromedp.Void](plotlyJS),
	)
	if err == nil {
		out, err = chromedp.Run(timeoutCtx, chromedp.Evaluate[renderOutput](script, chromedp.EvalAwaitPromise))
	}
	if err != nil {
		return nil, fmt.Errorf("rendering plotly figure: %w", err)
	}

	if format == "pdf" {
		return r.printPDF(timeoutCtx, out.Width, out.Height)
	}

	if out.DataURL == "" {
		return nil, fmt.Errorf("empty image from renderer")
	}
	return decodeDataURL(out.DataURL)
}

func (r *chromedpRenderer) printPDF(ctx context.Context, width, height float64) ([]byte, error) {
	widthIn := math.Max(width, 1) / 96.0
	heightIn := math.Max(height, 1) / 96.0

	pdfBuf, err := chromedp.Run(ctx, chromedp.PrintToPDF(
		chromedp.PDFPaper(chromedp.PaperSize{Width: widthIn, Height: heightIn}),
		chromedp.PDFMargin(0),
		chromedp.PDFPrintBackground(),
		untaggedPDF,
	))
	if err != nil {
		return nil, fmt.Errorf("printing figure to PDF: %w", err)
	}
	if len(pdfBuf) == 0 {
		return nil, fmt.Errorf("chromium produced an empty PDF")
	}
	return pdfBuf, nil
}

func untaggedPDF(p *page.PrintToPDFParams) {
	off := false
	p.GenerateTaggedPDF = &off
	p.GenerateDocumentOutline = &off
}

func decodeDataURL(dataURL string) ([]byte, error) {
	meta, payload, found := strings.Cut(dataURL, ",")
	if !found {
		return nil, fmt.Errorf("malformed data URL from renderer")
	}
	if strings.Contains(meta, ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("decoding svg data URL: %w", err)
	}
	return []byte(decoded), nil
}
