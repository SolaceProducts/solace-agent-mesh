// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ttsKeyCanary = "AIzaMUSTNOTAPPEAR"

func newLeakProbeTTS(baseURL string) *GeminiTTS {
	return NewGeminiTTS(GeminiConfig{URL: baseURL, APIKey: ttsKeyCanary}, nil)
}

const unreachableBase = "http://127.0.0.1:1"

func TestSynthesize_TransportErrorCarriesNoAPIKey(t *testing.T) {
	_, _, err := newLeakProbeTTS(unreachableBase).Synthesize(context.Background(), "hello", TTSOptions{})
	if err == nil {
		t.Skip("something is listening on 127.0.0.1:1")
	}
	if strings.Contains(err.Error(), ttsKeyCanary) {
		t.Errorf("api key reached the returned error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gemini tts") {
		t.Errorf("lost the operation context: %q", err.Error())
	}
}

func TestSynthesizeStream_TransportErrorCarriesNoAPIKey(t *testing.T) {
	_, err := newLeakProbeTTS(unreachableBase).SynthesizeStream(context.Background(), "hello", TTSOptions{})
	if err == nil {
		t.Skip("something is listening on 127.0.0.1:1")
	}
	if strings.Contains(err.Error(), ttsKeyCanary) {
		t.Errorf("api key reached the returned error: %q", err.Error())
	}
}

func TestSynthesize_ErrorStatusOmitsProviderBody(t *testing.T) {
	const bodyCanary = "PROVIDER-BODY-MUST-NOT-APPEAR"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"` + bodyCanary + `"}}`))
	}))
	defer srv.Close()

	_, _, err := newLeakProbeTTS(srv.URL).Synthesize(context.Background(), "hello", TTSOptions{})
	if err == nil {
		t.Fatal("expected a 403 to produce an error")
	}
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("provider response body reached the error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("lost the status, which is the actionable part: %q", err.Error())
	}
}

func TestSynthesizeStream_ErrorStatusOmitsProviderBody(t *testing.T) {
	const bodyCanary = "STREAM-BODY-MUST-NOT-APPEAR"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(bodyCanary))
	}))
	defer srv.Close()

	_, err := newLeakProbeTTS(srv.URL).SynthesizeStream(context.Background(), "hello", TTSOptions{})
	if err == nil {
		t.Fatal("expected a 429 to produce an error")
	}
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("provider response body reached the error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("lost the status: %q", err.Error())
	}
}
