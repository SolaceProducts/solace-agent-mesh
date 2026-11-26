// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

const (
	probeEnvWebIdentityTokenFile    = "AWS_WEB_IDENTITY_TOKEN_FILE"
	probeEnvContainerAuthzTokenFile = "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"
	probeEnvAWSAccessKeyID          = "AWS_ACCESS_KEY_ID"
	probeEnvAWSSecretAccessKey      = "AWS_SECRET_ACCESS_KEY"
)

func probeIRSAOrPodIdentityPresent() bool {
	return os.Getenv(probeEnvWebIdentityTokenFile) != "" || os.Getenv(probeEnvContainerAuthzTokenFile) != ""
}

func probeMaskStaticAWSEnvForIRSA() func() {
	savedKey := os.Getenv(probeEnvAWSAccessKeyID)
	savedSecret := os.Getenv(probeEnvAWSSecretAccessKey)
	_ = os.Unsetenv(probeEnvAWSAccessKeyID)
	_ = os.Unsetenv(probeEnvAWSSecretAccessKey)
	return func() {
		if savedKey != "" {
			_ = os.Setenv(probeEnvAWSAccessKeyID, savedKey)
		}
		if savedSecret != "" {
			_ = os.Setenv(probeEnvAWSSecretAccessKey, savedSecret)
		}
	}
}

const bedrockKBProbeTimeout = 10 * time.Second

const bedrockKBProbeDialTimeout = 3 * time.Second

const bedrockKBProbeFailureMessage = "Failed to connect to Bedrock Knowledge Base. Please verify your connector configuration."

const bedrockKBProbeUnreachableMessage = "Could not reach Amazon Bedrock to validate the connector. Verify network connectivity to the Bedrock service endpoint for the configured region."

type bedrockKBProbeClient interface {
	GetKnowledgeBase(ctx context.Context, in *bedrockagent.GetKnowledgeBaseInput, opts ...func(*bedrockagent.Options)) (*bedrockagent.GetKnowledgeBaseOutput, error)
}

var bedrockKBProbeClientBuilder = newBedrockKBProbeClient

func init() {
	registerConnectorProbe("knowledge_base", "bedrock", probeBedrockKB)
}

func probeBedrockKB(ctx context.Context, logger *slog.Logger, values map[string]any, _ bool) ([]string, error) {
	kbID := stringFromValues(values, "kb_id")
	if kbID == "" || isPlaceholder(kbID) {
		return nil, nil
	}
	region := stringFromValues(values, "region")
	if isPlaceholder(region) {
		return nil, nil
	}
	if region == "" {
		region = "us-east-1"
	}

	auth, skip := bedrockKBProbeAuthFromValues(values)
	if skip {
		return nil, nil
	}

	if auth.authType == "iam" && auth.externalID == "" {
		if logger != nil {
			logger.Info("bedrock-kb connector probe: external_id is empty on iam auth_type; AWS recommends ExternalId for cross-account AssumeRole to avoid confused-deputy attacks",
				"kbId", kbID, "region", region, "roleArn", auth.roleARN)
		}
	}

	pctx, cancel := context.WithTimeout(ctx, bedrockKBProbeTimeout)
	defer cancel()

	client, err := bedrockKBProbeClientBuilder(pctx, region, auth)
	if err != nil {
		probeWarn(logger, "bedrock-kb connector probe: AWS client build failed",
			"kbId", kbID, "region", region, "authType", auth.authType, "error", err)
		return nil, &ValidationError{Message: bedrockKBProbeMessageForError(err)}
	}

	if _, err := client.GetKnowledgeBase(pctx, &bedrockagent.GetKnowledgeBaseInput{
		KnowledgeBaseId: &kbID,
	}); err != nil {
		probeWarn(logger, "bedrock-kb connector probe: GetKnowledgeBase failed",
			"kbId", kbID, "region", region, "authType", auth.authType, "error", err)
		return nil, &ValidationError{Message: bedrockKBProbeMessageForError(err)}
	}
	return nil, nil
}

