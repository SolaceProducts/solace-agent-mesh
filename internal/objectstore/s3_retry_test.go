// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

func TestS3LegacyRetryerKeepsConfiguredAttempts(t *testing.T) {
	t.Setenv("S3_RETRY_MAX_ATTEMPTS", "")

	tests := []struct {
		name string
		cfg  map[string]any
		want int
	}{
		{"default budget", nil, defaultS3RetryMaxAttempts},
		{"configured budget", map[string]any{"retry_max_attempts": 4}, 4},
		{"zero falls back to the SDK default of three", map[string]any{"retry_max_attempts": 0}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lo awsconfig.LoadOptions
			for _, opt := range withS3LegacyRetryer(nil, tt.cfg) {
				if err := opt(&lo); err != nil {
					t.Fatalf("apply option: %v", err)
				}
			}
			if lo.Retryer == nil {
				t.Fatal("no retryer set")
			}
			if got := lo.Retryer().MaxAttempts(); got != tt.want {
				t.Errorf("MaxAttempts = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestS3RetryMaxAttempts(t *testing.T) {
	t.Run("default when nothing set", func(t *testing.T) {
		t.Setenv("S3_RETRY_MAX_ATTEMPTS", "")
		if got := s3RetryMaxAttempts(nil); got != defaultS3RetryMaxAttempts {
			t.Errorf("got %d, want default %d", got, defaultS3RetryMaxAttempts)
		}
	})

	t.Run("env var override", func(t *testing.T) {
		t.Setenv("S3_RETRY_MAX_ATTEMPTS", "9")
		if got := s3RetryMaxAttempts(nil); got != 9 {
			t.Errorf("got %d, want 9 from env", got)
		}
	})

	t.Run("cfg wins over env", func(t *testing.T) {
		t.Setenv("S3_RETRY_MAX_ATTEMPTS", "9")
		for _, v := range []any{4, int64(4), float64(4), "4"} {
			if got := s3RetryMaxAttempts(map[string]any{"retry_max_attempts": v}); got != 4 {
				t.Errorf("cfg %T(%v): got %d, want 4", v, v, got)
			}
		}
	})

	t.Run("zero disables override (SDK default applies)", func(t *testing.T) {
		t.Setenv("S3_RETRY_MAX_ATTEMPTS", "")
		got := s3RetryMaxAttempts(map[string]any{"retry_max_attempts": 0})
		if got != 0 {
			t.Errorf("got %d, want 0 (caller skips WithRetryMaxAttempts)", got)
		}
	})
}

func TestWithS3RetryAppendsOption(t *testing.T) {
	t.Setenv("S3_RETRY_MAX_ATTEMPTS", "")

	base := 0
	if got := len(withS3Retry(nil, map[string]any{"retry_max_attempts": 5})); got != base+1 {
		t.Errorf("positive budget: appended %d options, want 1", got)
	}
	if got := len(withS3Retry(nil, map[string]any{"retry_max_attempts": 0})); got != base {
		t.Errorf("zero budget: appended %d options, want 0", got)
	}
}
