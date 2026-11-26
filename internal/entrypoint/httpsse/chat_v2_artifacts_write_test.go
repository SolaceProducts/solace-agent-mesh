// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func (f *v2Artifacts) send(method, path, caller, contentType string, body *bytes.Buffer) *httptest.ResponseRecorder {
	f.t.Helper()
	req := withIdentity(httptest.NewRequest(method, chatv2.Prefix+path, body), caller)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *v2Artifacts) upload(caller, sessionID, filename, fileName, content string) *httptest.ResponseRecorder {
	f.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		f.t.Fatal(err)
	}
	_, _ = part.Write([]byte(content))
	if sessionID != "" {
		_ = w.WriteField("sessionId", sessionID)
	}
	if filename != "" {
		_ = w.WriteField("filename", filename)
	}
	_ = w.Close()
	return f.send(http.MethodPost, "/artifacts", caller, w.FormDataContentType(), &buf)
}

func (f *v2Artifacts) newSession(owner string) string {
	f.t.Helper()
	id := idgen.New()
	if err := f.sess.Create(context.Background(), &store.Session{ID: id, UserID: owner}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *v2Artifacts) copyInto(caller, dest, source string, filenames ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	quoted := make([]string, len(filenames))
	for i, name := range filenames {
		quoted[i] = `"` + name + `"`
	}
	body := `{"sourceSessionId":"` + source + `","filenames":[` + strings.Join(quoted, ",") + `]}`
	return f.send(http.MethodPut, "/sessions/"+dest+"/artifacts/copy", caller, "application/json", bytes.NewBufferString(body))
}

func (f *v2Artifacts) downloadText(caller, session, filename string) (int, string) {
	f.t.Helper()
	rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/"+filename, caller)
	return rec.Code, rec.Body.String()
}

func TestChatV2ArtifactUpload_OwnerCanDownloadTheStoredFile(t *testing.T) {
	f := newV2Artifacts(t)
	session := f.newSession(v2Alice)

	rec := f.upload(v2Alice, session, "", "report.txt", "q3 numbers")

	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s, want 201", rec.Code, rec.Body)
	}
	got := decodeData[chatv2.ChatArtifact](t, rec)
	if got.ID != "report.txt" || got.Type != "artifact" || got.Size != int64(len("q3 numbers")) {
		t.Errorf("artifact = %+v, want report.txt of the content's size", got)
	}
	if got.SessionID == nil || *got.SessionID != session {
		t.Errorf("sessionId = %v, want %s", got.SessionID, session)
	}
	if got.URI == nil || !strings.HasPrefix(*got.URI, "artifact://") || strings.Contains(*got.URI, "?version") {
		t.Errorf("uri = %v, want a path-only artifact:// uri", got.URI)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "__user_uploaded" {
		t.Errorf("tags = %v, want [__user_uploaded]", got.Tags)
	}
	if code, body := f.downloadText(v2Alice, session, "report.txt"); code != http.StatusOK || body != "q3 numbers" {
		t.Errorf("download after upload = %d %q, want 200 with the bytes", code, body)
	}
}

func TestChatV2ArtifactUpload_SecondUploadToTheSameSessionMakesVersionOne(t *testing.T) {
	f := newV2Artifacts(t)
	session := f.newSession(v2Alice)

	first := decodeData[chatv2.ChatArtifact](t, f.upload(v2Alice, session, "", "notes.txt", "one"))
	second := decodeData[chatv2.ChatArtifact](t, f.upload(v2Alice, session, "", "notes.txt", "two"))

	if first.Version == nil || *first.Version != 0 {
		t.Errorf("first version = %v, want 0", first.Version)
	}
	if second.Version == nil || *second.Version != 1 {
		t.Errorf("second version = %v, want 1", second.Version)
	}
	if second.VersionCount == nil || *second.VersionCount != 2 {
		t.Errorf("second versionCount = %v, want 2", second.VersionCount)
	}
}

func TestChatV2ArtifactUpload_WithoutASessionStartsOneAndReturnsItsID(t *testing.T) {
	f := newV2Artifacts(t)

	got := decodeData[chatv2.ChatArtifact](t, f.upload(v2Alice, "", "", "fresh.txt", "hello"))

	if got.SessionID == nil || validatePathUUID(*got.SessionID) != nil {
		t.Fatalf("sessionId = %v, want a UUID for the new session", got.SessionID)
	}
	if code, body := f.downloadText(v2Alice, *got.SessionID, "fresh.txt"); code != http.StatusOK || body != "hello" {
		t.Errorf("download from the new session = %d %q, want 200 hello", code, body)
	}
}

func TestChatV2ArtifactUpload_ReservedFilenameIs422(t *testing.T) {
	f := newV2Artifacts(t)

	rec := f.upload(v2Alice, "", "", "report.metadata.json", "{}")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reserved filename = %d %s, want 422", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "filename") {
		t.Errorf("body = %s, want the filename field named", rec.Body)
	}
}

