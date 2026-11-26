// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/golang-jwt/jwt/v5"
	"github.com/maximhq/bifrost/core/providers/azure"
	"github.com/maximhq/bifrost/core/schemas"
)

type AzureWorkloadIdentity struct {
	ClientID string
	TenantID string
}

const AzureWorkloadIdentityKey = "azure_workload_identity"

var azureCredentialKeys = []string{"api_key", "client_id", "client_secret", "tenant_id", "token_url"}

func IsAzureCredentialKey(key string) bool {
	return slices.Contains(azureCredentialKeys, strings.TrimSuffix(key, "_file")) || strings.HasPrefix(key, "oauth_")
}

const AzureFederatedTokenFileEnv = "AZURE_FEDERATED_TOKEN_FILE"

const AzureWorkloadIdentityEnabledEnv = "SAM_AZURE_WORKLOAD_IDENTITY_ENABLED"

func AzureWorkloadIdentityAvailable() bool {
	enabled, _ := strconv.ParseBool(os.Getenv(AzureWorkloadIdentityEnabledEnv))
	return enabled && os.Getenv(AzureFederatedTokenFileEnv) != ""
}

type AzureWorkloadIdentitySubject struct {
	Issuer         string
	Namespace      string
	ServiceAccount string
	Audiences      []string
}

const serviceAccountSubjectPrefix = "system:serviceaccount:"

func ReadAzureWorkloadIdentitySubject() (AzureWorkloadIdentitySubject, error) {
	token, err := os.ReadFile(os.Getenv(AzureFederatedTokenFileEnv))
	if err != nil {
		return AzureWorkloadIdentitySubject{}, fmt.Errorf("read service account token: %w", err)
	}
	claims, err := serviceAccountClaims(strings.TrimSpace(string(token)))
	if err != nil {
		return AzureWorkloadIdentitySubject{}, err
	}
	namespace, serviceAccount, ok := strings.Cut(strings.TrimPrefix(claims.Subject, serviceAccountSubjectPrefix), ":")
	if !strings.HasPrefix(claims.Subject, serviceAccountSubjectPrefix) || !ok || namespace == "" || serviceAccount == "" {
		return AzureWorkloadIdentitySubject{}, fmt.Errorf("service account token subject %q is not a Kubernetes service account", claims.Subject)
	}
	if claims.Issuer == "" || len(claims.Audience) == 0 {
		return AzureWorkloadIdentitySubject{}, errors.New("service account token has no issuer or audience")
	}
	return AzureWorkloadIdentitySubject{
		Issuer:         claims.Issuer,
		Namespace:      namespace,
		ServiceAccount: serviceAccount,
		Audiences:      claims.Audience,
	}, nil
}

func serviceAccountClaims(token string) (jwt.RegisteredClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwt.RegisteredClaims{}, errors.New("service account token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwt.RegisteredClaims{}, fmt.Errorf("decode service account token: %w", err)
	}
	var claims jwt.RegisteredClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwt.RegisteredClaims{}, fmt.Errorf("parse service account token claims: %w", err)
	}
	return claims, nil
}

var azureCredentials = struct {
	mu   sync.Mutex
	byID map[AzureWorkloadIdentity]azcore.TokenCredential
}{byID: map[AzureWorkloadIdentity]azcore.TokenCredential{}}

func azureCredentialFor(id AzureWorkloadIdentity) (azcore.TokenCredential, error) {
	azureCredentials.mu.Lock()
	defer azureCredentials.mu.Unlock()
	if cred, ok := azureCredentials.byID[id]; ok {
		return cred, nil
	}
	if !AzureWorkloadIdentityAvailable() {
		return nil, fmt.Errorf("azure workload identity is not available here: it needs %s=true and %s set", AzureWorkloadIdentityEnabledEnv, AzureFederatedTokenFileEnv)
	}
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientID:      id.ClientID,
		TenantID:      id.TenantID,
		TokenFilePath: os.Getenv(AzureFederatedTokenFileEnv),
	})
	if err != nil {
		return nil, fmt.Errorf("azure workload identity: %w", err)
	}
	azureCredentials.byID[id] = cred
	return cred, nil
}

const azureFoundryScope = "https://ai.azure.com/.default"

const azureFoundryHostSuffix = ".services.ai.azure.com"

func isAzureFoundryHost(host string) bool {
	return strings.HasSuffix(host, azureFoundryHostSuffix)
}

func azureTokenScope(endpoint string) string {
	if isAzureFoundryHost(baseURLHost(endpoint)) {
		return azureFoundryScope
	}
	return azure.DefaultAzureScope
}

func (c *BifrostClient) bifrostContext(ctx context.Context) (*schemas.BifrostContext, *schemas.BifrostError) {
	if c.azureCredential == nil {
		return toBifrostContext(ctx), nil
	}
	token, err := c.azureCredential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureTokenScope(AzureEndpoint(c.cfg))}})
	if err != nil {
		return nil, azureSignInError(err)
	}
	bfCtx := toBifrostContext(ctx)
	bfCtx.SetValue(azure.AzureAuthorizationTokenKey, token.Token)
	return bfCtx, nil
}
