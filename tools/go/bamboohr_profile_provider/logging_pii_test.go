// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func captureLogs(t *testing.T) (*bytes.Buffer, *slog.Logger) {
	t.Helper()
	buf := &bytes.Buffer{}
	return buf, slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestProviderLogs_DoNotCarryTheLookupEmail(t *testing.T) {
	const missingEmail = "nobody@acme.co"

	buf, logger := captureLogs(t)
	p, _ := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	}))
	p.logger = logger

	profile, err := p.enrichByLookupValue(t.Context(), missingEmail)
	if err != nil {
		t.Fatalf("enrichByLookupValue: %v", err)
	}
	if profile != nil {
		t.Fatalf("expected a miss for %q, got a profile", missingEmail)
	}

	logs := buf.String()
	if strings.Contains(logs, missingEmail) {
		t.Errorf("logs disclose the looked-up email:\n%s", logs)
	}
	if !strings.Contains(logs, "lookupValueFP") {
		t.Errorf("miss was not recorded with a correlatable fingerprint:\n%s", logs)
	}
}

func TestClientError_OmitsResponseBodyAndPhotoURL(t *testing.T) {
	const upstreamBody = "employee jane.smith@acme.co is not visible to this key"

	t.Run("api path", func(t *testing.T) {
		p, _ := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, upstreamBody)
		}))

		_, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co")
		if err == nil {
			t.Fatal("expected an error from a 401 directory fetch")
		}
		if strings.Contains(err.Error(), upstreamBody) {
			t.Errorf("error carries the upstream response body: %v", err)
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("error dropped the status, which is what identifies the failure: %v", err)
		}
	})

	t.Run("photo url", func(t *testing.T) {
		p, srv := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/photos/") {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, upstreamBody)
				return
			}
			_, _ = io.WriteString(w, directoryPayload)
		}))
		photoURL := srv.URL + "/photos/secret-access-hash.jpg"

		_, _, err := p.client.fetchPhoto(t.Context(), photoURL)
		if err == nil {
			t.Fatal("expected an error from a 403 photo fetch")
		}
		if strings.Contains(err.Error(), "secret-access-hash") {
			t.Errorf("error carries the photo URL's access hash: %v", err)
		}
		if strings.Contains(err.Error(), upstreamBody) {
			t.Errorf("error carries the upstream response body: %v", err)
		}
	})
}

func TestTransportFailures_OmitTheRequestURL(t *testing.T) {
	serveDirectory := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})

	t.Run("photo url", func(t *testing.T) {
		p, srv := newTestProvider(t, serveDirectory)
		photoURL := srv.URL + "/photos/secret-access-hash.jpg"
		srv.Close()

		_, _, err := p.client.fetchPhoto(t.Context(), photoURL)
		if err == nil {
			t.Fatal("expected an error fetching a photo from a closed listener")
		}
		if strings.Contains(err.Error(), "secret-access-hash") {
			t.Errorf("transport error carries the photo URL's access hash: %v", err)
		}
	})

	t.Run("api url", func(t *testing.T) {
		p, srv := newTestProvider(t, serveDirectory)
		srv.Close()

		_, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co")
		if err == nil {
			t.Fatal("expected an error fetching the directory from a closed listener")
		}
		if strings.Contains(err.Error(), "/v1/employees/directory") {
			t.Errorf("transport error carries the request URL: %v", err)
		}
	})
}

func directoryWithPhotoAt(baseURL string) string {
	return `{"employees":[
		{"id":"10","displayName":"Jane Smith","workEmail":"jane.smith@acme.co","photoUrl":"` + baseURL + `/photo/jane.jpg"},
		{"id":"","displayName":"No Id","workEmail":"no.id@acme.co"}
	]}`
}

func TestProviderLogs_NeverCarryTheUserEmail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		email   string
		wantKey string
		drive   func(t *testing.T, p *provider)
	}{
		{
			name:    "whos-out failure for a resolved user",
			email:   "jane.smith@acme.co",
			wantKey: "userIDFP",
			drive: func(t *testing.T, p *provider) {
				if _, err := p.getTimeOff(t.Context(), "jane.smith@acme.co", "", ""); err != nil {
					t.Fatalf("getTimeOff: %v", err)
				}
			},
		},
		{
			name:    "whos-out failure for a batch",
			email:   "jane.smith@acme.co",
			wantKey: "userCount",
			drive: func(t *testing.T, p *provider) {
				ids := []string{"jane.smith@acme.co", "ghost@acme.co"}
				if _, err := p.getTimeOffMulti(t.Context(), ids, "", ""); err != nil {
					t.Fatalf("getTimeOffMulti: %v", err)
				}
			},
		},
		{
			name:    "user absent from the directory",
			email:   "ghost@acme.co",
			wantKey: "userIDFP",
			drive: func(t *testing.T, p *provider) {
				if _, err := p.getTimeOff(t.Context(), "ghost@acme.co", "", ""); err != nil {
					t.Fatalf("getTimeOff: %v", err)
				}
			},
		},
		{
			name:    "directory record without a bamboo id",
			email:   "no.id@acme.co",
			wantKey: "userIDFP",
			drive: func(t *testing.T, p *provider) {
				if _, err := p.getTimeOff(t.Context(), "no.id@acme.co", "", ""); err != nil {
					t.Fatalf("getTimeOff: %v", err)
				}
			},
		},
		{
			name:    "photo fetch failure",
			email:   "jane.smith@acme.co",
			wantKey: "userIDFP",
			drive: func(t *testing.T, p *provider) {
				if _, _, err := p.getUserProfilePicture(t.Context(), "jane.smith@acme.co"); err != nil {
					t.Fatalf("getUserProfilePicture: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var srvURL string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "employees/directory"):
					_, _ = io.WriteString(w, directoryWithPhotoAt(srvURL))
				case strings.Contains(r.URL.Path, "time_off/whos_out"),
					strings.Contains(r.URL.Path, "/photo/"):
					w.WriteHeader(http.StatusInternalServerError)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})

			buf, logger := captureLogs(t)
			p, srv := newTestProviderWithLogger(t, handler, logger)
			srvURL = srv.URL

			tc.drive(t, p)

			logs := buf.String()
			if logs == "" {
				t.Fatal("no log line was emitted, so this path asserts nothing")
			}
			if strings.Contains(logs, tc.email) {
				t.Errorf("logs disclose the user's email:\n%s", logs)
			}
			if !strings.Contains(logs, tc.wantKey) {
				t.Errorf("expected key %q in the log output:\n%s", tc.wantKey, logs)
			}
		})
	}
}
