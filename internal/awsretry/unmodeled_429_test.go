// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awsretry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const unmodeledErrorBody = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>SlowDownPlease</Code><Message>busy</Message></Error>`

func unmodeledStatusServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(unmodeledErrorBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func s3ClientWithLegacyRetryer(endpoint string, maxAttempts int) *s3.Client {
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		Retryer: func() aws.Retryer {
			return retry.AddWithMaxBackoffDelay(Legacy(maxAttempts), time.Millisecond)
		},
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

func TestLegacyRetriesUnmodeled429UpToMaxAttempts(t *testing.T) {
	const maxAttempts = 4
	tests := []struct {
		name         string
		status       int
		wantRequests int32
	}{
		{"unmodeled 429 is retried until the attempt budget runs out", http.StatusTooManyRequests, maxAttempts},
		{"unmodeled 400 is not retried", http.StatusBadRequest, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, requests := unmodeledStatusServer(t, tt.status)
			client := s3ClientWithLegacyRetryer(srv.URL, maxAttempts)

			_, err := client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String("bucket"), Key: aws.String("key")})
			if err == nil {
				t.Fatal("GetObject succeeded against a server that only fails")
			}
			if got := requests.Load(); got != tt.wantRequests {
				t.Errorf("requests = %d, want %d", got, tt.wantRequests)
			}
		})
	}
}

func TestLegacyUnmodeled429CostsTheLegacyRetryCost(t *testing.T) {
	opErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
		Err:      &smithy.GenericAPIError{Code: "SlowDownPlease"},
	}
	r := Legacy(testMaxAttempts)
	if !r.IsErrorRetryable(opErr) {
		t.Fatal("unmodeled 429 is not retryable")
	}
	if got := retriesFromFullBucket(r, opErr); got != transientRetriesInFull {
		t.Errorf("retries from a full bucket = %d, want %d", got, transientRetriesInFull)
	}
	if _, err := r.GetRetryToken(context.Background(), opErr); err == nil {
		t.Error("retry token granted from an empty bucket")
	}
}
