// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestClient_SendText(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody outboundTextMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT123"}]}`))
	}))
	defer srv.Close()

	cfg := &WhatsAppConfig{
		AccessToken:     "tok-abc",
		PhoneNumberID:   "555111",
		GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL,
	}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	id, err := c.SendText(t.Context(), "15551234567", "Hello from SAM")
	if err != nil {
		t.Fatalf("SendText error: %v", err)
	}
	if id != "wamid.OUT123" {
		t.Errorf("returned message id = %q, want %q", id, "wamid.OUT123")
	}
	if gotPath != "/v26.0/555111/messages" {
		t.Errorf("path = %q, want /v26.0/555111/messages", gotPath)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("auth header = %q, want Bearer tok-abc", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody.MessagingProduct != "whatsapp" || gotBody.Type != "text" {
		t.Errorf("body messaging_product/type = %q/%q, want whatsapp/text", gotBody.MessagingProduct, gotBody.Type)
	}
	if gotBody.To != "15551234567" {
		t.Errorf("body to = %q, want 15551234567", gotBody.To)
	}
	if gotBody.Text.Body != "Hello from SAM" {
		t.Errorf("body text = %q, want %q", gotBody.Text.Body, "Hello from SAM")
	}
}

func TestClient_FetchMedia(t *testing.T) {
	const wantBytes = "PDF-CONTENT-BYTES"
	var metaAuth, bytesAuth string

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/media-bytes/", func(w http.ResponseWriter, r *http.Request) {
		bytesAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(wantBytes))
	})
	mux.HandleFunc("/v26.0/MEDIA123", func(w http.ResponseWriter, r *http.Request) {
		metaAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/MEDIA123","mime_type":"application/pdf","file_size":` +
			intToStr(len(wantBytes)) + `,"id":"MEDIA123"}`))
	})

	cfg := &WhatsAppConfig{
		AccessToken:     "tok-media",
		PhoneNumberID:   "555",
		GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL,
	}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	data, mime, err := c.FetchMedia(t.Context(), "MEDIA123", 1<<20)
	if err != nil {
		t.Fatalf("FetchMedia error: %v", err)
	}
	if string(data) != wantBytes {
		t.Errorf("bytes = %q, want %q", string(data), wantBytes)
	}
	if mime != "application/pdf" {
		t.Errorf("mime = %q, want application/pdf", mime)
	}
	if metaAuth != "Bearer tok-media" {
		t.Errorf("metadata auth = %q, want Bearer tok-media", metaAuth)
	}
	if bytesAuth != "Bearer tok-media" {
		t.Errorf("bytes auth = %q, want Bearer tok-media", bytesAuth)
	}
}

func TestClient_FetchMedia_TooLarge(t *testing.T) {
	var bytesFetched bool
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/media-bytes/", func(w http.ResponseWriter, _ *http.Request) {
		bytesFetched = true
		_, _ = w.Write([]byte("should-not-be-fetched"))
	})
	mux.HandleFunc("/v26.0/BIG", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/BIG","mime_type":"image/jpeg","file_size":5000000,"id":"BIG"}`))
	})

	cfg := &WhatsAppConfig{AccessToken: "t", PhoneNumberID: "5", GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, _, err := c.FetchMedia(t.Context(), "BIG", 1024)
	if err == nil {
		t.Fatal("expected error for oversized media, got nil")
	}
	if !errors.Is(err, ErrMediaTooLarge) {
		t.Errorf("error = %v, want ErrMediaTooLarge", err)
	}
	if bytesFetched {
		t.Error("oversized media bytes were downloaded; should reject from metadata only")
	}
}

func intToStr(n int) string { return strconv.Itoa(n) }

