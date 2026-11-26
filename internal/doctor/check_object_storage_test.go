// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDetectStorageType(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want storageType
	}{
		{"s3 explicit", Env{ObjectStorageType: "s3"}, storageS3},
		{"S3_BUCKET_NAME implicit", Env{S3BucketName: "b"}, storageS3},
		{"azure", Env{ObjectStorageType: "azure"}, storageAzure},
		{"gcs", Env{ObjectStorageType: "gcs"}, storageGCS},
		{"none default", Env{}, storageNone},
		{"unrecognized type → none", Env{ObjectStorageType: "swift"}, storageNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := detectStorageType(tt.env)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestObjectStorageTCPCheck_NoneInLocalSkips(t *testing.T) {
	check := NewObjectStorageTCPCheck()
	got := check.Run(context.Background(), Env{}, ContextLocal)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}

func TestObjectStorageTCPCheck_NoneInHelmFails(t *testing.T) {
	check := NewObjectStorageTCPCheck()
	got := check.Run(context.Background(), Env{}, ContextHelm)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
}

func TestObjectStorageTCPCheck_S3AirGapWarn(t *testing.T) {
	dialer := func(_ context.Context, _ string) error { return nil }
	check := &ObjectStorageTCPCheck{Dialer: dialer}
	env := Env{ObjectStorageType: "s3", S3BucketName: "mybucket"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusWarn {
		t.Errorf("status: got %s, want WARN", got.Status)
	}
	if !strings.Contains(got.Reason, "air-gapped") {
		t.Errorf("reason: %q", got.Reason)
	}
	if !strings.Contains(got.Detail, "mybucket") {
		t.Errorf("detail should include bucket: %q", got.Detail)
	}
}

func TestObjectStorageTCPCheck_S3WithEndpointURLPasses(t *testing.T) {
	dialer := func(_ context.Context, addr string) error {
		if !strings.Contains(addr, "minio.local") {
			t.Errorf("dialer called with %s", addr)
		}
		return nil
	}
	check := &ObjectStorageTCPCheck{Dialer: dialer}
	env := Env{ObjectStorageType: "s3", S3BucketName: "b", S3EndpointURL: "https://minio.local:9000"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
}

func TestObjectStorageTCPCheck_S3DialFails(t *testing.T) {
	check := &ObjectStorageTCPCheck{Dialer: func(context.Context, string) error { return errors.New("nope") }}
	env := Env{ObjectStorageType: "s3", S3BucketName: "b"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "TCP connection") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestObjectStorageTCPCheck_AzurePass(t *testing.T) {
	dialer := func(_ context.Context, addr string) error {
		if !strings.Contains(addr, "myacct.blob.core.windows.net:443") {
			t.Errorf("addr: %q", addr)
		}
		return nil
	}
	check := &ObjectStorageTCPCheck{Dialer: dialer}
	env := Env{ObjectStorageType: "azure", AzureStorageAccountName: "myacct"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS", got.Status)
	}
}

func TestObjectStorageTCPCheck_GCSPass(t *testing.T) {
	dialer := func(_ context.Context, addr string) error {
		if addr != "storage.googleapis.com:443" {
			t.Errorf("addr: %q", addr)
		}
		return nil
	}
	check := &ObjectStorageTCPCheck{Dialer: dialer}
	env := Env{ObjectStorageType: "gcs"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS", got.Status)
	}
}

func TestObjectStorageAuthCheck_S3MissingCredsFails(t *testing.T) {
	check := NewObjectStorageAuthCheck()
	got := check.Run(context.Background(), Env{ObjectStorageType: "s3", S3BucketName: "b"}, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "No static credentials") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestObjectStorageAuthCheck_S3PassViaProbe(t *testing.T) {
	check := &ObjectStorageAuthCheck{Probe: func(_ context.Context, st storageType, _ Env) error {
		if st != storageS3 {
			t.Errorf("got type %q", st)
		}
		return nil
	}}
	env := Env{ObjectStorageType: "s3", S3BucketName: "b", AWSAccessKeyID: "k", AWSSecretAccessKey: "s"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
}

func TestObjectStorageAuthCheck_S3WarnOnError(t *testing.T) {
	check := &ObjectStorageAuthCheck{Probe: func(context.Context, storageType, Env) error {
		return errors.New("denied")
	}}
	env := Env{ObjectStorageType: "s3", S3BucketName: "b", AWSAccessKeyID: "k", AWSSecretAccessKey: "s"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusWarn {
		t.Errorf("status: got %s, want WARN", got.Status)
	}
	if !strings.Contains(got.Reason, "denied") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestObjectStorageAuthCheck_AzureMissingCreds(t *testing.T) {
	check := NewObjectStorageAuthCheck()
	env := Env{ObjectStorageType: "azure"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
}

func TestObjectStorageAuthCheck_GCSMissingCreds(t *testing.T) {
	check := NewObjectStorageAuthCheck()
	env := Env{ObjectStorageType: "gcs"}
	got := check.Run(context.Background(), env, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
}

func TestObjectStorageAuthCheck_NoneSkips(t *testing.T) {
	check := NewObjectStorageAuthCheck()
	got := check.Run(context.Background(), Env{}, ContextHelm)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}
