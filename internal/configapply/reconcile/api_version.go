// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const APIV2ProbePath = platform.APIV2Prefix + "/agents"

const APIV2ProbeQuery = "?pageSize=1"

const v2ProbeKey = "v2"

const minV2PlatformRelease = "2.583.0"

const upgradeToThisCLIRelease = "upgrade Agent Mesh to the release of this sam CLI or later"

var errV2ResponseWithoutData = errors.New("response has no data member")

type NoV2Error struct {
	URL            string
	Version        string
	RefusesAPIKeys bool
}

func (e *NoV2Error) Error() string {
	upgrade := fmt.Sprintf("upgrade Agent Mesh to %s or later, or use a sam CLI of the server's own release", minV2PlatformRelease)
	switch {
	case e.Version != "":
		return fmt.Sprintf("the platform at %s runs Agent Mesh %s, older than the %s this sam CLI needs\n  %s",
			urlOrigin(e.URL), e.Version, minV2PlatformRelease, upgrade)
	case e.RefusesAPIKeys:
		return fmt.Sprintf("the platform at %s refuses API keys on /api/v2, which this sam CLI needs\n  %s",
			urlOrigin(e.URL), upgrade)
	}
	return fmt.Sprintf("the platform at %s does not serve /api/v2, which this sam CLI needs\n  %s",
		urlOrigin(e.URL), upgrade)
}

func urlOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the configured URL"
	}
	return u.Scheme + "://" + u.Host
}

type platformResource struct {
	path string
}

func (r platformResource) apiPath() string {
	return platform.APIV2Prefix + r.path
}

func (c *Client) callResource(ctx context.Context, r platformResource, method, subPath string, body, into any) error {
	return c.doSingle(ctx, method, r.apiPath()+subPath, body, into)
}

func listResource[T any](ctx context.Context, c *Client, r platformResource) ([]T, error) {
	return listAllPages[T](ctx, c, r.apiPath())
}

func listCustom[T any](ctx context.Context, c *Client, r platformResource) ([]T, error) {
	return listAllPages[T](ctx, c, r.apiPath()+"?origin=custom")
}

func (c *Client) requireV2(ctx context.Context) error {
	if known, refusal := c.knownV2(); known {
		return refusal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result := c.v2Probe.DoChan(v2ProbeKey, func() (any, error) {
		return nil, c.probeV2(context.WithoutCancel(ctx))
	})
	select {
	case r := <-result:
		return r.Err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) knownV2() (known bool, refusal error) {
	c.v2.mu.Lock()
	defer c.v2.mu.Unlock()
	return c.v2.known, c.v2.refusal
}

func (c *Client) probeV2(ctx context.Context) error {
	if known, refusal := c.knownV2(); known {
		return refusal
	}
	if release, older := c.releaseBeforeV2(ctx); older {
		return c.recordV2(&NoV2Error{URL: c.resolved.URL, Version: release}, 0, release)
	}
	err := c.do(ctx, http.MethodGet, APIV2ProbePath+APIV2ProbeQuery, nil, nil)
	var httpErr *HTTPError
	switch {
	case err == nil:
		return c.recordV2(nil, http.StatusOK, "")
	case errors.As(err, &httpErr) && isAPIKeyRefusedOnV2(httpErr):
		return c.recordV2(&NoV2Error{URL: c.resolved.URL, RefusesAPIKeys: true}, http.StatusForbidden, "")
	case errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusForbidden:
		return c.recordV2(nil, http.StatusForbidden, "")
	case errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound:
		return c.recordV2(&NoV2Error{URL: c.resolved.URL}, http.StatusNotFound, "")
	case errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized:
		return err
	case errors.As(err, &httpErr):
		reconcileLogger.Warn("platform API v2 probe inconclusive; failing this call", "probeStatus", httpErr.StatusCode)
	default:
		reconcileLogger.Warn("platform API v2 probe inconclusive; failing this call", "error", err)
	}
	return fmt.Errorf("check that the platform serves /api/v2: %w", err)
}

func (c *Client) releaseBeforeV2(ctx context.Context) (string, bool) {
	pv, err := c.readVersion(ctx)
	if err != nil {
		reconcileLogger.Debug("platform version unavailable; probing /api/v2 alone", "error", err)
		return "", false
	}
	release, ok := normalizeSemver(pv.gatewayVersion())
	if !ok || semver.Compare(release, "v"+minV2PlatformRelease) >= 0 {
		return "", false
	}
	return strings.TrimPrefix(release, "v"), true
}

func (c *Client) readVersion(ctx context.Context) (*PlatformVersion, error) {
	req, err := c.newRequest(ctx, http.MethodGet, versionEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var pv PlatformVersion
	if err := json.NewDecoder(resp.Body).Decode(&pv); err != nil {
		return nil, err
	}
	return &pv, nil
}

func isAPIKeyRefusedOnV2(httpErr *HTTPError) bool {
	return httpErr.StatusCode == http.StatusForbidden && strings.Contains(httpErr.Body, auth.MsgAPIKeyRouteNotAllowed)
}

func (c *Client) recordV2(refusal error, probeStatus int, release string) error {
	c.v2.mu.Lock()
	c.v2.refusal, c.v2.known = refusal, true
	c.v2.mu.Unlock()
	reconcileLogger.Debug("platform API v2 probed", "servesV2", refusal == nil, "probeStatus", probeStatus, "release", release)
	return refusal
}

func needsV2Probe(path string) bool {
	return strings.HasPrefix(path, platform.APIV2Prefix+"/") && path != APIV2ProbePath+APIV2ProbeQuery
}

func (c *Client) doSingle(ctx context.Context, method, path string, body, into any) error {
	respBody, err := c.send(ctx, c.http, method, path, body)
	if err != nil {
		return err
	}
	if into != nil && len(respBody) > 0 {
		if respBody, err = v2SingleData(respBody); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return c.decode(method, path, respBody, into)
}

func v2SingleData(body []byte) ([]byte, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if envelope.Data == nil || string(envelope.Data) == "null" {
		return nil, errV2ResponseWithoutData
	}
	return envelope.Data, nil
}
