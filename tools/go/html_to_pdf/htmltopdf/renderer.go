// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/http/httpproxy"
)

type errChromiumNotFound struct{ inner error }

func (e *errChromiumNotFound) Error() string { return e.inner.Error() }

type printOptions struct {
	paperWidth      float64
	paperHeight     float64
	landscape       bool
	printBackground bool
	margins         *float64
}

type renderOutput struct {
	pdf             []byte
	blockedRequests int64
}

type pdfRenderer interface {
	Render(ctx context.Context, htmlFileURL string, opts printOptions) (renderOutput, error)
}

type chromedpRenderer struct{}

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
		chromedp.Flag("blink-settings", scriptDisabledBlinkSettings),
		chromedp.Flag("disable-features", isolatedFrameFeatures),
		chromedp.Flag("enable-features", prefetchHoldbackFeatures),
		chromedp.Flag("proxy-server", proxyServers()),
		chromedp.Flag("proxy-bypass-list", proxyLoopbackToo),
		crashpadWritableHomeEnv(homeDir),
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-breakpad", true),
	)
}

func crashpadWritableHomeEnv(homeDir string) chromedp.ExecAllocatorOption {
	return chromedp.Env(
		"HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(homeDir, ".cache"),
	)
}

var browserHome = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "html2pdf-home-*")
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

const fontsReadyScript = `(async () => {
	if (document.fonts && document.fonts.ready) { await document.fonts.ready; }
	return true;
})()`

const scriptDisabledBlinkSettings = "scriptEnabled=false"

const isolatedFrameFeatures = "IsolateSandboxedIframes"

const prefetchHoldbackFeatures = "NetworkService,NetworkServiceInProcess,NoStatePrefetchHoldback"

const blackHoleHTTPProxy = "http=127.0.0.1:9;ws=127.0.0.1:9"

const proxyLoopbackToo = "<-loopback>"

func proxyServers() string {
	httpsProxy, ok := environmentHTTPSProxy()
	if !ok {
		return blackHoleHTTPProxy
	}
	return blackHoleHTTPProxy + ";https=" + httpsProxy
}

func environmentHTTPSProxy() (string, bool) {
	raw := httpproxy.FromEnvironment().HTTPSProxy
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || strings.ContainsAny(u.Host, proxyRuleSeparators) {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

const proxyRuleSeparators = ";,="

const (
	isolatedFrameTargetType = "iframe"
	chromiumErrorPagePrefix = "chrome-error://"
)

func (r *chromedpRenderer) Render(ctx context.Context, htmlFileURL string, opts printOptions) (renderOutput, error) {
	browser, err := warmBrowser()
	if err != nil {
		return renderOutput{}, err
	}

	tabCtx, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	stopPropagate := context.AfterFunc(ctx, cancelTab)
	defer stopPropagate()

	guard, err := openDocument(tabCtx, htmlFileURL)
	if err != nil {
		return renderOutput{}, fmt.Errorf("rendering HTML to PDF: %w", err)
	}
	pdfBuf, err := chromedp.Run(tabCtx, chromedp.PrintToPDF(pdfOptions(opts)...))
	if err != nil {
		return renderOutput{}, fmt.Errorf("rendering HTML to PDF: %w", err)
	}
	if len(pdfBuf) == 0 {
		return renderOutput{}, fmt.Errorf("chromium produced an empty PDF")
	}
	return renderOutput{pdf: pdfBuf, blockedRequests: guard.BlockedRequests()}, nil
}

func openDocument(tabCtx context.Context, htmlFileURL string) (*requestGuard, error) {
	guard, err := newRequestGuard(htmlFileURL)
	if err != nil {
		return nil, err
	}
	guard.watch(tabCtx)
	err = chromedp.Do(tabCtx,
		interceptAllRequests(),
		disablePageScript(),
		chromedp.Navigate(htmlFileURL),
		chromedp.WaitReady(chromedp.CSS("body")),
		chromedp.Evaluate[chromedp.Void](fontsReadyScript, chromedp.EvalAwaitPromise),
	)
	if err != nil {
		return nil, err
	}
	if err := requireNoIsolatedFrames(tabCtx, guard); err != nil {
		return nil, err
	}
	return guard, nil
}

func requireNoIsolatedFrames(tabCtx context.Context, guard *requestGuard) error {
	res, err := chromedp.Call(tabCtx, target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		return fmt.Errorf("listing browser targets: %w", err)
	}
	tabID := chromedp.FromContext(tabCtx).Target.TargetID
	if frameOutsideGuard(res.TargetInfos, tabID, guard.refusedDocument) {
		return errIsolatedFrame
	}
	return nil
}

func frameOutsideGuard(infos []*target.Info, tabID target.ID, refused func(string) bool) bool {
	byID := make(map[target.ID]*target.Info, len(infos))
	for _, info := range infos {
		byID[info.TargetID] = info
	}
	for _, info := range infos {
		if info.Type != isolatedFrameTargetType || !descendsFrom(byID, info, tabID) {
			continue
		}
		if strings.HasPrefix(info.URL, chromiumErrorPagePrefix) || refused(withoutFragment(info.URL)) {
			continue
		}
		return true
	}
	return false
}

func descendsFrom(byID map[target.ID]*target.Info, info *target.Info, tabID target.ID) bool {
	for seen := 0; info != nil && seen < len(byID); seen++ {
		if info.ParentID == tabID {
			return true
		}
		info = byID[info.ParentID]
	}
	return false
}

var errIsolatedFrame = errors.New("the document opened a frame outside the request guard")

func disablePageScript() chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		_, err := cdp.Call(ctx, t, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true})
		return chromedp.Void{}, err
	}
}

const omittedMarginsInches = 0.0

func pdfOptions(opts printOptions) []chromedp.PDFOption {
	margins := omittedMarginsInches
	if opts.margins != nil {
		margins = *opts.margins
	}
	pdfOpts := []chromedp.PDFOption{
		chromedp.PDFPaper(chromedp.PaperSize{Width: opts.paperWidth, Height: opts.paperHeight}),
		chromedp.PDFMargin(margins),
		chromedp.PDFPreferCSSPageSize(),
		untaggedPDF,
	}
	if opts.printBackground {
		pdfOpts = append(pdfOpts, chromedp.PDFPrintBackground())
	}
	if opts.landscape {
		pdfOpts = append(pdfOpts, chromedp.PDFLandscape())
	}
	return pdfOpts
}

func untaggedPDF(p *page.PrintToPDFParams) {
	off := false
	p.GenerateTaggedPDF = &off
	p.GenerateDocumentOutline = &off
}