func TestClient_UploadMedia(t *testing.T) {
	var gotPath, gotProduct, gotType, gotFilename, gotPartContentType string
	var gotFileContent []byte
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseMultipartForm(1 << 20)
		gotProduct = r.FormValue("messaging_product")
		gotType = r.FormValue("type")
		f, hdr, err := r.FormFile("file")
		if err == nil {
			defer f.Close() //nolint:errcheck
			gotFileContent, _ = io.ReadAll(f)
			gotFilename = hdr.Filename
			gotPartContentType = hdr.Header.Get("Content-Type")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"media-up-1"}`))
	}))
	defer srv.Close()

	cfg := &WhatsAppConfig{AccessToken: "tok-up", PhoneNumberID: "555", GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	id, err := c.UploadMedia(t.Context(), []byte("FILE-BYTES"), "image/png", "out.png")
	if err != nil {
		t.Fatalf("UploadMedia error: %v", err)
	}
	if id != "media-up-1" {
		t.Errorf("media id = %q, want media-up-1", id)
	}
	if gotPath != "/v26.0/555/media" {
		t.Errorf("path = %q, want /v26.0/555/media", gotPath)
	}
	if gotAuth != "Bearer tok-up" {
		t.Errorf("auth = %q, want Bearer tok-up", gotAuth)
	}
	if gotProduct != "whatsapp" {
		t.Errorf("messaging_product = %q, want whatsapp", gotProduct)
	}
	if gotType != "image/png" {
		t.Errorf("type = %q, want image/png", gotType)
	}
	if string(gotFileContent) != "FILE-BYTES" {
		t.Errorf("file content = %q, want FILE-BYTES", string(gotFileContent))
	}
	if gotFilename != "out.png" {
		t.Errorf("file filename = %q, want out.png", gotFilename)
	}
	if gotPartContentType != "image/png" {
		t.Errorf("file part Content-Type = %q, want image/png (Meta validates this header, not the type field)", gotPartContentType)
	}
}

func TestClient_SendMedia(t *testing.T) {
	var gotBody outboundMediaMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.MED"}]}`))
	}))
	defer srv.Close()

	cfg := &WhatsAppConfig{AccessToken: "t", PhoneNumberID: "555", GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Run("image", func(t *testing.T) {
		id, err := c.SendMedia(t.Context(), "15551234567", "media-1", "image", "")
		if err != nil {
			t.Fatalf("SendMedia image error: %v", err)
		}
		if id != "wamid.MED" {
			t.Errorf("wamid = %q, want wamid.MED", id)
		}
		if gotBody.Type != "image" || gotBody.Image == nil || gotBody.Image.ID != "media-1" {
			t.Errorf("image body = %+v, want type=image image.id=media-1", gotBody)
		}
		if gotBody.To != "15551234567" {
			t.Errorf("to = %q, want 15551234567", gotBody.To)
		}
	})

	t.Run("document carries filename", func(t *testing.T) {
		_, err := c.SendMedia(t.Context(), "15551234567", "media-2", "document", "report.pdf")
		if err != nil {
			t.Fatalf("SendMedia document error: %v", err)
		}
		if gotBody.Type != "document" || gotBody.Document == nil || gotBody.Document.ID != "media-2" {
			t.Fatalf("document body = %+v, want type=document document.id=media-2", gotBody)
		}
		if gotBody.Document.Filename != "report.pdf" {
			t.Errorf("document filename = %q, want report.pdf", gotBody.Document.Filename)
		}
	})
}

func TestClient_SendText_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":80007,"message":"rate limit hit"}}`))
	}))
	defer srv.Close()

	cfg := &WhatsAppConfig{
		AccessToken:     "tok",
		PhoneNumberID:   "555",
		GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL,
	}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := c.SendText(t.Context(), "15551234567", "hi")
	if err == nil {
		t.Fatal("expected error on 429 response, got nil")
	}
}

func clientAgainst(url string) *Client {
	return NewClient(&WhatsAppConfig{
		AccessToken:     "tok",
		PhoneNumberID:   "555",
		GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: url,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestClient_SendText_2xxUnparseable(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	wamid, err := clientAgainst(srv.URL).SendText(t.Context(), "15551234567", "hi")
	if err != nil {
		t.Fatalf("an accepted send must not report failure: %v", err)
	}
	if wamid != "" {
		t.Errorf("wamid = %q, want empty — there was no id to read", wamid)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestSendText_TornResponseBodyIsAcceptedNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.PARTIAL"`))
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, hijackErr := hj.Hijack()
			if hijackErr == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	_, err := clientAgainst(srv.URL).SendText(t.Context(), "15551234567", "hi")
	if err == nil {
		t.Fatal("a torn response body must surface, not read as a clean success")
	}
	if !errors.Is(err, errRequestAccepted) {
		t.Fatalf("error = %v, want it marked accepted so the pool records a delivery rather than a failure", err)
	}
	if _, _, retryable, _ := classifySendError(err); retryable {
		t.Error("a message Meta accepted must not be retried; retrying delivers it twice")
	}
}

func TestClient_SendText_2xxNoMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[]}`))
	}))
	defer srv.Close()

	id, err := clientAgainst(srv.URL).SendText(t.Context(), "15551234567", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("message id = %q, want empty", id)
	}
}

func TestClient_FetchMedia_MetadataError(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/M", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
	})
	_, _, err := clientAgainst(srv.URL).FetchMedia(t.Context(), "M", 1<<20)
	if err == nil {
		t.Fatal("expected error on metadata 404, got nil")
	}
}

func TestClient_FetchMedia_NoURL(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/M", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mime_type":"image/jpeg","file_size":10,"id":"M"}`))
	})
	_, _, err := clientAgainst(srv.URL).FetchMedia(t.Context(), "M", 1<<20)
	if err == nil {
		t.Fatal("expected error when metadata has no url, got nil")
	}
}

