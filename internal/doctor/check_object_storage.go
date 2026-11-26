// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/iterator"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

type storageType string

const (
	storageNone  storageType = "none"
	storageS3    storageType = "s3"
	storageAzure storageType = "azure"
	storageGCS   storageType = "gcs"
)

func detectStorageType(env Env) (storageType, string) {
	t := strings.ToLower(env.ObjectStorageType)
	switch t {
	case "s3", "azure", "gcs":
		return storageType(t), ""
	}
	if env.S3BucketName != "" {
		return storageS3, ""
	}
	if t != "" && t != "none" {
		return storageNone, fmt.Sprintf("OBJECT_STORAGE_TYPE=%q is not a recognised value (expected: s3, azure, gcs)", t)
	}
	return storageNone, "No object storage configured (OBJECT_STORAGE_TYPE not set and S3_BUCKET_NAME not set)"
}

type ObjectStorageTCPCheck struct {
	Dialer func(ctx context.Context, addr string) error
}

func NewObjectStorageTCPCheck() *ObjectStorageTCPCheck { return &ObjectStorageTCPCheck{} }

func (ObjectStorageTCPCheck) Name() string { return "object_storage" }

func (ObjectStorageTCPCheck) DisplayName() string { return "Object Storage" }

func (c *ObjectStorageTCPCheck) Run(ctx context.Context, env Env, doctorCtx Context) CheckResult {
	st, noneReason := detectStorageType(env)
	if st == storageNone {
		status := StatusSkip
		if doctorCtx.IsStrict() {
			status = StatusFail
		}
		return CheckResult{Name: c.DisplayName(), Status: status, Reason: noneReason}
	}

	dialer := c.Dialer
	if dialer == nil {
		dialer = func(ctx context.Context, addr string) error {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		}
	}

	switch st {
	case storageS3:
		return c.checkS3(ctx, env, dialer)
	case storageAzure:
		return c.checkAzure(ctx, env, dialer)
	case storageGCS:
		return c.checkGCS(ctx, env, dialer)
	default:
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Reason: fmt.Sprintf("Unrecognized storage type: %s", st)}
	}
}

func (c *ObjectStorageTCPCheck) checkS3(ctx context.Context, env Env, dialer func(context.Context, string) error) CheckResult {
	host, port, airGapAdvisory := s3Endpoint(env)
	addr := fmt.Sprintf("%s:%d", host, port)
	detail := s3Detail(env)
	if err := dialer(ctx, addr); err != nil {
		reason := fmt.Sprintf("TCP connection to %s failed: %v", addr, err)
		if airGapAdvisory != "" {
			reason += airGapAdvisory
		}
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: detail, Reason: reason}
	}
	if airGapAdvisory != "" {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusWarn,
			Detail: detail,
			Reason: "S3 reachable but S3_ENDPOINT_URL not set — set it for air-gapped or private S3 deployments",
		}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func (c *ObjectStorageTCPCheck) checkAzure(ctx context.Context, env Env, dialer func(context.Context, string) error) CheckResult {
	host := "blob.core.windows.net"
	if env.AzureStorageAccountName != "" {
		host = env.AzureStorageAccountName + ".blob.core.windows.net"
	}
	addr := host + ":443"
	detail := azureDetail(env)
	if err := dialer(ctx, addr); err != nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: detail,
			Reason: fmt.Sprintf("TCP connection to %s failed: %v", addr, err),
		}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func (c *ObjectStorageTCPCheck) checkGCS(ctx context.Context, _ Env, dialer func(context.Context, string) error) CheckResult {
	addr := "storage.googleapis.com:443"
	if err := dialer(ctx, addr); err != nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: "gcs",
			Reason: fmt.Sprintf("TCP connection to %s failed: %v", addr, err),
		}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: "gcs"}
}

