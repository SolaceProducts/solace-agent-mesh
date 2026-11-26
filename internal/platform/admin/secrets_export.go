// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"slices"
)

func ConnectorSecretsDiffer(ctx context.Context, svc *ConnectorService, id string, candidate map[string]any) (bool, error) {
	connType, subtype, stored, err := svc.GetValuesCleartext(ctx, id)
	if err != nil {
		return false, err
	}
	fields := registryToSecretFields(getSecretRegistry().ConnectorSecretFields(connType, subtype))
	return anyDiffers(compareSecrets(fields, candidate, stored)), nil
}

func GatewaySecretsDiffer(ctx context.Context, svc *GatewayService, id string, candidate map[string]any) (bool, error) {
	gwType, stored, err := svc.GetValuesCleartext(ctx, id)
	if err != nil {
		return false, err
	}
	fields := registryToSecretFields(getSecretRegistry().GatewaySecretFields(gwType))
	return anyDiffers(compareSecrets(fields, candidate, stored)), nil
}

func anyDiffers(result map[string]SecretFieldDiffResult) bool {
	for _, r := range result {
		if !r.Matches {
			return true
		}
	}
	return false
}

func ConnectorSecretFieldNames(connType, subtype string) []string {
	return secretFieldNames(getSecretRegistry().ConnectorSecretFields(connType, subtype))
}

func GatewaySecretFieldNames(gwType string) []string {
	return secretFieldNames(getSecretRegistry().GatewaySecretFields(gwType))
}

func secretFieldNames(reg map[string]secretFieldInfo) []string {
	out := make([]string, 0, len(reg))
	for name := range reg {
		out = append(out, name)
	}
	return out
}

func IsSecretPlaceholder(v string) bool {
	return isPlaceholder(v)
}

func A2AAuthTypes() []string {
	return []string{"none", authTypeBearer, authTypeAPIKey, authTypeBasic, authTypeOAuth2}
}

func IsKnownA2AAuthType(authType string) bool {
	return slices.Contains(A2AAuthTypes(), authType)
}

func A2AAuthSecretWireFields(authType string) []string {
	pairs := authSecretPairs(authType)
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.wire)
	}
	return out
}

func IsRedactedSecret(v string) bool {
	return v == redactedSentinel
}
