// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var widgets = platformResource{path: "/widgets"}

const apiV1PlatformPrefix = "/api/v1/platform"

func v2Target(t *testing.T, servesV2 bool, routes map[string]string) (*Client, *atomic.Int32) {
	c, probes, _ := v2TargetCountingV1(t, servesV2, routes)
	return c, probes
}

func v2TargetCountingV1(t *testing.T, servesV2 bool, routes map[string]string) (c *Client, probes, v1Requests *atomic.Int32) {
	t.Helper()
	probes, v1Requests = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == APIV2ProbePath {
			probes.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, apiV1PlatformPrefix+"/") {
			v1Requests.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, platform.APIV2Prefix+"/") && !servesV2 {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == APIV2ProbePath {
			writeV2Page(w, []struct{}{})
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), probes, v1Requests
}

func statusTarget(t *testing.T, status int) (c *Client, probes, others *atomic.Int32) {
	t.Helper()
	probes, others = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case APIV2ProbePath:
			probes.Add(1)
		case versionEndpoint:
		default:
			others.Add(1)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), probes, others
}

func getWidget(ctx context.Context, c *Client) (widget, error) {
	var got widget
	err := c.callResource(ctx, widgets, http.MethodGet, "/w1", nil, &got)
	return got, err
}

func TestRequireV2_CallsATargetThatServesIt(t *testing.T) {
	c, probes := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets/w1": `{"data":{"id":"w1","name":"first"}}`,
	})

	for range 2 {
		got, err := getWidget(t.Context(), c)
		if err != nil {
			t.Fatalf("callResource: %v", err)
		}
		if got.ID != "w1" {
			t.Errorf("widget = %+v, want w1", got)
		}
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("probed the target %d times, want 1", n)
	}
}

func TestRequireV2_RefusesATargetWithoutV2(t *testing.T) {
	c, probes, v1Requests := v2TargetCountingV1(t, false, map[string]string{
		"/api/v1/platform/widgets/w1": `{"id":"w1","name":"first"}`,
	})

	for range 2 {
		_, err := getWidget(t.Context(), c)
		var refusal *NoV2Error
		if !errors.As(err, &refusal) {
			t.Fatalf("err = %v, want the refusal of a target without /api/v2", err)
		}
		for _, want := range []string{c.TargetURL(), "does not serve /api/v2", "upgrade Agent Mesh to 2.583.0 or later"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not mention %q", err, want)
			}
		}
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("probed the target %d times, want 1: a 404 is a definite answer", n)
	}
	if n := v1Requests.Load(); n != 0 {
		t.Errorf("the target received %d v1 requests, want 0: the CLI never falls back to v1", n)
	}
}

func TestRequireV2_RefusesARequestBuiltOutsideDo(t *testing.T) {
	c, _ := v2Target(t, false, nil)

	_, err := c.DownloadToolset(t.Context(), "t1")
	var refusal *NoV2Error
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want the refusal of a target without /api/v2", err)
	}
}

func TestRequireV2_SurfacesTheProbesSignInRefusal(t *testing.T) {
	c, probes, others := statusTarget(t, http.StatusUnauthorized)

	for range 2 {
		_, err := getWidget(t.Context(), c)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("err = %v, want the probe's 401, which carries the sign-in remedy", err)
		}
	}
	if n := probes.Load(); n != 2 {
		t.Errorf("probed the target %d times, want 2: a 401 is not an answer about /api/v2", n)
	}
	if n := others.Load(); n != 0 {
		t.Errorf("the target received %d other requests, want 0", n)
	}
}

func TestRequireV2_LetsACallerRefusedOnAgentsMakeItsOwnCall(t *testing.T) {
	c, probes, others := statusTarget(t, http.StatusForbidden)

	for range 3 {
		_, err := getWidget(t.Context(), c)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden || httpErr.Path != platform.APIV2Prefix+"/widgets/w1" {
			t.Fatalf("err = %v, want the widget request's own 403", err)
		}
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("probed the target %d times, want 1: a 403 is a definite answer", n)
	}
	if n := others.Load(); n != 3 {
		t.Errorf("the target received %d widget requests, want 3", n)
	}
}