func TestChatV2ArtifactUpload_UnsafeFilenameIs422(t *testing.T) {
	f := newV2Artifacts(t)

	if rec := f.upload(v2Alice, "", "../escape.txt", "x.txt", "x"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("path traversal filename = %d %s, want 422", rec.Code, rec.Body)
	}
}

func TestChatV2ArtifactUpload_MalformedSessionIDIs422(t *testing.T) {
	f := newV2Artifacts(t)

	rec := f.upload(v2Alice, "not-a-uuid", "", "x.txt", "x")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed sessionId = %d %s, want 422", rec.Code, rec.Body)
	}
}

func TestChatV2ArtifactDelete_OwnerDeletesTheFile(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "gone.txt", "bye")

	rec := f.call(http.MethodDelete, "/sessions/"+session+"/artifacts/gone.txt", v2Alice)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete by owner = %d %s, want 204", rec.Code, rec.Body)
	}
	if code, _ := f.downloadText(v2Alice, session, "gone.txt"); code != http.StatusNotFound {
		t.Errorf("download after delete = %d, want 404", code)
	}
}

func TestChatV2ArtifactDelete_AnotherUserGets404AndTheFileStays(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "kept.txt", "alice's")

	rec := f.call(http.MethodDelete, "/sessions/"+session+"/artifacts/kept.txt", v2Bob)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete by another user = %d %s, want 404", rec.Code, rec.Body)
	}
	if code, body := f.downloadText(v2Alice, session, "kept.txt"); code != http.StatusOK || body != "alice's" {
		t.Errorf("file after another user's delete = %d %q, want 200 alice's", code, body)
	}
}

func TestChatV2ArtifactCopy_MovesTheFilesIntoTheTargetSession(t *testing.T) {
	f := newV2Artifacts(t)
	source, _ := f.store(v2Alice, "report.pdf", "pdf bytes")
	dest := f.newSession(v2Alice)

	rec := f.copyInto(v2Alice, dest, source, "report.pdf")

	if rec.Code != http.StatusOK {
		t.Fatalf("copy = %d %s, want 200", rec.Code, rec.Body)
	}
	result := decodeData[chatv2.ArtifactCopyResult](t, rec)
	if len(result.Copied) != 1 || result.Copied[0] != "report.pdf" || result.Failed == nil || len(result.Failed) != 0 {
		t.Errorf("result = %+v, want report.pdf copied and no failures", result)
	}
	if code, body := f.downloadText(v2Alice, dest, "report.pdf"); code != http.StatusOK || body != "pdf bytes" {
		t.Errorf("file in the target = %d %q, want 200 the copied bytes", code, body)
	}
}

func TestChatV2ArtifactCopy_FromAnotherUsersSessionIs404(t *testing.T) {
	f := newV2Artifacts(t)
	source, _ := f.store(v2Alice, "secret.txt", "alice only")
	dest := f.newSession(v2Bob)

	rec := f.copyInto(v2Bob, dest, source, "secret.txt")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("copy from another user's session = %d %s, want 404", rec.Code, rec.Body)
	}
	if code, _ := f.downloadText(v2Bob, dest, "secret.txt"); code != http.StatusNotFound {
		t.Errorf("copied file in bob's session = %d, want 404", code)
	}
}

func TestChatV2ArtifactCopy_UserScopedFileCountsAsCopied(t *testing.T) {
	f := newV2Artifacts(t)
	source := f.newSession(v2Alice)
	dest := f.newSession(v2Alice)

	rec := f.copyInto(v2Alice, dest, source, "user:prefs.json")

	if rec.Code != http.StatusOK {
		t.Fatalf("copy = %d %s, want 200", rec.Code, rec.Body)
	}
	result := decodeData[chatv2.ArtifactCopyResult](t, rec)
	if len(result.Copied) != 1 || result.Copied[0] != "user:prefs.json" || len(result.Failed) != 0 {
		t.Errorf("result = %+v, want user:prefs.json copied", result)
	}
}

