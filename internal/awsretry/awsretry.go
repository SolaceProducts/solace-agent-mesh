// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awsretry

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

const (
	legacyRetryCost        uint = 5
	legacyRetryTimeoutCost uint = 10
)

type legacyRetryer struct {
	*retry.Standard

	limiter  *ratelimit.TokenRateLimit
	timeouts retry.IsErrorTimeouts
}

func Legacy(maxAttempts int) aws.Retryer {
	limiter := ratelimit.NewTokenRateLimit(retry.DefaultRetryRateTokens)
	return &legacyRetryer{
		Standard: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = maxAttempts
			o.Backoff = retry.NewExponentialJitterBackoff(retry.DefaultMaxBackoff)
			o.RateLimiter = limiter
			o.RetryCost = legacyRetryCost
			o.NoRetryIncrement = retry.DefaultNoRetryIncrement
		}),
		limiter:  limiter,
		timeouts: retry.IsErrorTimeouts(retry.DefaultTimeouts),
	}
}

func Provider(maxAttempts int) func() aws.Retryer {
	return func() aws.Retryer { return Legacy(maxAttempts) }
}

func IsLegacy(r aws.Retryer) bool {
	_, ok := r.(*legacyRetryer)
	return ok
}

func Apply(cfg aws.Config) aws.Config {
	if cfg.Retryer != nil || cfg.RetryMode == aws.RetryModeAdaptive {
		return cfg
	}
	attempts := cfg.RetryMaxAttempts
	if attempts <= 0 {
		attempts = retry.DefaultMaxAttempts
	}
	cfg.Retryer = Provider(attempts)
	cfg.RetryMaxAttempts = 0
	return cfg
}

func Load(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return aws.Config{}, err
	}
	return Apply(cfg), nil
}

func (r *legacyRetryer) GetAttemptToken(context.Context) (func(error) error, error) {
	return func(attemptErr error) error {
		if attemptErr != nil {
			return nil
		}
		return r.limiter.AddTokens(retry.DefaultNoRetryIncrement)
	}, nil
}

func (r *legacyRetryer) GetRetryToken(ctx context.Context, opErr error) (func(error) error, error) {
	cost := legacyRetryCost
	if r.timeouts.IsErrorTimeout(opErr).Bool() {
		cost = legacyRetryTimeoutCost
	}
	release, err := r.limiter.GetToken(ctx, cost)
	if err != nil {
		return nil, fmt.Errorf("failed to get rate limit token, %w", err)
	}
	return func(attemptErr error) error {
		if attemptErr != nil {
			return nil
		}
		return release()
	}, nil
}