func TestRequireV2_FailsTheCallWhenTheProbeFails(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, probes, others := statusTarget(t, status)

			for range 2 {
				_, err := getWidget(t.Context(), c)
				var refusal *NoV2Error
				if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "check that the platform serves /api/v2") {
					t.Fatalf("err = %v, want this call to fail on the probe without refusing the target", err)
				}
			}
			if n := probes.Load(); n != 2 {
				t.Errorf("probed the target %d times, want 2: a transient failure leaves the next call to probe again", n)
			}
			if n := others.Load(); n != 0 {
				t.Errorf("the target received %d other requests, want 0", n)
			}
		})
	}
}

func TestRequireV2_ProbesAgainAfterATransientFailure(t *testing.T) {
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == APIV2ProbePath && probes.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == APIV2ProbePath {
			writeV2Page(w, []struct{}{})
			return
		}
		writeV2Single(w, http.StatusOK, widget{ID: "w1"})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(target.Resolved{URL: srv.URL})

	if _, err := getWidget(t.Context(), c); err == nil {
		t.Fatal("the call during the 503 should fail")
	}
	for range 2 {
		if _, err := getWidget(t.Context(), c); err != nil {
			t.Fatalf("call after the 503: %v", err)
		}
	}
	if n := probes.Load(); n != 2 {
		t.Errorf("probed the target %d times, want 2: once for the 503, once more for the answer it then keeps", n)
	}
}

func TestRequireV2_FailsTheCallWhenTheTargetIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := NewClient(target.Resolved{URL: srv.URL})

	_, err := getWidget(t.Context(), c)
	var refusal *NoV2Error
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a connection failure, not the refusal of a target without /api/v2", err)
	}
}

func TestRequireV2_ProbesAgainAfterACancelledCaller(t *testing.T) {
	c, probes := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets/w1": `{"data":{"id":"w1","name":"first"}}`,
	})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := getWidget(cancelled, c); !errors.Is(err, context.Canceled) {
		t.Errorf("err for the cancelled caller = %v, want context.Canceled", err)
	}
	if _, err := getWidget(t.Context(), c); err != nil {
		t.Errorf("call after the cancelled caller: %v; one cancelled caller must not fail the next", err)
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("the target saw %d probes, want 1: the cancelled caller never reached it", n)
	}
}

func TestRequireV2_ParallelCallersShareOneProbeOfAHungTarget(t *testing.T) {
	var probes atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == versionEndpoint {
			http.NotFound(w, r)
			return
		}
		probes.Add(1)
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	c := NewClient(target.Resolved{URL: srv.URL})

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := getWidget(ctx, c)
			errs <- err
		}()
	}
	for probes.Load() == 0 {
		runtime.Gosched()
	}
	cancel()

	for range 8 {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("probed the hung target %d times, want 1: parallel callers wait on one probe", n)
	}
}

func TestRequireV2_ProbesTheTargetOnceUnderParallelCallers(t *testing.T) {
	c, probes := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets/w1": `{"data":{"id":"w1","name":"first"}}`,
	})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = getWidget(t.Context(), c) })
	}
	wg.Wait()

	if n := probes.Load(); n != 1 {
		t.Errorf("probed the target %d times, want 1", n)
	}
}

type widget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestDoSingle_UnwrapsAV2SingleResource(t *testing.T) {
	c, _ := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets/w1": `{"data":{"id":"w1","name":"first"}}`,
	})

	var got widget
	if err := c.doSingle(t.Context(), http.MethodGet, platform.APIV2Prefix+"/widgets/w1", nil, &got); err != nil {
		t.Fatalf("doSingle: %v", err)
	}
	if got != (widget{ID: "w1", Name: "first"}) {
		t.Errorf("widget = %+v, want the resource inside data", got)
	}
}