func TestChatV2ArtifactDelete_ProjectIDIsNotAcceptedAndDeletesNothing(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "keep.txt", "x")

	rec := f.call(http.MethodDelete, "/sessions/"+session+"/artifacts/keep.txt?projectId="+idgen.New(), v2Alice)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete with projectId = %d %s, want 400", rec.Code, rec.Body)
	}
	if code, _ := f.downloadText(v2Alice, session, "keep.txt"); code != http.StatusOK {
		t.Errorf("file after refused delete = %d, want 200", code)
	}
}

func TestChatV2ArtifactUpload_ResponseMatchesTheListEntryOfTheSameFile(t *testing.T) {
	f := newV2ArtifactLists(t)

	uploaded := decodeData[chatv2.ChatArtifact](t, f.upload(v2Alice, "", "", "rows.csv", "a,b\n1,2\n"))

	listed := decodeArtifactList[chatv2.ChatArtifact](t, f.listed("/sessions/"+*uploaded.SessionID+"/artifacts", v2Alice)).Data
	if len(listed) != 1 {
		t.Fatalf("listed %d files, want 1", len(listed))
	}
	listed[0].SessionID = uploaded.SessionID
	listed[0].UpdatedTime = uploaded.UpdatedTime
	if diff := cmp.Diff(listed[0], uploaded); diff != "" {
		t.Errorf("upload response differs from the list entry (-list +upload):\n%s", diff)
	}
}

func TestChatV2ArtifactCopy_RequestFieldProblems(t *testing.T) {
	f := newV2Artifacts(t)
	source := f.newSession(v2Alice)
	dest := f.newSession(v2Alice)
	path := "/sessions/" + dest + "/artifacts/copy"
	tests := map[string]struct {
		body string
		want int
	}{
		"missing source":    {`{"filenames":["a.txt"]}`, http.StatusUnprocessableEntity},
		"blank source":      {`{"sourceSessionId":"","filenames":["a.txt"]}`, http.StatusUnprocessableEntity},
		"non uuid source":   {`{"sourceSessionId":"nope","filenames":["a.txt"]}`, http.StatusUnprocessableEntity},
		"missing filenames": {`{"sourceSessionId":"` + source + `"}`, http.StatusUnprocessableEntity},
		"empty filenames":   {`{"sourceSessionId":"` + source + `","filenames":[]}`, http.StatusUnprocessableEntity},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := f.send(http.MethodPut, path, v2Alice, "application/json", bytes.NewBufferString(tt.body))
			if rec.Code != tt.want {
				t.Errorf("status = %d %s, want %d", rec.Code, rec.Body, tt.want)
			}
		})
	}
}

func TestChatV2ArtifactReads_SecondUserGets404OnVersionsAndNumberedVersion(t *testing.T) {
	f := newV2Artifacts(t)
	session, version := f.store(v2Alice, "private.txt", "alice only")
	base := "/sessions/" + session + "/artifacts/private.txt/versions"

	for name, path := range map[string]string{
		"versions list":    base,
		"numbered version": base + "/" + fmt.Sprint(version),
	} {
		rec := f.call(http.MethodGet, path, v2Bob)
		emptyList := name == "versions list" && rec.Code == http.StatusOK && len(decodeArtifactList[chatv2.ArtifactVersion](t, rec).Data) == 0
		if rec.Code != http.StatusNotFound && !emptyList {
			t.Errorf("%s as another user = %d %s, want 404 or an empty list", name, rec.Code, rec.Body)
		}
	}
}

func TestChatV2ArtifactCopy_IntoAnotherUsersSessionNeverReachesTheirFiles(t *testing.T) {
	f := newV2Artifacts(t)
	source := f.newSession(v2Alice)
	loc := f.svc.Resolve("", v2Alice, source, "mine.txt")
	if _, err := f.svc.Inner.Save(context.Background(), loc, []byte("mine"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	bobs := f.newSession(v2Bob)

	f.copyInto(v2Alice, bobs, source, "mine.txt")

	if code, _ := f.downloadText(v2Bob, bobs, "mine.txt"); code == http.StatusOK {
		t.Errorf("Bob reads a file Alice copied into his session id, want it absent")
	}
}

func TestChatV2ArtifactUpload_FileOverTheContentLimitIs413(t *testing.T) {
	f := newV2Artifacts(t)
	f.h.core.Config().GatewayArtifactContentLimitBytes = 4

	rec := f.upload(v2Alice, "", "", "big.txt", "0123456789")

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload = %d %s, want 413", rec.Code, rec.Body)
	}
}
