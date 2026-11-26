// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
	"golang.org/x/sync/singleflight"
)

type TokenRefresher interface {
	ForceRefresh(ctx context.Context, t target.Target) (string, error)
}

type Client struct {
	resolved   target.Resolved
	http       *http.Client
	deployHTTP *http.Client
	target     target.Target
	refresher  TokenRefresher
	skew       *SchemaSkewSink
	v2Probe    singleflight.Group

	v2 struct {
		mu      sync.Mutex
		known   bool
		refusal error
	}

	servedServiceAccounts struct {
		mu   sync.Mutex
		path *platformResource
	}
}

const (
	requestTimeout       = 30 * time.Second
	deployRequestTimeout = 5 * time.Minute
)

func (c *Client) SetSchemaSkewSink(s *SchemaSkewSink) { c.skew = s }

func NewClient(r target.Resolved) *Client {
	return &Client{
		resolved:   r,
		http:       &http.Client{Timeout: requestTimeout},
		deployHTTP: &http.Client{Timeout: deployRequestTimeout},
	}
}

func NewClientWithAutoRefresh(r target.Resolved, t target.Target, refresher TokenRefresher) *Client {
	c := NewClient(r)
	c.target = t
	c.refresher = refresher
	return c
}

func (c *Client) TargetURL() string {
	return c.resolved.URL
}

type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

func (c *Client) setClientHeaders(req *http.Request) {
	if c.resolved.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.resolved.Token)
	}
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	httputil.SetAPIVersion(req)
	if session := clianalytics.SessionID(); session != "" {
		req.Header.Set(httputil.HeaderClientSession, session)
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if needsV2Probe(path) {
		if err := c.requireV2(ctx); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.resolved.URL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	c.setClientHeaders(req)
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, into any) error {
	return c.doWith(ctx, c.http, method, path, body, into)
}

func (c *Client) doDeploy(ctx context.Context, path string, body, into any) error {
	return c.doWith(ctx, c.deployHTTP, http.MethodPost, path, body, into)
}

func (c *Client) doWith(ctx context.Context, hc *http.Client, method, path string, body, into any) error {
	respBody, err := c.send(ctx, hc, method, path, body)
	if err != nil {
		return err
	}
	return c.decode(method, path, respBody, into)
}

func (c *Client) send(ctx context.Context, hc *http.Client, method, path string, body any) ([]byte, error) {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal %s %s: %w", method, path, err)
		}
	}

	attempt := func() (int, []byte, error) {
		var reader io.Reader
		if raw != nil {
			reader = bytes.NewReader(raw)
		}
		req, err := c.newRequest(ctx, method, path, reader)
		if err != nil {
			return 0, nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		httputil.CLINotices.Record(resp)
		respBody, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, respBody, nil
	}

	status, respBody, err := attempt()
	if err != nil {
		return nil, err
	}

	if status == http.StatusUnauthorized && c.refresher != nil && c.resolved.Token != "" {
		if newTok, refErr := c.refresher.ForceRefresh(ctx, c.target); refErr == nil && newTok != "" {
			c.resolved.Token = newTok
			status, respBody, err = attempt()
			if err != nil {
				return nil, err
			}
		}
	}

	if status >= 400 {
		return nil, &HTTPError{Method: method, Path: path, StatusCode: status, Body: string(respBody)}
	}
	return respBody, nil
}

func (c *Client) decode(method, path string, respBody []byte, into any) error {
	if into == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, into); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	if c.skew != nil {
		c.skew.record(pathWithoutQuery(path), detectUnknownKeys(respBody, into))
	}
	return nil
}

func pathWithoutQuery(path string) string {
	before, _, _ := strings.Cut(path, "?")
	return before
}
