// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const keyCanary = "AIzaMUSTNOTAPPEAR"

func TestUnwrapURLError_DropsTheURLKeepsTheCause(t *testing.T) {
	cause := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://generativelanguage.googleapis.com/v1beta/models?key=" + keyCanary,
		Err: cause,
	}

	if !strings.Contains(wrapped.Error(), keyCanary) {
		t.Fatal("fixture is stale: *url.Error no longer renders the URL")
	}

	got := UnwrapURLError(wrapped)
	if strings.Contains(got.Error(), keyCanary) {
		t.Errorf("api key survived unwrapping: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "connection refused") {
		t.Errorf("lost the cause, which is the diagnostic: %q", got.Error())
	}
	if !errors.Is(got, cause) {
		t.Error("unwrapping broke the cause chain; errors.Is no longer matches")
	}
}

func TestUnwrapURLError_PreservesSentinelThroughWrapping(t *testing.T) {
	wrapped := &url.Error{
		Op:  "Post",
		URL: "https://x.example/v1?key=" + keyCanary,
		Err: context.DeadlineExceeded,
	}
	if !errors.Is(UnwrapURLError(wrapped), context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded no longer detectable after unwrapping")
	}
}

func TestUnwrapURLError_PassesThroughOtherErrors(t *testing.T) {
	plain := errors.New("some other failure")
	if got := UnwrapURLError(plain); got != plain {
		t.Errorf("rewrote a non-url.Error: %v", got)
	}
	if UnwrapURLError(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestUnwrapURLError_AgainstRealClientDoFailure(t *testing.T) {
	target := "http://127.0.0.1:1/v1beta/models?key=" + keyCanary
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("building the request failed: %v", err)
	}
	_, doErr := (&http.Client{}).Do(req)
	if doErr == nil {
		t.Skip("something is listening on 127.0.0.1:1")
	}
	if !strings.Contains(doErr.Error(), keyCanary) {
		t.Fatalf("fixture is stale: client.Do no longer embeds the URL: %v", doErr)
	}
	if got := UnwrapURLError(doErr); strings.Contains(got.Error(), keyCanary) {
		t.Errorf("api key survived a real Do failure: %q", got.Error())
	}
}
