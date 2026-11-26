// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

type v2ScheduledArtifacts struct {
	t   *testing.T
	fx  schedArtFixture
	mux *http.ServeMux
}

func newV2ScheduledArtifacts(t *testing.T, namespace string) *v2ScheduledArtifacts {
	t.Helper()
	h, _, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc, schedArtTestOwner, namespace,
		schedArtTestFilename, []byte(schedArtTestPayload))
	mux := http.NewServeMux()
	apicontract.Mount(mux, chatv2.Prefix, h.chatV2Routes(), chatV2Guard, nil)
	return &v2ScheduledArtifacts{t: t, fx: fx, mux: mux}
}

func (f *v2ScheduledArtifacts) get(path, caller string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+path, nil), caller)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *v2ScheduledArtifacts) artifactPath(filename string) string {
	return "/artifacts/scheduled/" + f.fx.sessionID + "/" + filename
}

func TestChatV2ScheduledArtifact_CreatorGetsTheBytes(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	rec := f.get(f.artifactPath(schedArtTestFilename), schedArtTestOwner)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if body, _ := io.ReadAll(rec.Body); string(body) != schedArtTestPayload {
		t.Errorf("body = %q, want %q", body, schedArtTestPayload)
	}
	if got := rec.Header().Get("Content-Type"); got != schedArtTestMIMEType {
		t.Errorf("Content-Type = %q, want %q", got, schedArtTestMIMEType)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "inline;") {
		t.Errorf("Content-Disposition = %q, want inline by default", disp)
	}
}

func TestChatV2ScheduledArtifact_OtherUserGets404(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	rec := f.get(f.artifactPath(schedArtTestFilename), "stranger@example.com")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for another user; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChatV2ScheduledArtifact_OtherNamespaceGets404(t *testing.T) {
	f := newV2ScheduledArtifacts(t, "other-ns")

	rec := f.get(f.artifactPath(schedArtTestFilename), schedArtTestOwner)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for another namespace; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChatV2ScheduledArtifact_PathTraversalRefused(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	for _, filename := range []string{"%2E%2E%2Fpasswd", "%2E%2E%5Cwin", "%2E"} {
		t.Run(filename, func(t *testing.T) {
			rec := f.get(f.artifactPath(filename), schedArtTestOwner)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 for a traversal filename; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestChatV2ScheduledArtifact_DownloadTrueIsAttachment(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	rec := f.get(f.artifactPath(schedArtTestFilename)+"?download=true", schedArtTestOwner)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment", disp)
	}
	if !strings.Contains(disp, "filename*=UTF-8''"+url.PathEscape(schedArtTestFilename)) {
		t.Errorf("Content-Disposition = %q, want the UTF-8 filename", disp)
	}
}

func TestChatV2ScheduledArtifact_NonUUIDSessionIs400(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	rec := f.get("/artifacts/scheduled/not-a-uuid/"+schedArtTestFilename, schedArtTestOwner)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-UUID session id; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChatV2ScheduledArtifact_DownloadThatIsNotABooleanIs400(t *testing.T) {
	f := newV2ScheduledArtifacts(t, schedArtTestNamespace)

	rec := f.get(f.artifactPath(schedArtTestFilename)+"?download=maybe", schedArtTestOwner)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for download=maybe; body=%s", rec.Code, rec.Body.String())
	}
}