func bedrockKBProbeMessageForError(err error) string {
	if bedrockKBProbeUnreachable(err) {
		return bedrockKBProbeUnreachableMessage
	}
	return bedrockKBProbeFailureMessage
}

func bedrockKBProbeUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var sendErr *smithyhttp.RequestSendError
	if errors.As(err, &sendErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func probeWarn(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Warn(msg, args...)
}

type bedrockKBProbeAuth struct {
	authType        string
	accessKeyID     string
	secretAccessKey string
	roleARN         string
	externalID      string
	sessionName     string
}

func bedrockKBProbeAuthFromValues(values map[string]any) (bedrockKBProbeAuth, bool) {
	authType := stringFromValues(values, "auth_type")
	if authType == "" {
		authType = "access_key"
	}
	auth := bedrockKBProbeAuth{
		authType:    authType,
		externalID:  stringFromValues(values, "external_id"),
		sessionName: stringFromValues(values, "session_name"),
	}

	switch authType {
	case "access_key":
		auth.accessKeyID = stringFromValues(values, "aws_access_key_id")
		auth.secretAccessKey = stringFromValues(values, "aws_secret_access_key")
		if auth.accessKeyID == "" || auth.secretAccessKey == "" {
			return auth, true
		}
		if isPlaceholder(auth.accessKeyID) || isPlaceholder(auth.secretAccessKey) {
			return auth, true
		}
	case "iam":
		accountID := stringFromValues(values, "aws_account_id")
		roleName := stringFromValues(values, "role_name")
		if accountID == "" || roleName == "" {
			return auth, true
		}
		if isPlaceholder(accountID) || isPlaceholder(roleName) {
			return auth, true
		}
		auth.roleARN = fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
	default:
		return auth, true
	}
	return auth, false
}

func stringFromValues(values map[string]any, key string) string {
	v, _ := values[key].(string)
	return v
}

func newBedrockKBProbeClient(ctx context.Context, region string, auth bedrockKBProbeAuth) (bedrockKBProbeClient, error) {
	cfg, err := loadBedrockKBProbeAWSConfig(ctx, region, auth)
	if err != nil {
		return nil, err
	}
	return bedrockagent.NewFromConfig(cfg), nil
}

func bedrockKBProbeHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: bedrockKBProbeDialTimeout}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   bedrockKBProbeDialTimeout,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func loadBedrockKBProbeAWSConfig(ctx context.Context, region string, auth bedrockKBProbeAuth) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		awsconfig.WithHTTPClient(bedrockKBProbeHTTPClient()),
	}

	hasStaticCreds := auth.accessKeyID != "" && auth.secretAccessKey != ""
	switch {
	case hasStaticCreds:
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(auth.accessKeyID, auth.secretAccessKey, ""),
		))
	case probeIRSAOrPodIdentityPresent():
		restore := probeMaskStaticAWSEnvForIRSA()
		defer restore()
	default:
		opts = append(opts, awsconfig.WithCredentialsProvider(ec2rolecreds.New()))
	}

	baseCfg, err := awsretry.Load(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	if auth.roleARN == "" {
		return baseCfg, nil
	}

	sessionName := auth.sessionName
	if sessionName == "" {
		sessionName = "solace-bedrock-kb-probe"
	}
	stsClient := sts.NewFromConfig(baseCfg)
	input := &sts.AssumeRoleInput{
		RoleArn:         &auth.roleARN,
		RoleSessionName: &sessionName,
	}
	if auth.externalID != "" {
		input.ExternalId = &auth.externalID
	}
	result, err := stsClient.AssumeRole(ctx, input)
	if err != nil {
		return aws.Config{}, fmt.Errorf("assume role %s: %w", auth.roleARN, err)
	}
	creds := result.Credentials
	cfg, err := awsretry.Load(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				*creds.AccessKeyId,
				*creds.SecretAccessKey,
				*creds.SessionToken,
			),
		),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config after assume role: %w", err)
	}
	return cfg, nil
}
