// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/auth"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/db"
)

const (
	neptuneSigningService   = "neptune-db"
	neptuneEmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	neptuneTokenRefresh     = 4 * time.Minute
)

type neptuneAuthTokenManager struct {
	region   string
	endpoint string
	creds    aws.CredentialsProvider

	mu          sync.Mutex
	cachedToken neo4j.AuthToken
	signedAt    time.Time
}

var _ auth.TokenManager = (*neptuneAuthTokenManager)(nil)

func newNeptuneAuthTokenManager(region, endpoint string, creds aws.CredentialsProvider) *neptuneAuthTokenManager {
	return &neptuneAuthTokenManager{region: region, endpoint: endpoint, creds: creds}
}

func (m *neptuneAuthTokenManager) GetAuthToken(ctx context.Context) (neo4j.AuthToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cachedToken.Tokens != nil && time.Since(m.signedAt) < neptuneTokenRefresh {
		return m.cachedToken, nil
	}

	token, err := m.sign(ctx)
	if err != nil {
		return neo4j.AuthToken{}, err
	}
	m.cachedToken = token
	m.signedAt = time.Now()
	return token, nil
}

func (m *neptuneAuthTokenManager) HandleSecurityException(_ context.Context, token neo4j.AuthToken, _ *db.Neo4jError) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reflect.DeepEqual(m.cachedToken.Tokens, token.Tokens) {
		m.cachedToken = neo4j.AuthToken{}
		m.signedAt = time.Time{}
	}
	return true, nil
}

func (m *neptuneAuthTokenManager) sign(ctx context.Context) (neo4j.AuthToken, error) {
	req, err := http.NewRequest(http.MethodGet, "https://"+m.endpoint+"/opencypher", nil)
	if err != nil {
		return neo4j.AuthToken{}, fmt.Errorf("build neptune auth request: %w", err)
	}
	req.Host = m.endpoint

	creds, err := m.creds.Retrieve(ctx)
	if err != nil {
		return neo4j.AuthToken{}, fmt.Errorf("retrieve aws credentials: %w", err)
	}

	if err = v4.NewSigner().SignHTTP(ctx, creds, req, neptuneEmptyPayloadHash, neptuneSigningService, m.region, time.Now().UTC()); err != nil {
		return neo4j.AuthToken{}, fmt.Errorf("sigv4 sign neptune request: %w", err)
	}

	authData := map[string]string{
		"Authorization": req.Header.Get("Authorization"),
		"X-Amz-Date":    req.Header.Get("X-Amz-Date"),
		"Host":          m.endpoint,
		"HttpMethod":    req.Method,
	}
	if token := req.Header.Get("X-Amz-Security-Token"); token != "" {
		authData["X-Amz-Security-Token"] = token
	}

	authJSON, err := json.Marshal(authData)
	if err != nil {
		return neo4j.AuthToken{}, fmt.Errorf("marshal neptune auth: %w", err)
	}

	return neo4j.BasicAuth("username", string(authJSON), ""), nil
}

func neptuneEndpointFromConnStr(connStr string) (string, error) {
	u, err := url.Parse(connStr)
	if err != nil {
		return "", fmt.Errorf("parse connection_string: %w", unwrapURLError(err))
	}
	if u.Host == "" {
		return "", errors.New("connection_string has no host")
	}
	return u.Host, nil
}

func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}