func s3Endpoint(env Env) (string, int, string) {
	if env.S3EndpointURL == "" {
		region := firstNonEmpty(env.S3Region, env.AWSRegion, "us-east-1")
		return fmt.Sprintf("s3.%s.amazonaws.com", region), 443, " (S3_ENDPOINT_URL not set — set it for air-gapped or private S3 deployments)"
	}
	u, err := url.Parse(env.S3EndpointURL)
	if err != nil || u.Hostname() == "" {
		return env.S3EndpointURL, 443, ""
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if p := u.Port(); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	return u.Hostname(), port, ""
}

func s3Detail(env Env) string {
	bucket := firstNonEmpty(env.S3BucketName, env.ConnectorSpecBucketName)
	if bucket != "" {
		return "s3, " + bucket
	}
	return "s3"
}

func azureDetail(env Env) string {
	if env.AzureStorageAccountName != "" {
		return "azure, " + env.AzureStorageAccountName
	}
	return "azure"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type ObjectStorageAuthCheck struct {
	Probe func(ctx context.Context, st storageType, env Env) error
}

func NewObjectStorageAuthCheck() *ObjectStorageAuthCheck { return &ObjectStorageAuthCheck{} }

func (ObjectStorageAuthCheck) Name() string { return "object_storage_authentication" }

func (ObjectStorageAuthCheck) DisplayName() string { return "Object Storage Authentication" }

func (c *ObjectStorageAuthCheck) Run(ctx context.Context, env Env, _ Context) CheckResult {
	st, _ := detectStorageType(env)
	if st == storageNone {
		return CheckResult{Name: c.DisplayName(), Status: StatusSkip, Reason: "No object storage configured"}
	}

	switch st {
	case storageS3:
		return c.checkS3(ctx, env)
	case storageAzure:
		return c.checkAzure(ctx, env)
	case storageGCS:
		return c.checkGCS(ctx, env)
	default:
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Reason: fmt.Sprintf("Unrecognized storage type: %s", st)}
	}
}

func (c *ObjectStorageAuthCheck) probe(ctx context.Context, st storageType, env Env, real func() error) error {
	if c.Probe != nil {
		return c.Probe(ctx, st, env)
	}
	return real()
}

func clientFromEnv(env Env, kind storageType) (*objectstore.Client, error) {
	cfg := map[string]any{"type": string(kind)}
	switch kind {
	case storageS3:
		cfg["bucket_name"] = firstNonEmpty(env.S3BucketName, env.ConnectorSpecBucketName)
		cfg["region"] = firstNonEmpty(env.S3Region, env.AWSRegion, "us-east-1")
		cfg["endpoint_url"] = env.S3EndpointURL
		cfg["aws_access_key_id"] = env.AWSAccessKeyID
		cfg["aws_secret_access_key"] = env.AWSSecretAccessKey
	case storageAzure:
		cfg["bucket_name"] = "_doctor_probe"
		cfg["connection_string"] = env.AzureStorageConnectionString
		cfg["account_name"] = env.AzureStorageAccountName
		cfg["account_key"] = env.AzureStorageAccountKey
	case storageGCS:
		cfg["bucket_name"] = firstNonEmpty(env.S3BucketName, env.ConnectorSpecBucketName, "_doctor_probe")
		cfg["credentials_file"] = env.GoogleApplicationCredentials
	}
	return objectstore.NewFromConfig(cfg)
}

func (c *ObjectStorageAuthCheck) checkS3(ctx context.Context, env Env) CheckResult {
	detail := s3Detail(env)
	if env.AWSAccessKeyID == "" || env.AWSSecretAccessKey == "" {
		return c.failMissingCredentials(detail)
	}
	err := c.probe(ctx, storageS3, env, func() error {
		client, err := clientFromEnv(env, storageS3)
		if err != nil {
			return err
		}
		s3Client := client.S3()
		bucket := firstNonEmpty(env.S3BucketName, env.ConnectorSpecBucketName)
		if bucket != "" {
			_, err = s3Client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(bucket)})
			return err
		}
		_, err = s3Client.ListBuckets(ctx, &awss3.ListBucketsInput{})
		return err
	})
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: detail, Reason: fmt.Sprintf("cannot verify S3 credentials: %s", truncate(err.Error(), 200))}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func (c *ObjectStorageAuthCheck) checkAzure(ctx context.Context, env Env) CheckResult {
	detail := azureDetail(env)
	if env.AzureStorageAccountKey == "" && env.AzureStorageConnectionString == "" {
		return c.failMissingCredentials(detail)
	}
	err := c.probe(ctx, storageAzure, env, func() error {
		client, err := clientFromEnv(env, storageAzure)
		if err != nil {
			return err
		}
		_, err = client.AzureContainer().GetAccountInfo(ctx, nil)
		return err
	})
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: detail, Reason: fmt.Sprintf("cannot verify Azure credentials: %s", truncate(err.Error(), 200))}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func (c *ObjectStorageAuthCheck) checkGCS(ctx context.Context, env Env) CheckResult {
	detail := "gcs"
	if env.GoogleApplicationCredentials == "" {
		return c.failMissingCredentials(detail)
	}
	err := c.probe(ctx, storageGCS, env, func() error {
		client, err := clientFromEnv(env, storageGCS)
		if err != nil {
			return err
		}
		it := client.GCSClient().Buckets(ctx, "")
		_, err = it.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		return err
	})
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: detail, Reason: fmt.Sprintf("cannot verify GCS credentials: %s", truncate(err.Error(), 200))}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func (c *ObjectStorageAuthCheck) failMissingCredentials(detail string) CheckResult {
	return CheckResult{
		Name:   c.DisplayName(),
		Status: StatusFail,
		Detail: detail,
		Reason: "No static credentials provided (if using workload identity, skip this check via SAM_DOCTOR_SKIP_CHECKS)",
	}
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}
