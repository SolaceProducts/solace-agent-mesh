// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

type awsAuth struct {
	region          string
	accessKeyID     string
	secretAccessKey string
	roleARN         string
	externalID      string
	sessionName     string
}

func authFromConfig(config map[string]any) awsAuth {
	return awsAuth{
		region:          configString(config, "region", "us-east-1"),
		accessKeyID:     configString(config, "aws_access_key_id", ""),
		secretAccessKey: configString(config, "aws_secret_access_key", ""),
		roleARN:         configString(config, "role_arn", ""),
		externalID:      configString(config, "external_id", ""),
		sessionName:     configString(config, "session_name", ""),
	}
}

func loadAWSConfig(ctx context.Context, auth awsAuth) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error

	if auth.region != "" {
		opts = append(opts, awsconfig.WithRegion(auth.region))
	}

	if auth.accessKeyID != "" && auth.secretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(auth.accessKeyID, auth.secretAccessKey, ""),
		))
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
		sessionName = "solace-graph-db-session"
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
