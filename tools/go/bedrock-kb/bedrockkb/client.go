// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bedrockkb

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

func maskStaticAWSEnvForIRSA() func() {
	if !irsaOrPodIdentityPresent() {
		return func() {}
	}

	savedKey := os.Getenv(envAWSAccessKeyID)
	savedSecret := os.Getenv(envAWSSecretAccessKey)
	_ = os.Unsetenv(envAWSAccessKeyID)
	_ = os.Unsetenv(envAWSSecretAccessKey)

	return func() {
		if savedKey != "" {
			_ = os.Setenv(envAWSAccessKeyID, savedKey)
		}
		if savedSecret != "" {
			_ = os.Setenv(envAWSSecretAccessKey, savedSecret)
		}
	}
}

func irsaOrPodIdentityPresent() bool {
	return os.Getenv(envWebIdentityTokenFile) != "" || os.Getenv(envContainerAuthzTokenFile) != ""
}

const (
	envWebIdentityTokenFile    = "AWS_WEB_IDENTITY_TOKEN_FILE"
	envContainerAuthzTokenFile = "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"
	envAWSAccessKeyID          = "AWS_ACCESS_KEY_ID"
	envAWSSecretAccessKey      = "AWS_SECRET_ACCESS_KEY"
)

type bedrockClients struct {
	runtime *bedrockagentruntime.Client
	agent   *bedrockagent.Client
}

type awsAuth struct {
	region          string
	accessKeyID     string
	secretAccessKey string
	roleARN         string
	externalID      string
	sessionName     string
}

func newBedrockClients(ctx context.Context, auth awsAuth) (*bedrockClients, error) {
	cfg, err := loadAWSConfig(ctx, auth)
	if err != nil {
		return nil, err
	}
	return &bedrockClients{
		runtime: bedrockagentruntime.NewFromConfig(cfg),
		agent:   bedrockagent.NewFromConfig(cfg),
	}, nil
}

func newRuntimeClient(ctx context.Context, auth awsAuth) (*bedrockagentruntime.Client, error) {
	cfg, err := loadAWSConfig(ctx, auth)
	if err != nil {
		return nil, err
	}
	return bedrockagentruntime.NewFromConfig(cfg), nil
}

func loadAWSConfig(ctx context.Context, auth awsAuth) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error

	if auth.region != "" {
		opts = append(opts, awsconfig.WithRegion(auth.region))
	}

	hasStaticConfigCreds := auth.accessKeyID != "" && auth.secretAccessKey != ""
	switch {
	case hasStaticConfigCreds:
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(auth.accessKeyID, auth.secretAccessKey, ""),
		))
	case irsaOrPodIdentityPresent():
		restore := maskStaticAWSEnvForIRSA()
		defer restore()
	default:
		opts = append(opts, awsconfig.WithCredentialsProvider(ec2rolecreds.New()))
	}

	baseCfg, err := awsretry.Load(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}

	if auth.roleARN != "" {
		return assumeRoleConfig(ctx, baseCfg, auth)
	}

	return baseCfg, nil
}

func assumeRoleConfig(ctx context.Context, baseCfg aws.Config, auth awsAuth) (aws.Config, error) {
	stsClient := sts.NewFromConfig(baseCfg)

	sessionName := auth.sessionName
	if sessionName == "" {
		sessionName = "solace-bedrock-kb-session"
	}

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
	return awsretry.Load(ctx,
		awsconfig.WithRegion(auth.region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				*creds.AccessKeyId,
				*creds.SecretAccessKey,
				*creds.SessionToken,
			),
		),
	)
}
