// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/db"
)

const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testEndpoint  = "my-cluster.us-east-1.neptune.amazonaws.com:8182"
	testRegion    = "us-east-1"
)

func extractAuthJSON(t *testing.T, token neo4j.AuthToken) map[string]string {
	t.Helper()
	credRaw, ok := token.Tokens["credentials"]
	if !ok {
		t.Fatal("AuthToken.Tokens missing 'credentials' key")
	}
	credStr, ok := credRaw.(string)
	if !ok {
		t.Fatalf("credentials is %T, want string", credRaw)
	}
	var authData map[string]string
	if err := json.Unmarshal([]byte(credStr), &authData); err != nil {
		t.Fatalf("unmarshal credentials JSON: %v", err)
	}
	return authData
}

func TestNeptuneAuthToken_SignsRequest(t *testing.T) {
	creds := credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, "")
	mgr := newNeptuneAuthTokenManager(testRegion, testEndpoint, creds)

	token, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}

	authData := extractAuthJSON(t, token)

	authz := authData["Authorization"]
	if authz == "" {
		t.Fatal("Authorization field is empty")
	}
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization = %q, want prefix AWS4-HMAC-SHA256", authz)
	}
	if !strings.Contains(authz, "Credential="+testAccessKey+"/") {
		t.Errorf("Authorization missing Credential=%s/: %q", testAccessKey, authz)
	}
	if !strings.Contains(authz, "SignedHeaders=") {
		t.Errorf("Authorization missing SignedHeaders=: %q", authz)
	}
	if !strings.Contains(authz, "Signature=") {
		t.Errorf("Authorization missing Signature=: %q", authz)
	}

	if authData["X-Amz-Date"] == "" {
		t.Error("X-Amz-Date is empty")
	}
	if authData["Host"] != testEndpoint {
		t.Errorf("Host = %q, want %q", authData["Host"], testEndpoint)
	}
	if _, present := authData["X-Amz-Security-Token"]; present {
		t.Error("X-Amz-Security-Token should be absent when no session token is set")
	}
}

func TestNeptuneAuthToken_SignsRequest_WithSessionToken(t *testing.T) {
	const sessionToken = "AQoDYXdzEJr//EXAMPLE"
	creds := credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, sessionToken)
	mgr := newNeptuneAuthTokenManager(testRegion, testEndpoint, creds)

	token, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}

	authData := extractAuthJSON(t, token)
	if authData["X-Amz-Security-Token"] != sessionToken {
		t.Errorf("X-Amz-Security-Token = %q, want %q", authData["X-Amz-Security-Token"], sessionToken)
	}
}

func TestNeptuneAuthToken_Caches(t *testing.T) {
	creds := credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, "")
	mgr := newNeptuneAuthTokenManager(testRegion, testEndpoint, creds)

	first, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("first GetAuthToken: %v", err)
	}
	second, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("second GetAuthToken: %v", err)
	}

	firstCreds, ok1 := first.Tokens["credentials"].(string)
	secondCreds, ok2 := second.Tokens["credentials"].(string)
	if !ok1 || !ok2 {
		t.Fatal("credentials not a string in one or both tokens")
	}
	if firstCreds != secondCreds {
		t.Error("two GetAuthToken calls within the refresh window returned different credentials; expected cache hit")
	}
}

type countingCredentialsProvider struct {
	inner aws.CredentialsProvider
	calls atomic.Int64
}

func (c *countingCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	c.calls.Add(1)
	return c.inner.Retrieve(ctx)
}

func TestNeptuneAuthToken_HandleSecurityExceptionInvalidates(t *testing.T) {
	inner := credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, "")
	counting := &countingCredentialsProvider{inner: inner}
	mgr := newNeptuneAuthTokenManager(testRegion, testEndpoint, counting)

	first, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("first GetAuthToken: %v", err)
	}
	callsAfterFirst := counting.calls.Load()

	retry, err := mgr.HandleSecurityException(t.Context(), first, &db.Neo4jError{Code: "Neo.ClientError.Security.Unauthorized"})
	if err != nil {
		t.Fatalf("HandleSecurityException: %v", err)
	}
	if !retry {
		t.Error("HandleSecurityException returned false; driver expects true to retry")
	}

	_, err = mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("second GetAuthToken: %v", err)
	}
	callsAfterSecond := counting.calls.Load()

	if callsAfterSecond <= callsAfterFirst {
		t.Errorf("expected a re-sign after HandleSecurityException: Retrieve called %d times before, %d after",
			callsAfterFirst, callsAfterSecond)
	}
}

func TestNeptuneAuthToken_HandleSecurityException_DoesNotInvalidateMismatch(t *testing.T) {
	inner := credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, "")
	counting := &countingCredentialsProvider{inner: inner}
	mgr := newNeptuneAuthTokenManager(testRegion, testEndpoint, counting)

	cached, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	callsAfterFirst := counting.calls.Load()

	stale := neo4j.BasicAuth("username", `{"stale":"token"}`, "")
	_, err = mgr.HandleSecurityException(t.Context(), stale, &db.Neo4jError{Code: "Neo.ClientError.Security.Unauthorized"})
	if err != nil {
		t.Fatalf("HandleSecurityException: %v", err)
	}

	hit, err := mgr.GetAuthToken(t.Context())
	if err != nil {
		t.Fatalf("GetAuthToken after mismatch exception: %v", err)
	}
	if counting.calls.Load() != callsAfterFirst {
		t.Error("cache was incorrectly invalidated by a mismatched token")
	}

	hitCreds, _ := hit.Tokens["credentials"].(string)
	cachedCreds, _ := cached.Tokens["credentials"].(string)
	if hitCreds != cachedCreds {
		t.Error("token changed after a mismatched HandleSecurityException; cache should still be valid")
	}
}
