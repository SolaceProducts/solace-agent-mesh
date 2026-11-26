// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOutboundHTTPTransport_PreservesProxyFromEnvironment(t *testing.T) {
	wantProxyPtr := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()

	for _, insecure := range []bool{false, true} {
		tr := outboundHTTPTransport(insecure)

		if tr.Proxy == nil {
			t.Fatalf("Proxy is nil (insecure=%v) — HTTPS_PROXY/HTTP_PROXY/NO_PROXY will be ignored", insecure)
		}
		if got := reflect.ValueOf(tr.Proxy).Pointer(); got != wantProxyPtr {
			t.Errorf("Proxy does not point to http.ProxyFromEnvironment (insecure=%v)", insecure)
		}

		if tr.TLSClientConfig == nil {
			t.Fatalf("TLSClientConfig is nil (insecure=%v)", insecure)
		}
		if tr.TLSClientConfig.InsecureSkipVerify != insecure {
			t.Errorf("InsecureSkipVerify = %v, want %v", tr.TLSClientConfig.InsecureSkipVerify, insecure)
		}
	}
}

func TestElasticsearchConnect_HTTPSInstallsTransport(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":{"number":"8.0.0"}}`))
	}))
	defer srv.Close()

	b := newElasticsearchBackend(map[string]any{
		"connection_string":    srv.URL,
		"insecure_skip_verify": true,
	})
	_ = b.Connect(t.Context())

	if hits.Load() == 0 {
		t.Fatal("elasticsearch client never reached the stub server — transport wiring broken")
	}
}

const emptySearchResponse = `{"hits":{"total":{"value":0},"hits":[]}}`

func TestOpenSearchConnect_SendsNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	b := newOpenSearchBackend(map[string]any{"connection_string": srv.URL})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("Connect sent %d request(s), want 0", got)
	}
}

var awsStaticCredentials = map[string]any{
	"region":                "us-east-1",
	"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
	"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
}

func withConfig(base map[string]any, extra map[string]any) map[string]any {
	merged := maps.Clone(base)
	maps.Copy(merged, extra)
	return merged
}

func TestOpenSearchQuery_InstallsTransport(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(emptySearchResponse))
	}))
	defer srv.Close()

	branches := map[string]map[string]any{
		"plain": {},
		"aws":   awsStaticCredentials,
	}
	for branch, auth := range branches {
		for _, insecure := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/insecure_skip_verify=%v", branch, insecure), func(t *testing.T) {
				b := newOpenSearchBackend(withConfig(auth, map[string]any{
					"connection_string":    srv.URL,
					"insecure_skip_verify": insecure,
				}))
				if err := b.Connect(t.Context()); err != nil {
					t.Fatalf("Connect: %v", err)
				}
				_, err := b.ExecuteQuery(t.Context(), QueryParams{Index: "logs", Query: `{"query":{"match_all":{}}}`})
				if insecure && err != nil {
					t.Fatalf("ExecuteQuery with skip-verify: %v", err)
				}
				if !insecure && (err == nil || !strings.Contains(err.Error(), "certificate")) {
					t.Fatalf("ExecuteQuery without skip-verify err = %v, want a certificate error", err)
				}
			})
		}
	}
}

func TestOpenSearchQuery_AWSSignsWithContentHash(t *testing.T) {
	type observed struct {
		authorization string
		contentSHA256 string
		bodySHA256    string
	}
	seen := make(chan observed, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		select {
		case seen <- observed{
			authorization: r.Header.Get("Authorization"),
			contentSHA256: r.Header.Get("X-Amz-Content-Sha256"),
			bodySHA256:    hex.EncodeToString(sum[:]),
		}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(emptySearchResponse))
	}))
	defer srv.Close()

	b := newOpenSearchBackend(withConfig(awsStaticCredentials, map[string]any{"connection_string": srv.URL}))
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := b.ExecuteQuery(t.Context(), QueryParams{Index: "logs", Query: `{"query":{"match_all":{}}}`}); err != nil {
		t.Fatalf("ExecuteQuery: %v", err)
	}

	got := <-seen
	if !strings.Contains(got.authorization, "/us-east-1/es/aws4_request") {
		t.Fatalf("Authorization = %q, want a SigV4 signature scoped to us-east-1/es", got.authorization)
	}
	if got.contentSHA256 != got.bodySHA256 {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want the body hash %q", got.contentSHA256, got.bodySHA256)
	}
}

func TestOpenSearchClientConfig_SigningService(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"managed domain", "https://my-domain.us-east-1.es.amazonaws.com", "es"},
		{"serverless collection", "https://abc123def.us-east-1.aoss.amazonaws.com", "aoss"},
		{"serverless collection with port", "https://abc123def.us-east-1.aoss.amazonaws.com:443", "aoss"},
		{"serverless collection in the China partition", "https://abc123def.cn-north-1.aoss.amazonaws.com.cn", "aoss"},
		{"serverless suffix outside the host", "https://search.example.com/.aoss.amazonaws.com", "es"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newOpenSearchBackend(withConfig(awsStaticCredentials, map[string]any{"connection_string": tt.endpoint}))
			cfg, err := b.clientConfig(t.Context())
			if err != nil {
				t.Fatalf("clientConfig: %v", err)
			}
			if cfg.Signer == nil {
				t.Fatal("AWS config installed no signer")
			}

			const body = `{"query":{"match_all":{}}}`
			req := httptest.NewRequest(http.MethodPost, tt.endpoint+"/logs/_search", strings.NewReader(body))
			if err := cfg.Signer.SignRequest(req); err != nil {
				t.Fatalf("SignRequest: %v", err)
			}

			wantScope := "/us-east-1/" + tt.want + "/aws4_request"
			if got := req.Header.Get("Authorization"); !strings.Contains(got, wantScope) {
				t.Fatalf("Authorization = %q, want scope %q", got, wantScope)
			}
			sum := sha256.Sum256([]byte(body))
			if got, want := req.Header.Get("X-Amz-Content-Sha256"), hex.EncodeToString(sum[:]); got != want {
				t.Fatalf("X-Amz-Content-Sha256 = %q, want the body hash %q", got, want)
			}
		})
	}
}

func TestIsServerlessEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{"managed domain", "https://my-domain.us-east-1.es.amazonaws.com", false},
		{"serverless collection", "https://abc123def.us-east-1.aoss.amazonaws.com", true},
		{"serverless collection with port", "https://abc123def.us-east-1.aoss.amazonaws.com:443", true},
		{"serverless suffix outside the host", "https://search.example.com/.aoss.amazonaws.com", false},
		{"serverless collection in the China partition", "https://abc123def.cn-north-1.aoss.amazonaws.com.cn", true},
		{"upper-case host with trailing dot", "https://ABC123DEF.US-EAST-1.AOSS.AMAZONAWS.COM.", true},
		{"self-hosted endpoint", "https://opensearch.internal.example.com:9200", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isServerlessEndpoint(tt.endpoint); got != tt.want {
				t.Errorf("isServerlessEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}
