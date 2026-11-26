// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

const s3DialConnectTimeout = 10 * time.Second

const s3MaxIdleConnsPerHost = 200

func tunedS3HTTPClient() *awshttp.BuildableClient {
	return awshttp.NewBuildableClient().
		WithDialerOptions(func(d *net.Dialer) {
			d.Timeout = s3DialConnectTimeout
		}).
		WithTransportOptions(func(tr *http.Transport) {
			tr.MaxIdleConnsPerHost = s3MaxIdleConnsPerHost
		})
}

const defaultS3RetryMaxAttempts = 6

func s3RetryMaxAttempts(cfg map[string]any) int {
	if cfg != nil {
		switch v := cfg["retry_max_attempts"].(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	if s := strings.TrimSpace(os.Getenv("S3_RETRY_MAX_ATTEMPTS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return defaultS3RetryMaxAttempts
}

func withS3Retry(opts []func(*awsconfig.LoadOptions) error, cfg map[string]any) []func(*awsconfig.LoadOptions) error {
	if n := s3RetryMaxAttempts(cfg); n > 0 {
		opts = append(opts, awsconfig.WithRetryMaxAttempts(n))
	}
	return opts
}

func withS3LegacyRetryer(opts []func(*awsconfig.LoadOptions) error, cfg map[string]any) []func(*awsconfig.LoadOptions) error {
	attempts := s3RetryMaxAttempts(cfg)
	if attempts <= 0 {
		attempts = retry.DefaultMaxAttempts
	}
	return append(opts, awsconfig.WithRetryer(awsretry.Provider(attempts)))
}

func newS3FromConfig(cfg map[string]any) (*Client, error) {
	bucketName, _ := cfg["bucket_name"].(string)
	if bucketName == "" {
		bucketName = ResolveBucket("")
	}
	if bucketName == "" {
		return nil, fmt.Errorf("s3: bucket_name is required (or set OBJECT_STORAGE_BUCKET_NAME)")
	}

	region, _ := cfg["region"].(string)
	if region == "" {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
	}

	endpointURL, _ := cfg["endpoint_url"].(string)
	if endpointURL == "" {
		endpointURL = os.Getenv("S3_ENDPOINT_URL")
	}

	accessKey, _ := cfg["aws_access_key_id"].(string)
	if accessKey == "" {
		accessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}

	secretKey, _ := cfg["aws_secret_access_key"].(string)
	if secretKey == "" {
		secretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}

	sessionToken, _ := cfg["aws_session_token"].(string)
	if sessionToken == "" {
		sessionToken = os.Getenv("AWS_SESSION_TOKEN")
	}

	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region), awsconfig.WithHTTPClient(tunedS3HTTPClient()))
	opts = withS3Retry(opts, cfg)
	opts = withS3LegacyRetryer(opts, cfg)
	if accessKey != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if endpointURL != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = &endpointURL
			o.UsePathStyle = true
		})
	}

	return &Client{
		storageType: "s3",
		bucketName:  bucketName,
		s3Client:    s3.NewFromConfig(awsCfg, s3Opts...),
	}, nil
}

func newS3FromEnv() (*Client, error) {
	bucketName := ResolveBucket("")
	if bucketName == "" {
		return nil, fmt.Errorf("s3: OBJECT_STORAGE_BUCKET_NAME required")
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	endpointURL := os.Getenv("S3_ENDPOINT_URL")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region), awsconfig.WithHTTPClient(tunedS3HTTPClient()))
	opts = withS3Retry(opts, nil)
	opts = withS3LegacyRetryer(opts, nil)
	if accessKey != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if endpointURL != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = &endpointURL
			o.UsePathStyle = true
		})
	}

	return &Client{
		storageType: "s3",
		bucketName:  bucketName,
		s3Client:    s3.NewFromConfig(awsCfg, s3Opts...),
	}, nil
}
