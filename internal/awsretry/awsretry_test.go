// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awsretry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
)

const (
	bucketTokens           = 500
	transientRetriesInFull = bucketTokens / 5
	timeoutRetriesInFull   = bucketTokens / 10
	firstRetryDelayCeiling = 2 * time.Second
	minLongestFirstDelay   = 50 * time.Millisecond
	backoffSamples         = 200
	testMaxAttempts        = 3
)

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }

func (timeoutError) Timeout() bool { return true }

func retriesFromFullBucket(r aws.Retryer, opErr error) int {
	n := 0
	for n <= bucketTokens {
		if _, err := r.GetRetryToken(context.Background(), opErr); err != nil {
			return n
		}
		n++
	}
	return n
}

func TestLegacyRetryCostByErrorKind(t *testing.T) {
	tests := []struct {
		name  string
		opErr error
		want  int
	}{
		{"transient error costs five tokens", errors.New("connection reset"), transientRetriesInFull},
		{"throttling error costs five tokens", &smithy.GenericAPIError{Code: "ThrottlingException"}, transientRetriesInFull},
		{"timeout costs ten tokens", timeoutError{}, timeoutRetriesInFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retriesFromFullBucket(Legacy(testMaxAttempts), tt.opErr); got != tt.want {
				t.Errorf("retries from a full bucket = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLegacyFirstRetryDelayIsDrawnFromOneSecondBase(t *testing.T) {
	r := Legacy(testMaxAttempts)
	var longest time.Duration
	for range backoffSamples {
		d, err := r.RetryDelay(1, errors.New("connection reset"))
		if err != nil {
			t.Fatalf("RetryDelay: %v", err)
		}
		if d >= firstRetryDelayCeiling {
			t.Fatalf("first retry delay %v is not below %v", d, firstRetryDelayCeiling)
		}
		longest = max(longest, d)
	}
	if longest < minLongestFirstDelay {
		t.Errorf("longest first retry delay over %d samples = %v, want at least %v", backoffSamples, longest, minLongestFirstDelay)
	}
}

func TestLegacyBackoffCapsAtTwentySeconds(t *testing.T) {
	d, err := Legacy(testMaxAttempts).RetryDelay(5, errors.New("connection reset"))
	if err != nil {
		t.Fatalf("RetryDelay: %v", err)
	}
	if want := 20 * time.Second; d != want {
		t.Errorf("delay after attempt 5 = %v, want %v", d, want)
	}
}

func TestApplyInstallsLegacyRetryerKeepingAttempts(t *testing.T) {
	tests := []struct {
		name string
		cfg  aws.Config
		want int
	}{
		{"unset attempts fall back to three", aws.Config{}, 3},
		{"configured attempts are kept", aws.Config{RetryMaxAttempts: 7}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply(tt.cfg)
			if got.Retryer == nil || !IsLegacy(got.Retryer()) {
				t.Fatal("config does not carry the legacy retryer")
			}
			if n := got.Retryer().MaxAttempts(); n != tt.want {
				t.Errorf("MaxAttempts = %d, want %d", n, tt.want)
			}
			if got.RetryMaxAttempts != 0 {
				t.Errorf("RetryMaxAttempts = %d, want 0 so the service client does not rewrap the retryer", got.RetryMaxAttempts)
			}
		})
	}
}

func TestApplyLeavesExplicitRetryerAlone(t *testing.T) {
	custom := func() aws.Retryer { return Legacy(9) }
	got := Apply(aws.Config{Retryer: custom})
	if n := got.Retryer().MaxAttempts(); n != 9 {
		t.Errorf("MaxAttempts = %d, want 9", n)
	}
}

func TestApplyLeavesAdaptiveModeAlone(t *testing.T) {
	got := Apply(aws.Config{RetryMode: aws.RetryModeAdaptive})
	if got.Retryer != nil {
		t.Error("adaptive mode config got a retryer installed")
	}
}

func TestLoadReturnsConfigWithLegacyRetryer(t *testing.T) {
	cfg, err := Load(t.Context(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Retryer == nil || !IsLegacy(cfg.Retryer()) {
		t.Fatal("loaded config does not carry the legacy retryer")
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", cfg.Region)
	}
}

func TestLegacyKeepsConfiguredMaxAttempts(t *testing.T) {
	if got := Legacy(6).MaxAttempts(); got != 6 {
		t.Errorf("MaxAttempts = %d, want 6", got)
	}
	if got := Provider(4)().MaxAttempts(); got != 4 {
		t.Errorf("Provider MaxAttempts = %d, want 4", got)
	}
}