func TestDo_LeavesAV2SingleEnvelopeForACallerThatDecodesIt(t *testing.T) {
	budgets := platformResource{path: "/budgets"}
	c, _ := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/budgets/b1": `{"data":{"id":"b1","name":"team"}}`,
	})
	skew := NewSchemaSkewSink()
	c.SetSchemaSkewSink(skew)

	var got entplatform.DataResponse[widget]
	if err := c.do(t.Context(), http.MethodGet, budgets.apiPath()+"/b1", nil, &got); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got.Data.ID != "b1" || got.Data.Name != "team" {
		t.Errorf("budget = %+v, want the resource inside data", got.Data)
	}
	if findings := skew.Findings(); len(findings) != 0 {
		t.Errorf("skew = %+v, want none: the caller decodes the envelope v2 sends", findings)
	}
}

func TestListAllPages_ReadsAV2ListEnvelope(t *testing.T) {
	c, _ := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets": `{"data":[{"id":"w1","name":"first"},{"id":"w2","name":"second"}],` +
			`"meta":{"pagination":{"pageNumber":1,"pageSize":100,"totalCount":2,"nextPage":null}}}`,
	})

	got, err := listAllPages[widget](t.Context(), c, widgets.apiPath())
	if err != nil {
		t.Fatalf("listAllPages: %v", err)
	}
	if len(got) != 2 || got[1].Name != "second" {
		t.Errorf("widgets = %+v, want both list items", got)
	}
}

