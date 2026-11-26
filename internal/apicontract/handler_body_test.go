// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type uploadWidgetRequest struct {
	ID    string       `path:"id" json:"-"`
	File  UploadedFile `form:"file" json:"-"`
	Notes string       `form:"notes" json:"-"`
}

type putWidgetChunkRequest struct {
	WidgetID string  `path:"widgetId" json:"-"`
	Index    int     `path:"id" json:"-"`
	Chunk    RawBody `json:"-"`
}

type getBuiltinWidgetRequest struct {
	ID string `path:"id,builtin" json:"-"`
}

func bodyMux(got *any) *http.ServeMux {
	mux := http.NewServeMux()
	for pattern, h := range map[string]Handler{
		"PUT /widgets/{id}/bundle": Handle(func(_ context.Context, req uploadWidgetRequest) (widget, error) {
			*got = req
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to upload widget bundle"),
		"PUT /widgets/{widgetId}/chunks/{id}": Handle(func(_ context.Context, req putWidgetChunkRequest) (struct{}, error) {
			*got = req
			return struct{}{}, nil
		}, "Unable to store chunk"),
		"GET /widgets/{id}": Handle(func(_ context.Context, req getBuiltinWidgetRequest) (widget, error) {
			*got = req
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to get widget"),
		"GET /widgets/{id}/bundle": Handle(func(_ context.Context, req getWidgetRequest) (FileResponse, error) {
			if req.ID != widgetID {
				return FileResponse{}, errors.New("no bundle")
			}
			return FileResponse{ContentType: "application/zip", Filename: "my widget.zip", Size: 3, Headers: map[string]string{"X-Truncated": "true", "Content-Security-Policy": "default-src *", "X-Content-Type-Options": "sniff"}, Body: io.NopCloser(strings.NewReader("zip"))}, nil
		}, "Unable to download widget bundle"),
	} {
		mux.HandleFunc(pattern, h.serve)
	}
	return mux
}

func multipartRequest(t *testing.T, target string, files, values map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		part, err := mw.CreateFormFile(name, name+".zip")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(content))
	}
	for name, value := range values {
		if err := mw.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, target, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestHandle_BindsMultipartForm(t *testing.T) {
	var got any
	mux := bodyMux(&got)
	rec := serveWidgetRequest(mux, multipartRequest(t, "/widgets/"+widgetID+"/bundle",
		map[string]string{"file": "PK-bytes"}, map[string]string{"notes": "v2"}))
	expectStatus(t, rec, http.StatusOK)
	req := got.(uploadWidgetRequest)
	if req.File.Name != "file.zip" || string(req.File.Data) != "PK-bytes" || req.Notes != "v2" || req.ID != widgetID {
		t.Errorf("bound %+v", req)
	}
}

func TestHandle_MultipartFormProblems(t *testing.T) {
	mux := bodyMux(new(any))
	target := "/widgets/" + widgetID + "/bundle"

	missing := serveWidgetRequest(mux, multipartRequest(t, target, nil, map[string]string{"notes": "v2"}))
	expectDetail(t, missing, http.StatusUnprocessableEntity, "file")

	both := serveWidgetRequest(mux, multipartRequest(t, target, nil, map[string]string{"extra": "x"}))
	body := decodeError(t, both)
	expectStatus(t, both, http.StatusBadRequest)
	if body.ValidationDetails["extra"] == nil || body.ValidationDetails["file"] == nil {
		t.Errorf("want both the unknown and the missing part listed, got %v", body.ValidationDetails)
	}

	misplaced := serveWidgetRequest(mux, multipartRequest(t, target, map[string]string{"file": "a", "notes": "b"}, nil))
	expectDetail(t, misplaced, http.StatusBadRequest, "notes")

	notMultipart := serveWidget(mux, http.MethodPut, target, `{"file":"x"}`)
	expectStatus(t, notMultipart, http.StatusBadRequest)
	decodeError(t, notMultipart)
}

func TestHandle_BindsRawBodyAndIndex(t *testing.T) {
	var got any
	mux := bodyMux(&got)
	rec := serveWidget(mux, http.MethodPut, "/widgets/"+widgetID+"/chunks/3", "chunk-bytes")
	expectStatus(t, rec, http.StatusNoContent)
	if req := got.(putWidgetChunkRequest); req.Index != 3 || string(req.Chunk) != "chunk-bytes" {
		t.Errorf("bound %+v", req)
	}
	for _, bad := range []string{"-1", "03", "x", "9999999999999999999999"} {
		expectDetail(t, serveWidget(mux, http.MethodPut, "/widgets/"+widgetID+"/chunks/"+bad, "c"), http.StatusBadRequest, "id")
	}
}

func TestHandle_BuiltinPathTakesAUUIDOrABuiltinID(t *testing.T) {
	var got any
	mux := bodyMux(&got)
	for _, id := range []string{widgetID, "builtin_artifact_tools", "sam-docs"} {
		expectStatus(t, serveWidget(mux, http.MethodGet, "/widgets/"+id, ""), http.StatusOK)
		if req := got.(getBuiltinWidgetRequest); req.ID != id {
			t.Errorf("bound %q, want %q", req.ID, id)
		}
	}
	for _, bad := range []string{"Sam-Docs", "sam--docs", "sam_docs_", "a.b"} {
		expectDetail(t, serveWidget(mux, http.MethodGet, "/widgets/"+bad, ""), http.StatusBadRequest, "id")
	}
}

func TestHandle_FileResponse(t *testing.T) {
	mux := bodyMux(new(any))
	rec := serveWidget(mux, http.MethodGet, "/widgets/"+widgetID+"/bundle", "")
	expectStatus(t, rec, http.StatusOK)
	for header, want := range map[string]string{
		"Content-Type":            "application/zip",
		"Content-Disposition":     "attachment; filename*=UTF-8''my%20widget.zip",
		"Content-Length":          "3",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"X-Truncated":             "true",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if rec.Body.String() != "zip" {
		t.Errorf("body %q", rec.Body.String())
	}

	failed := serveWidget(mux, http.MethodGet, "/widgets/019f9b46-77bc-76ba-a494-017487b5f5c5/bundle", "")
	expectStatus(t, failed, http.StatusInternalServerError)
	if decodeError(t, failed).Message != "Unable to download widget bundle" {
		t.Errorf("a failure before streaming sends the envelope, got %s", failed.Body.String())
	}
}

func TestWriteBodyReadError_TooLargeIs413(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBodyReadError(rec, &http.MaxBytesError{Limit: 1})
	expectStatus(t, rec, http.StatusRequestEntityTooLarge)
	if decodeError(t, rec).Message != MsgBodyTooLarge {
		t.Errorf("message %s", rec.Body.String())
	}
}
