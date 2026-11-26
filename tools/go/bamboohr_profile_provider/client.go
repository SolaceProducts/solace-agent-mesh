// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.bamboohr.com/api/gateway.php"

type client struct {
	baseURL   string
	subdomain string
	apiKey    string
	http      *http.Client
	logger    *slog.Logger

	retryAttempts int
	retryBackoff  time.Duration
}

type clientError struct {
	Status  int
	Path    string
	BodyLen int
}

func (e *clientError) Error() string {
	return fmt.Sprintf("bamboohr: %s returned status %d (body %d bytes)", e.Path, e.Status, e.BodyLen)
}

func redactedCause(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func newClient(cfg *bamboohrConfig, logger *slog.Logger) *client {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		subdomain:     cfg.Subdomain,
		apiKey:        cfg.APIKey,
		http:          &http.Client{Timeout: cfg.RequestTimeout, CheckRedirect: dropCredentialsOnRedirect},
		logger:        logger.With("logger_name", "bamboohr-client"),
		retryAttempts: 3,
		retryBackoff:  500 * time.Millisecond,
	}
}

func (c *client) apiURL(path string, params url.Values) string {
	u := fmt.Sprintf("%s/%s/v1/%s", c.baseURL, c.subdomain, strings.TrimLeft(path, "/"))
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}

func (c *client) doJSON(ctx context.Context, path string, params url.Values, out any) error {
	return c.doWithRetries(ctx, path, params, func(body io.Reader) error {
		if out == nil {
			_, err := io.Copy(io.Discard, body)
			return err
		}
		dec := json.NewDecoder(body)
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		return nil
	})
}

func (c *client) doWithRetries(ctx context.Context, path string, params url.Values, handle func(io.Reader) error) error {
	var lastErr error
	backoff := c.retryBackoff
	for attempt := 1; attempt <= c.retryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.doOnce(ctx, path, params, handle)
		if err == nil {
			return nil
		}
		lastErr = err

		if ce, ok := errors.AsType[*clientError](err); ok {
			if ce.Status != http.StatusTooManyRequests && ce.Status < 500 {
				return err
			}
		}

		if attempt == c.retryAttempts {
			break
		}
		c.logger.Warn("bamboohr request failed; retrying",
			"path", path,
			"attempt", attempt,
			"error", err,
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}

func (c *client) doOnce(ctx context.Context, path string, params url.Values, handle func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL(path, params), nil)
	if err != nil {
		return fmt.Errorf("bamboohr: build request %s: %w", path, redactedCause(err))
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(c.apiKey, "x")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bamboohr: request %s: %w", path, redactedCause(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return handle(resp.Body)
	}

	const maxBodyBytes = 4096
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	return &clientError{Status: resp.StatusCode, Path: path, BodyLen: len(snippet)}
}

type rawDirectoryEmployee struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	FirstName       string `json:"firstName"`
	LastName        string `json:"lastName"`
	PreferredName   string `json:"preferredName"`
	JobTitle        string `json:"jobTitle"`
	WorkEmail       string `json:"workEmail"`
	Department      string `json:"department"`
	Location        string `json:"location"`
	Supervisor      string `json:"supervisor"`
	SupervisorEmail string `json:"supervisorEmail"`
	HireDate        string `json:"hireDate"`
	MobilePhone     string `json:"mobilePhone"`
	PhotoURL        string `json:"photoUrl"`
}

type directoryResponse struct {
	Employees []rawDirectoryEmployee `json:"employees"`
}

func (c *client) fetchEmployeeDirectory(ctx context.Context) ([]rawDirectoryEmployee, error) {
	var resp directoryResponse
	if err := c.doJSON(ctx, "employees/directory", nil, &resp); err != nil {
		return nil, err
	}
	c.logger.Debug("fetched bamboohr directory", "count", len(resp.Employees))
	return resp.Employees, nil
}

type whosOutEntry struct {
	EmployeeID any    `json:"employeeId"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Type       string `json:"type"`
}

func (c *client) fetchWhosOut(ctx context.Context, startDate, endDate string) ([]whosOutEntry, error) {
	params := url.Values{}
	if startDate != "" {
		params.Set("start", startDate)
	}
	if endDate != "" {
		params.Set("end", endDate)
	}
	var entries []whosOutEntry
	if err := c.doJSON(ctx, "time_off/whos_out", params, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

const photoEndpointLabel = "employee photo"

func originOf(raw string) (scheme, host, port string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	port = u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme, strings.ToLower(u.Hostname()), port, true
}

func (c *client) mayReceiveAPIKey(photoURL string) bool {
	targetScheme, targetHost, targetPort, ok := originOf(photoURL)
	if !ok {
		return false
	}
	baseScheme, baseHost, basePort, ok := originOf(c.baseURL)
	if !ok {
		return false
	}
	return targetScheme == baseScheme && targetHost == baseHost && targetPort == basePort
}

func dropCredentialsOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	req.Header.Del("Authorization")
	return nil
}

func (c *client) fetchPhoto(ctx context.Context, photoURL string) ([]byte, string, error) {
	if photoURL == "" {
		return nil, "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("bamboohr: build %s request: %w", photoEndpointLabel, redactedCause(err))
	}
	if c.mayReceiveAPIKey(photoURL) {
		req.SetBasicAuth(c.apiKey, "x")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("bamboohr: %s request: %w", photoEndpointLabel, redactedCause(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		const maxBodyBytes = 1024
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		return nil, "", &clientError{Status: resp.StatusCode, Path: photoEndpointLabel, BodyLen: len(snippet)}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("bamboohr: read photo body: %w", err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func matchesEmployee(entryID any, target string) bool {
	switch v := entryID.(type) {
	case string:
		return v == target
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64) == target
	case int:
		return strconv.Itoa(v) == target
	case int64:
		return strconv.FormatInt(v, 10) == target
	}
	return false
}
