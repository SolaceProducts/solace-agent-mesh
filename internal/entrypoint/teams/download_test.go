// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestDownloadAttachment_FileDownloadInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("file content here")) //nolint:errcheck
	}))
	defer srv.Close()

	att := Attachment{
		Name:        "report.csv",
		ContentType: "application/vnd.microsoft.teams.file.download.info",
		Content: map[string]any{
			"downloadUrl": srv.URL + "/download",
			"fileType":    "csv",
		},
	}

	file, err := downloadAttachment(context.Background(), att, 100, runtime.NoopMetrics{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if file.Name != "report.csv" {
		t.Errorf("name = %q, want %q", file.Name, "report.csv")
	}
	if file.MIMEType != "text/csv" {
		t.Errorf("mime = %q, want %q", file.MIMEType, "text/csv")
	}
	if string(file.Content) != "file content here" {
		t.Errorf("content = %q, want %q", string(file.Content), "file content here")
	}
}

func TestDownloadAttachment_ContentURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key":"value"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	att := Attachment{
		Name:       "data.json",
		ContentURL: srv.URL + "/file",
	}

	file, err := downloadAttachment(context.Background(), att, 100, runtime.NoopMetrics{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if file.MIMEType != "application/json" {
		t.Errorf("mime = %q, want %q", file.MIMEType, "application/json")
	}
}

func TestDownloadAttachment_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ab")) //nolint:errcheck
	}))
	defer srv.Close()

	att := Attachment{
		Name:       "big.bin",
		ContentURL: srv.URL + "/file",
	}

	_, err := downloadAttachment(context.Background(), att, 0, runtime.NoopMetrics{}, slog.Default())
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestDownloadAttachment_NoURL(t *testing.T) {
	att := Attachment{
		Name: "nourl.txt",
	}

	_, err := downloadAttachment(context.Background(), att, 100, runtime.NoopMetrics{}, slog.Default())
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestNormalizeMIMEType(t *testing.T) {
	tests := []struct {
		fileType string
		respCT   string
		filename string
		want     string
	}{
		{"csv", "", "", "text/csv"},
		{"json", "", "", "application/json"},
		{"", "text/html; charset=utf-8", "", "text/html"},
		{"", "", "doc.pdf", "application/pdf"},
		{"", "", "file.unknown", "application/octet-stream"},
		{"text/plain", "", "", "text/plain"},
		{"", "application/octet-stream", "data.yaml", "text/yaml"},
	}
	for _, tt := range tests {
		got := normalizeMIMEType(tt.fileType, tt.respCT, tt.filename)
		if got != tt.want {
			t.Errorf("normalizeMIMEType(%q, %q, %q) = %q, want %q",
				tt.fileType, tt.respCT, tt.filename, got, tt.want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal", "normal.txt", "normal.txt"},
		{"multiple dots preserved", "file.name.with.dots.txt", "file.name.with.dots.txt"},
		{"slash traversal", "path/to/file.txt", "path_to_file.txt"},
		{"illegal chars", "bad:chars|here.txt", "bad_chars_here.txt"},
		{"all special chars replaced", "f<>:\"|?*\x00.txt", "f________.txt"},
		{"long name with extension truncated, extension kept", strings.Repeat("a", 250) + ".txt", strings.Repeat("a", 195) + ".txt"},
		{"long name without extension truncated", strings.Repeat("a", 250), strings.Repeat("a", 200)},
		{"hidden dots stripped", ".hidden.", "hidden"},
		{"whitespace only", "  .  ", "file"},
		{"empty", "", "file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeFilename_PathTraversalNeutralized(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"../../../etc/passwd",
		`..\..\windows\system32`,
		`..\..//etc/passwd`,
		"./inner/../../escape",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := sanitizeFilename(in)
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("sanitizeFilename(%q) = %q, still contains a path separator", in, got)
			}
			if strings.HasPrefix(got, "..") || strings.HasPrefix(got, ".") {
				t.Errorf("sanitizeFilename(%q) = %q, must not start with dot/parent-dir", in, got)
			}
			if got == "" {
				t.Errorf("sanitizeFilename(%q) returned empty (must fall back to %q)", in, "file")
			}
		})
	}
}

func TestHeadTeamsAttachmentSize(t *testing.T) {
	cases := []struct {
		name       string
		buildAtt   func(srvURL string) Attachment
		respHeader map[string]string
		respStatus int
		want       int64
		noServer   bool
	}{
		{
			name: "FileDownloadInfo with Content-Length",
			buildAtt: func(srvURL string) Attachment {
				return Attachment{
					ContentType: "application/vnd.microsoft.teams.file.download.info",
					Content:     map[string]any{"downloadUrl": srvURL},
				}
			},
			respHeader: map[string]string{"Content-Length": "1048576"},
			respStatus: http.StatusOK,
			want:       1048576,
		},
		{
			name: "falls back to ContentURL when no FileDownloadInfo",
			buildAtt: func(srvURL string) Attachment {
				return Attachment{ContentURL: srvURL}
			},
			respHeader: map[string]string{"Content-Length": "2097152"},
			respStatus: http.StatusOK,
			want:       2097152,
		},
		{
			name: "no download URL returns -1",
			buildAtt: func(string) Attachment {
				return Attachment{ContentType: "text/plain"}
			},
			noServer: true,
			want:     -1,
		},
		{
			name: "missing Content-Length returns -1",
			buildAtt: func(srvURL string) Attachment {
				return Attachment{ContentURL: srvURL}
			},
			respHeader: map[string]string{},
			respStatus: http.StatusOK,
			want:       -1,
		},
		{
			name: "HTTP error returns -1",
			buildAtt: func(srvURL string) Attachment {
				return Attachment{ContentURL: srvURL}
			},
			respHeader: map[string]string{"Content-Length": "9999"},
			respStatus: http.StatusInternalServerError,
			want:       -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var srvURL string
			if !tc.noServer {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodHead {
						t.Errorf("request method = %s, want HEAD", r.Method)
					}
					for k, v := range tc.respHeader {
						w.Header().Set(k, v)
					}
					w.WriteHeader(tc.respStatus)
				}))
				defer srv.Close()
				srvURL = srv.URL
			}

			got := headTeamsAttachmentSize(context.Background(), tc.buildAtt(srvURL), slog.Default())
			if got != tc.want {
				t.Errorf("headTeamsAttachmentSize = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsFileAttachment(t *testing.T) {
	tests := []struct {
		name string
		att  Attachment
		want bool
	}{
		{"file download info", Attachment{ContentType: "application/vnd.microsoft.teams.file.download.info"}, true},
		{"content URL", Attachment{ContentURL: "https://example.com/file"}, true},
		{"adaptive card", Attachment{ContentType: "application/vnd.microsoft.card.adaptive"}, false},
		{"empty", Attachment{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFileAttachment(tt.att)
			if got != tt.want {
				t.Errorf("isFileAttachment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDownloadAttachment_TransportErrorOmitsURLQuery(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	downloadURL := srv.URL + "/sites/team/report.csv?tempauth=secret-token-value"
	srv.Close()

	att := Attachment{Name: "report.csv", ContentURL: downloadURL}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	_, err := downloadAttachment(context.Background(), att, 100, runtime.NoopMetrics{}, logger)
	if err == nil {
		t.Fatal("expected a transport error from a closed server")
	}
	if headTeamsAttachmentSize(context.Background(), att, logger) != -1 {
		t.Fatal("expected -1 from a closed server")
	}
	for name, text := range map[string]string{"error": err.Error(), "log": logBuf.String()} {
		if strings.Contains(text, "secret-token-value") || strings.Contains(text, "/sites/team") {
			t.Errorf("%s keeps the URL path or query: %s", name, text)
		}
	}
	if !strings.Contains(err.Error(), "http://127.0.0.1") {
		t.Errorf("error should keep scheme and host: %s", err)
	}
}

func TestDownloadAttachment_CanceledStillMatchesContext(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := downloadAttachment(ctx, Attachment{Name: "a.txt", ContentURL: srv.URL + "/a.txt?tempauth=x"}, 100, runtime.NoopMetrics{}, slog.Default())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false: %v", err)
	}
}

func TestDownloadAttachment_BadURLOmitsQuery(t *testing.T) {
	_, err := downloadAttachment(context.Background(), Attachment{Name: "a.txt", ContentURL: "http://host/a.txt?tempauth=secret-token-value\x7f"}, 100, runtime.NoopMetrics{}, slog.Default())
	if err == nil {
		t.Fatal("expected a request build error")
	}
	if strings.Contains(err.Error(), "secret-token-value") {
		t.Errorf("error keeps the URL query: %s", err)
	}
}
