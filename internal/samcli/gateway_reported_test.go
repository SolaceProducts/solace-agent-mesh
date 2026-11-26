// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"testing"
)

func isolateGatewayRecording(t *testing.T) {
	t.Helper()
	previous := addressedGateway.Load()
	addressedGateway.Store(nil)
	t.Cleanup(func() { addressedGateway.Store(previous) })
}

func TestRecordAddressedGatewayIgnoresAnUnresolvedURL(t *testing.T) {
	isolateGatewayRecording(t)
	recordAddressedGateway("", "", "gateway-token", false, false)
	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway", got)
	}
}

func TestRecordAddressedGatewayIgnoresTheCommandsOwnFallback(t *testing.T) {
	isolateGatewayRecording(t)
	recordAddressedGateway(defaultTaskSendURL, defaultTaskSendURL, "", false, false)
	if got := recordedGateway(); got.Addressed() {
		t.Errorf("recordedGateway() = %+v, want the zero Gateway for the built-in fallback", got)
	}
}

func TestRecordAddressedGatewayRecordsWhenTheCommandHasNoFallback(t *testing.T) {
	isolateGatewayRecording(t)
	recordAddressedGateway("https://sam.example.com", "", "gateway-token", false, false)
	if got := recordedGateway(); !got.Addressed() {
		t.Error("a resolved gateway must be recorded when the command declares no fallback")
	}
}

func TestRecordAddressedGatewayCarriesTheCommandsOwnCredentials(t *testing.T) {
	isolateGatewayRecording(t)
	recordAddressedGateway("https://sam.example.com/", defaultTaskSendURL, "gateway-token", true, false)
	got := recordedGateway()
	if got.BaseURL != "https://sam.example.com/" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.Token != "gateway-token" {
		t.Errorf("Token was not carried through")
	}
	if !got.AllowPlainHTTP || got.SkipTLSVerify {
		t.Errorf("AllowPlainHTTP, SkipTLSVerify = %v, %v, want each as the command passed it", got.AllowPlainHTTP, got.SkipTLSVerify)
	}
}
