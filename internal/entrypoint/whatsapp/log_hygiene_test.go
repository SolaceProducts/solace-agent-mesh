// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestFetchInboundMedia_FailureLogOmitsResponseBody(t *testing.T) {
	const leak = "to: 15551234567"
	errBody := `{"error":{"code":131047,"message":"Re-engagement required","error_data":{"details":"` + leak + `"}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errBody))
	}))
	t.Cleanup(srv.Close)

	cfg := &WhatsAppConfig{
		AccessToken: "tok-test", PhoneNumberID: "555111",
		GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL,
		DisableRateLimiting: true,
	}
	logs := &recordCollector{}
	logger := slog.New(logs)
	a := &Adapter{
		core: newTestCore(t), cfg: cfg, logger: logger,
		claims: newMemoryClaims(), claimLease: defaultClaimLease,
		hilDedup: runtime.NewMapCache(),
		client:   NewClient(cfg, logger),
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	t.Cleanup(a.cancel)
	startTestSender(t, a)

	msg := &inboundMessage{From: "15551230000", ID: "wamid.medialeak", Type: "image", Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"}}
	if _, _, ok, _ := a.fetchInboundMedia(msg, msg.Image, "image"); ok {
		t.Fatal("expected the media fetch to fail on a Graph error envelope")
	}

	var sawCode bool
	for _, r := range logs.all() {
		r.Attrs(func(at slog.Attr) bool {
			if s := at.Value.String(); strings.Contains(s, leak) || strings.Contains(s, `"error":{`) {
				t.Errorf("log field %q carries the provider payload: %q", at.Key, s)
			}
			if at.Key == "metaErrorCode" && at.Value.String() == "131047" {
				sawCode = true
			}
			return true
		})
	}
	if !sawCode {
		t.Errorf("expected the failure log to carry metaErrorCode=131047; records = %+v", logs.all())
	}
}

func TestSafeGraphErrFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  *graphAPIError
		want []any
	}{
		{
			name: "permanent rejection carries the class, no subcode",
			err: &graphAPIError{Op: "send", HTTPStatus: 400, Attempts: 1,
				GraphErr: graphError{Code: 131047, Message: "Re-engagement required"},
				Body:     `{"error":{"error_data":{"details":"to: 15551234567"}}}`},
			want: []any{"httpStatus", 400, "metaErrorCode", 131047, "errorClass", "permanent"},
		},
		{
			name: "expired token is labelled credentials_rejected with its subcode",
			err: &graphAPIError{Op: "send", HTTPStatus: 401, Attempts: 1,
				GraphErr: graphError{Code: codeOAuthException, Subcode: subcodeTokenExpired, Message: "Error validating access token"}},
			want: []any{"httpStatus", 401, "metaErrorCode", 190, "errorClass", "credentials_rejected", "metaErrorSubcode", 463},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeGraphErrFields(tt.err); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("graphAPIError fields = %v, want %v", got, tt.want)
			}
		})
	}

	plain := errors.New("dial tcp: connection refused")
	got := safeGraphErrFields(plain)
	want := []any{"errorClass", string(classTransport), "error", plain}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("non-Graph error fields = %v, want %v", got, want)
	}
}

func TestWithoutRequestURL(t *testing.T) {
	const signed = "https://cdn.whatsapp.net/media/abc?access_token=SECRET-TOKEN&state=PKCE"
	cause := errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	wrapped := &url.Error{Op: "Get", URL: signed, Err: cause}

	if got := wrapped.Error(); !strings.Contains(got, "SECRET-TOKEN") {
		t.Fatalf("precondition: *url.Error should stringify the URL, got %q", got)
	}
	stripped := withoutRequestURL(wrapped)
	if s := stripped.Error(); strings.Contains(s, "SECRET-TOKEN") || strings.Contains(s, "access_token") {
		t.Errorf("unwrapped error still carries the signed URL: %q", s)
	}
	if !errors.Is(stripped, cause) {
		t.Errorf("unwrapped error = %v, want the underlying cause %v", stripped, cause)
	}
	if got := withoutRequestURL(cause); got != cause {
		t.Errorf("a non-url.Error must pass through unchanged, got %v", got)
	}
}

func TestMediaFetch_TransportFailureOmitsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close()

	cfg := &WhatsAppConfig{
		AccessToken: "tok-test", PhoneNumberID: "555111",
		GraphAPIVersion: "v26.0", GraphAPIBaseURL: dead,
		DisableRateLimiting: true,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewClient(cfg, logger)

	_, _, err := c.FetchMedia(context.Background(), "MEDIA1", 0)
	if err == nil {
		t.Fatal("expected the metadata fetch to fail against a closed listener")
	}
	if s := err.Error(); strings.Contains(s, dead) || strings.Contains(s, "access_token") {
		t.Errorf("transport error carries the request URL: %q", s)
	}
}

func TestSendFailureLogsOmitProviderPayload(t *testing.T) {
	const phone = "15551234567"
	const leak = phone

	for _, tt := range []struct {
		name    string
		wantMsg string
		drive   func(t *testing.T, a *Adapter)
	}{
		{
			name:    "interactive prompt",
			wantMsg: "whatsapp message delivery failed permanently",
			drive: func(t *testing.T, a *Adapter) {
				_ = a.core.Tasks().Register("t-hil", "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
				a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})
				a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
			},
		},
		{
			name:    "authorization prompt",
			wantMsg: "whatsapp message delivery failed permanently",
			drive: func(t *testing.T, a *Adapter) {
				registerAuthTask(a, "t-auth", phone)
				a.OnStatusUpdate(t.Context(), "t-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
			},
		},
		{
			name:    "typing indicator",
			wantMsg: "whatsapp typing indicator failed",
			drive: func(t *testing.T, a *Adapter) {
				a.typingTrack(phone, "t-typing", "wamid.typing")
				a.typingShow(phone)
				a.stopTyping()
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newWAMock(t)
			mock.rejectAll = true
			logs := &recordCollector{}
			a := newAdapterRig(t, mock, "Builder", slog.New(logs))

			tt.drive(t, a)
			waitQueueDrained(t, a)

			var found bool
			for _, r := range logs.all() {
				if r.Message != tt.wantMsg {
					continue
				}
				found = true
				var sawCode bool
				r.Attrs(func(at slog.Attr) bool {
					if s := at.Value.String(); strings.Contains(s, leak) || strings.Contains(s, "error_data") {
						t.Errorf("field %q carries the provider payload: %q", at.Key, s)
					}
					if at.Key == "metaErrorCode" {
						sawCode = true
					}
					return true
				})
				if !sawCode {
					t.Errorf("%q must carry metaErrorCode", tt.wantMsg)
				}
			}
			if !found {
				t.Errorf("expected a %q record; got %d records", tt.wantMsg, len(logs.all()))
			}
		})
	}
}

func TestSendMedia_FailureLogOmitsProviderPayload(t *testing.T) {
	logs := &recordCollector{}
	a, m := newOutboundArtifactRig(t, slog.New(logs))
	m.rejectSend = true

	if !deliverMediaBytes(t, a, taskT1, "15551230001", []byte("%PDF-1.4 x"), "application/pdf", "report.pdf") {
		t.Fatal("the artifact should have been queued; Meta rejects it at send time, not here")
	}

	var found bool
	for _, r := range logs.all() {
		if r.Message != "whatsapp message delivery failed permanently" {
			continue
		}
		found = true
		r.Attrs(func(at slog.Attr) bool {
			if s := at.Value.String(); strings.Contains(s, "15551234567") || strings.Contains(s, "error_data") {
				t.Errorf("field %q carries the provider payload: %q", at.Key, s)
			}
			return true
		})
	}
	if !found {
		t.Errorf("expected a send-failure record; got %d records", len(logs.all()))
	}
}

func TestDownloadMedia_TransportFailureOmitsURL(t *testing.T) {
	deadSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := deadSrv.URL + "/media/abc?access_token=SECRET-TOKEN"
	deadSrv.Close()

	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + deadURL + `","mime_type":"image/jpeg","file_size":10,"id":"MEDIA1"}`))
	}))
	t.Cleanup(meta.Close)

	cfg := &WhatsAppConfig{
		AccessToken: "tok-test", PhoneNumberID: "555111",
		GraphAPIVersion: "v26.0", GraphAPIBaseURL: meta.URL,
		DisableRateLimiting: true,
	}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, _, err := c.FetchMedia(context.Background(), "MEDIA1", 0)
	if err == nil {
		t.Fatal("expected the download to fail against a closed listener")
	}
	if s := err.Error(); strings.Contains(s, "SECRET-TOKEN") || strings.Contains(s, "access_token") {
		t.Errorf("download error carries the signed CDN URL: %q", s)
	}
}