func TestClient_FetchMedia_DownloadError(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/M", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/dl","mime_type":"image/jpeg","file_size":10,"id":"M"}`))
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	_, _, err := clientAgainst(srv.URL).FetchMedia(t.Context(), "M", 1<<20)
	if err == nil {
		t.Fatal("expected error on download 500, got nil")
	}
}

func TestClient_FetchMedia_DownloadCap(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/M", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/dl","mime_type":"image/jpeg","file_size":0,"id":"M"}`))
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 5000)) })

	_, _, err := clientAgainst(srv.URL).FetchMedia(t.Context(), "M", 1024)
	if !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("expected ErrMediaTooLarge from the download-time cap, got %v", err)
	}
}

func TestClient_UploadMedia_Errors(t *testing.T) {
	t.Run("api error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad media"}}`))
		}))
		defer srv.Close()
		if _, err := clientAgainst(srv.URL).UploadMedia(t.Context(), []byte("x"), "image/png", "x.png"); err == nil {
			t.Fatal("expected error on 400 upload")
		}
	})
	t.Run("missing id", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		if _, err := clientAgainst(srv.URL).UploadMedia(t.Context(), []byte("x"), "image/png", "x.png"); err == nil {
			t.Fatal("expected error when upload response has no id")
		}
	})
}

func TestClient_UploadMedia_EdgeCases(t *testing.T) {
	t.Run("empty filename defaults", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m1"}`))
		}))
		defer srv.Close()
		id, err := clientAgainst(srv.URL).UploadMedia(t.Context(), []byte("x"), "image/png", "")
		if err != nil || id != "m1" {
			t.Fatalf("UploadMedia(empty filename) = %q, %v; want m1, nil", id, err)
		}
	})
	t.Run("unparseable 2xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		if _, err := clientAgainst(srv.URL).UploadMedia(t.Context(), []byte("x"), "image/png", "x.png"); err == nil {
			t.Fatal("expected error on unparseable upload response")
		}
	})
}

func TestClient_SendInteractiveButtons(t *testing.T) {
	var got outboundInteractiveMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.INT"}]}`))
	}))
	defer srv.Close()
	cfg := &WhatsAppConfig{AccessToken: "t", PhoneNumberID: "5", GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	id, err := c.SendInteractiveButtons(t.Context(), "15551234567", "Pick one:",
		[]replyButton{{ID: "opt:0", Title: "Yes"}, {ID: "opt:1", Title: "No"}})
	if err != nil {
		t.Fatalf("SendInteractiveButtons: %v", err)
	}
	if id != "wamid.INT" {
		t.Errorf("id = %q, want wamid.INT", id)
	}
	if got.Type != "interactive" || got.Interactive.Type != "button" {
		t.Fatalf("type/interactive.type = %q/%q, want interactive/button", got.Type, got.Interactive.Type)
	}
	if got.Interactive.Body.Text != "Pick one:" {
		t.Errorf("body = %q, want 'Pick one:'", got.Interactive.Body.Text)
	}
	if len(got.Interactive.Action.Buttons) != 2 ||
		got.Interactive.Action.Buttons[0].Reply.ID != "opt:0" ||
		got.Interactive.Action.Buttons[1].Reply.Title != "No" {
		t.Errorf("buttons = %+v", got.Interactive.Action.Buttons)
	}
}

func TestClient_SendInteractiveList(t *testing.T) {
	var got outboundInteractiveMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.LST"}]}`))
	}))
	defer srv.Close()
	cfg := &WhatsAppConfig{AccessToken: "t", PhoneNumberID: "5", GraphAPIVersion: "v26.0", GraphAPIBaseURL: srv.URL}
	c := NewClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := c.SendInteractiveList(t.Context(), "15551234567", "Choose a fruit", "Options",
		[]listRow{{ID: "opt:0", Title: "Apple"}, {ID: "opt:1", Title: "Banana", Description: "yellow"}})
	if err != nil {
		t.Fatalf("SendInteractiveList: %v", err)
	}
	if got.Interactive.Type != "list" {
		t.Fatalf("interactive.type = %q, want list", got.Interactive.Type)
	}
	if got.Interactive.Action.Button != "Options" {
		t.Errorf("list button = %q, want Options", got.Interactive.Action.Button)
	}
	if len(got.Interactive.Action.Sections) != 1 || len(got.Interactive.Action.Sections[0].Rows) != 2 {
		t.Fatalf("sections/rows = %+v", got.Interactive.Action.Sections)
	}
	if got.Interactive.Action.Sections[0].Rows[1].Description != "yellow" {
		t.Errorf("row desc = %q, want yellow", got.Interactive.Action.Sections[0].Rows[1].Description)
	}
}