func TestDoSingle_RejectsAV2SingleWithoutData(t *testing.T) {
	for name, body := range map[string]string{
		"no data member": `{"id":"w1","name":"first"}`,
		"null data":      `{"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := v2Target(t, true, map[string]string{
				platform.APIV2Prefix + "/widgets/w1": body,
			})

			var got widget
			err := c.doSingle(t.Context(), http.MethodGet, platform.APIV2Prefix+"/widgets/w1", nil, &got)
			if err == nil {
				t.Fatal("a v2 single without its data must fail to decode, not decode as empty")
			}
		})
	}
}

func TestDoSingle_ReportsSkewInsideTheV2Resource(t *testing.T) {
	c, _ := v2Target(t, true, map[string]string{
		platform.APIV2Prefix + "/widgets/w1": `{"data":{"id":"w1","name":"first","colour":"blue"}}`,
	})
	skew := NewSchemaSkewSink()
	c.SetSchemaSkewSink(skew)

	var got widget
	if err := c.doSingle(t.Context(), http.MethodGet, platform.APIV2Prefix+"/widgets/w1", nil, &got); err != nil {
		t.Fatalf("doSingle: %v", err)
	}
	findings := skew.Findings()
	if len(findings) != 1 || findings[0].Key != "colour" {
		t.Errorf("skew = %+v, want only the unknown field inside data", findings)
	}
}

func releaseTarget(t *testing.T, release string) (c *Client, others *atomic.Int32) {
	t.Helper()
	others = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case versionEndpoint:
			_, _ = fmt.Fprintf(w, `{"products":[{"id":%q,"version":%q}]}`, gatewayProductID, release)
		case APIV2ProbePath:
			others.Add(1)
			writeV2Page(w, []struct{}{})
		default:
			others.Add(1)
			writeV2Single(w, http.StatusOK, widget{ID: "w1"})
		}
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), others
}

func TestRequireV2_RefusesAReleaseBeforeTheOneItNeeds(t *testing.T) {
	for _, release := range []string{"main-v2.525.3", "main-v2.530.3", "main-v2.540.2", "main-v2.542.0", "main-v2.563.3", "main-v2.563.9-4-gabc1234", "main-v2.564.2", "main-v2.565.0", "main-v2.565.1", "main-v2.566.0", "main-v2.567.0", "main-v2.568.3", "main-v2.569.0", "main-v2.570.0", "main-v2.571.6", "main-v2.572.0", "main-v2.573.0", "main-v2.574.0", "main-v2.574.1", "main-v2.575.0", "main-v2.576.0", "main-v2.577.0", "main-v2.578.4", "main-v2.579.0", "main-v2.579.3", "main-v2.580.0", "main-v2.580.1", "main-v2.581.0", "main-v2.581.2", "main-v2.582.0", "main-v2.582.2"} {
		t.Run(release, func(t *testing.T) {
			c, others := releaseTarget(t, release)

			for range 2 {
				_, err := getWidget(t.Context(), c)
				var refusal *NoV2Error
				if !errors.As(err, &refusal) {
					t.Fatalf("err = %v, want the refusal of a release before %s", err, minV2PlatformRelease)
				}
				for _, want := range []string{c.TargetURL(), releaseNumber(release), "upgrade Agent Mesh to 2.583.0 or later"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q does not mention %q", err, want)
					}
				}
			}
			if n := others.Load(); n != 0 {
				t.Errorf("the target received %d requests besides its version, want 0", n)
			}
		})
	}
}

func TestRequireV2_CallsAReleaseFromTheOneItNeeds(t *testing.T) {
	for _, release := range []string{"main-v2.583.0", "main-v2.583.1", "main-v2.584.0", "dev"} {
		t.Run(release, func(t *testing.T) {
			c, _ := releaseTarget(t, release)

			if _, err := getWidget(t.Context(), c); err != nil {
				t.Fatalf("call to %s: %v", release, err)
			}
		})
	}
}

func keyGatedPlatform(t *testing.T) *httptest.Server {
	t.Helper()
	ok := func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []struct{}{})
	}
	mux := http.NewServeMux()
	allowed := map[string]bool{}
	for _, pattern := range platform.APIKeyRoutePatterns() {
		mux.HandleFunc(pattern, ok)
		allowed[pattern] = true
	}
	mux.HandleFunc("GET "+APIV2ProbePath, ok)

	authn := entplatform.NewAuthMiddleware(nil, nil, ciKeyAuthority{}, slog.New(slog.DiscardHandler))
	srv := httptest.NewServer(authn(auth.RestrictAPIKeys(mux, mux, allowed)(mux)))
	t.Cleanup(srv.Close)
	return srv
}

const ciKey = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type ciKeyAuthority struct{}

func (ciKeyAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{KeyID: "key-1", OwnerID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77", OwnerSub: "system:ci-deployer", Scopes: []string{"*"}}, nil
}

func TestRequireV2_RefusesATargetThatKeepsAPIKeysOffV2(t *testing.T) {
	srv := keyGatedPlatform(t)
	c := NewClient(target.Resolved{URL: srv.URL, Token: ciKey})

	_, err := getWidget(t.Context(), c)
	var refusal *NoV2Error
	if !errors.As(err, &refusal) || !refusal.RefusesAPIKeys {
		t.Fatalf("err = %v, want the refusal of a platform that keeps API keys off /api/v2", err)
	}
	for _, want := range []string{"refuses API keys on /api/v2", "upgrade Agent Mesh to 2.583.0 or later"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
}

func TestNoV2Error_NamesOnlyTheTargetsOrigin(t *testing.T) {
	for _, refusal := range []*NoV2Error{
		{URL: "https://ci:hunter2@platform.example.com:8443/base?token=s3cret"},
		{URL: "https://ci:hunter2@platform.example.com:8443/base?token=s3cret", Version: "2.530.3"},
		{URL: "https://ci:hunter2@platform.example.com:8443/base?token=s3cret", RefusesAPIKeys: true},
	} {
		msg := refusal.Error()
		if !strings.Contains(msg, "https://platform.example.com:8443 ") {
			t.Errorf("refusal %q does not name the target's origin", msg)
		}
		for _, secret := range []string{"hunter2", "s3cret", "/base"} {
			if strings.Contains(msg, secret) {
				t.Errorf("refusal %q prints %q from the configured URL", msg, secret)
			}
		}
	}
}

func releaseNumber(release string) string {
	number, _, _ := strings.Cut(strings.TrimPrefix(release, "main-v"), "-")
	return number
}
