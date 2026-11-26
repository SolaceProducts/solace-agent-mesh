// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type spyIngestor struct {
	mu       sync.Mutex
	surfaces []string
	relayed  []productanalytics.ClientPlatform
}

func (s *spyIngestor) EnqueueRelayed(_ context.Context, surface string, host productanalytics.ClientPlatform, _ []productanalytics.RawEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.surfaces = append(s.surfaces, surface)
	s.relayed = append(s.relayed, host)
}

func stopIngestService(t *testing.T, svc *productanalytics.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func newSpyIngestHandler() (*productAnalyticsHandler, *spyIngestor) {
	spy := &spyIngestor{}
	return &productAnalyticsHandler{svc: spy, logger: slog.Default()}, spy
}

func postTo(h *productAnalyticsHandler, route func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/productAnalytics/ingest/cli", strings.NewReader(body))
	rec := httptest.NewRecorder()
	route(rec, req)
	return rec
}

const cliRunBody = `"events":[{"event":"cli:command_run","properties":{"command":"sam task send","outcome":"success"}}]`

func TestCLIIngestAcceptsTheReportingHostPlatform(t *testing.T) {
	h, spy := newSpyIngestHandler()
	rec := postTo(h, h.handleCLIIngest, `{"platform":{"os":"windows","arch":"386","appVersion":"main-v1.2.3"},`+cliRunBody+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(spy.relayed) != 1 {
		t.Fatalf("relayed batches = %d, want 1", len(spy.relayed))
	}
	want := productanalytics.ClientPlatform{OS: "windows", Arch: "386", AppVersion: "main-v1.2.3"}
	if spy.relayed[0] != want {
		t.Errorf("handler accepted %+v, want %+v", spy.relayed[0], want)
	}
}

func TestCLIIngestAcceptsNoPlatformOutsideTheVocabulary(t *testing.T) {
	bodies := []string{
		`{"platform":{"os":"Ubuntu 22.04","arch":"x86_64","appVersion":"built by alice@example.com"},` + cliRunBody + `}`,
		`{"platform":{"os":"linux ","arch":"arm64\n","appVersion":"/Users/alice/src/sam"},` + cliRunBody + `}`,
		`{"platform":{"os":"` + strings.Repeat("a", 2048) + `","arch":"` + strings.Repeat("b", 2048) +
			`","appVersion":"` + strings.Repeat("c", 2048) + `"},` + cliRunBody + `}`,
	}
	for _, body := range bodies {
		h, spy := newSpyIngestHandler()
		if rec := postTo(h, h.handleCLIIngest, body); rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if len(spy.relayed) != 1 {
			t.Fatalf("relayed batches = %d, want 1", len(spy.relayed))
		}
		if got := spy.relayed[0]; got != (productanalytics.ClientPlatform{}) {
			t.Errorf("handler accepted %+v from a forged platform, want every field emptied", got)
		}
	}
}

func TestCLIIngestStillNamesItsOwnSurface(t *testing.T) {
	h, spy := newSpyIngestHandler()
	body := `{"platform":{"os":"linux","arch":"amd64","appVersion":"dev"},"events":[{"event":"page_view","properties":{"interface":"ui"}}]}`
	if rec := postTo(h, h.handleCLIIngest, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(spy.surfaces) != 1 || spy.surfaces[0] != productanalytics.SurfaceCLI {
		t.Errorf("surfaces = %v, want [cli]", spy.surfaces)
	}
}

const pageViewBody = `"events":[{"event":"page_view","properties":{"path":"/agents"}}]`

func postBrowserIngest(h *productAnalyticsHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/productAnalytics/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleIngest(rec, req)
	return rec
}

func TestBrowserIngestAcceptsThePageHostPlatform(t *testing.T) {
	h, spy := newSpyIngestHandler()
	rec := postBrowserIngest(h, `{"platform":{"os":"windows","arch":"amd64"},`+pageViewBody+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(spy.relayed) != 1 {
		t.Fatalf("relayed batches = %d, want 1", len(spy.relayed))
	}
	want := productanalytics.ClientPlatform{OS: "windows", Arch: "amd64", AppVersion: samversion.Version}
	if spy.relayed[0] != want {
		t.Errorf("handler accepted %+v, want %+v", spy.relayed[0], want)
	}
}

func TestBrowserIngestRefusesAnAppVersion(t *testing.T) {
	h, spy := newSpyIngestHandler()
	rec := postBrowserIngest(h, `{"platform":{"os":"linux","arch":"amd64","appVersion":"bundle-v9"},`+pageViewBody+`}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a browser-supplied appVersion", rec.Code)
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if _, named := resp.ValidationDetails["appVersion"]; !named {
		t.Errorf("validationDetails = %v, want it to name appVersion", resp.ValidationDetails)
	}
	if len(spy.relayed) != 0 {
		t.Errorf("handler forwarded a refused batch: relayed = %d", len(spy.relayed))
	}
}

func TestBrowserIngestAcceptsNoPlatformOutsideTheVocabulary(t *testing.T) {
	bodies := []string{
		`{"platform":{"os":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)","arch":"x86_64"},` + pageViewBody + `}`,
		`{"platform":{"os":"macOS","arch":"x86"},` + pageViewBody + `}`,
		`{"platform":{"os":"linux ","arch":"arm64\n"},` + pageViewBody + `}`,
		`{"platform":{"os":"` + strings.Repeat("a", 2048) + `","arch":"` + strings.Repeat("b", 2048) + `"},` + pageViewBody + `}`,
	}
	for _, body := range bodies {
		h, spy := newSpyIngestHandler()
		if rec := postBrowserIngest(h, body); rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if len(spy.relayed) != 1 {
			t.Fatalf("relayed batches = %d, want 1", len(spy.relayed))
		}
		if got := spy.relayed[0]; got.OS != "" || got.Arch != "" {
			t.Errorf("handler accepted %+v from a forged platform, want os and arch emptied", got)
		}
	}
}

func TestBrowserIngestDoesNotLendItsOwnPlatform(t *testing.T) {
	for name, body := range map[string]string{
		"no platform field": `{` + pageViewBody + `}`,
		"empty platform":    `{"platform":{},` + pageViewBody + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, spy := newSpyIngestHandler()
			if rec := postBrowserIngest(h, body); rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
			}
			if len(spy.relayed) != 1 {
				t.Fatalf("relayed batches = %d, want 1", len(spy.relayed))
			}
			want := productanalytics.ClientPlatform{AppVersion: samversion.Version}
			if spy.relayed[0] != want {
				t.Errorf("handler accepted %+v, want %+v", spy.relayed[0], want)
			}
		})
	}
}

func TestBrowserIngestStillNamesItsOwnSurface(t *testing.T) {
	h, spy := newSpyIngestHandler()
	body := `{"platform":{"os":"darwin","arch":"arm64"},"events":[{"event":"cli:command_run","properties":{"interface":"cli"}}]}`
	if rec := postBrowserIngest(h, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(spy.surfaces) != 1 || spy.surfaces[0] != productanalytics.SurfaceUI {
		t.Errorf("surfaces = %v, want [ui]", spy.surfaces)
	}
}

func TestCLIIngestDeliversTheReportingHostPlatform(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := `{"platform":{"os":"windows","arch":"386","appVersion":"main-v1.2.3"},` + cliRunBody + `}`
	if rec := postTo(h, h.handleCLIIngest, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	stopIngestService(t, svc)

	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	props := sink.events[0].Properties
	for key, want := range map[string]string{"os": "windows", "arch": "386", "appVersion": "main-v1.2.3"} {
		if got := props[key]; got != want {
			t.Errorf("delivered %s = %v, want %q", key, got, want)
		}
	}
	if props["interface"] != productanalytics.SurfaceCLI {
		t.Errorf("delivered interface = %v, want cli", props["interface"])
	}
}

func TestCLIIngestDeliversNoForgedPlatform(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := `{"platform":{"os":"Ubuntu 22.04","arch":"x86_64","appVersion":"alice@example.com"},` + cliRunBody + `}`
	if rec := postTo(h, h.handleCLIIngest, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	stopIngestService(t, svc)

	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	props := sink.events[0].Properties
	for _, key := range []string{"os", "arch", "appVersion"} {
		if got, present := props[key]; present {
			t.Errorf("delivered %s = %v, want the field absent", key, got)
		}
	}
}

func TestBrowserIngestDeliversThePageHostPlatform(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec := postBrowserIngest(h, `{"platform":{"os":"windows","arch":"amd64"},`+pageViewBody+`}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	stopIngestService(t, svc)

	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	props := sink.events[0].Properties
	for key, want := range map[string]string{"os": "windows", "arch": "amd64", "appVersion": samversion.Version} {
		if got := props[key]; got != want {
			t.Errorf("delivered %s = %v, want %q", key, got, want)
		}
	}
	if props["interface"] != productanalytics.SurfaceUI {
		t.Errorf("delivered interface = %v, want ui", props["interface"])
	}
}

func TestBrowserIngestDeliversNoBorrowedPlatform(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec := postBrowserIngest(h, `{`+pageViewBody+`}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	stopIngestService(t, svc)

	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	props := sink.events[0].Properties
	for _, key := range []string{"os", "arch"} {
		if got, present := props[key]; present {
			t.Errorf("delivered %s = %v, want the field absent rather than this process's", key, got)
		}
	}
	if got := props["appVersion"]; got != samversion.Version {
		t.Errorf("delivered appVersion = %v, want %q", got, samversion.Version)
	}
}
