// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
)

const (
	sempMaxResponseBytes = 8 << 20
	sempPageSize         = 100
	sempMaxPages         = 10_000
	sempTimeout          = 30 * time.Second
)

type sempConfig struct {
	BaseURL            string
	VPN                string
	Username           string
	Password           string
	CACertPath         string
	InsecureSkipVerify bool
}

type sempClient struct {
	queuesURL *url.URL
	username  string
	password  string
	http      *http.Client
}

func newSEMPClient(cfg sempConfig) (*sempClient, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("SOLACE_SEMP_URL is required")
	}
	if strings.TrimSpace(cfg.VPN) == "" {
		return nil, errors.New("message VPN is required (SOLACE_BROKER_VPN)")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("SOLACE_SEMP_USERNAME and SOLACE_SEMP_PASSWORD are required")
	}
	base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(cfg.BaseURL), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse SOLACE_SEMP_URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("SOLACE_SEMP_URL scheme %q must be http or https", base.Scheme)
	}
	queuesURL, err := url.Parse(fmt.Sprintf("%s/SEMP/v2/config/msgVpns/%s/queues", base.String(), url.PathEscape(cfg.VPN)))
	if err != nil {
		return nil, fmt.Errorf("build queues URL: %w", err)
	}
	hc, err := tlsutil.BuildHTTPClient(cfg.CACertPath, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("build HTTP client: %w", err)
	}
	hc.Timeout = sempTimeout
	return &sempClient{queuesURL: queuesURL, username: cfg.Username, password: cfg.Password, http: hc}, nil
}

type sempResponse struct {
	Data []struct {
		QueueName string `json:"queueName"`
	} `json:"data"`
	Meta struct {
		Paging struct {
			NextPageURI string `json:"nextPageUri"`
		} `json:"paging"`
		Error *struct {
			Status      string `json:"status"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"meta"`
}

func (c *sempClient) listQueues(ctx context.Context, prefix string) ([]string, error) {
	q := url.Values{}
	q.Set("count", fmt.Sprint(sempPageSize))
	q.Set("select", "queueName")
	q.Set("where", "queueName=="+prefix+"*")
	first := *c.queuesURL
	first.RawQuery = q.Encode()

	var names []string
	for pageURL, page := first.String(), 0; pageURL != ""; page++ {
		if page >= sempMaxPages {
			return nil, fmt.Errorf("queue listing exceeded %d pages", sempMaxPages)
		}
		var body sempResponse
		if err := c.do(ctx, http.MethodGet, pageURL, &body); err != nil {
			return nil, err
		}
		for _, qd := range body.Data {
			names = append(names, qd.QueueName)
		}
		pageURL = body.Meta.Paging.NextPageURI
		if pageURL != "" {
			next, perr := url.Parse(pageURL)
			if perr != nil {
				return nil, fmt.Errorf("parse SEMP nextPageUri: %w", perr)
			}
			if next.Scheme != c.queuesURL.Scheme || next.Host != c.queuesURL.Host {
				return nil, fmt.Errorf("SEMP nextPageUri host %q does not match %q", next.Host, c.queuesURL.Host)
			}
		}
	}
	return names, nil
}

func (c *sempClient) deleteQueue(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, c.queuesURL.String()+"/"+url.PathEscape(name), nil)
	if errors.Is(err, errSEMPNotFound) {
		return nil
	}
	return err
}

var errSEMPNotFound = errors.New("SEMP object not found")

func (c *sempClient) do(ctx context.Context, method, rawURL string, out *sempResponse) error {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build SEMP request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("SEMP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, sempMaxResponseBytes))
	if err != nil {
		return fmt.Errorf("read SEMP response: %w", err)
	}
	var decoded sempResponse
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return fmt.Errorf("unexpected non-JSON response (HTTP %d) from SEMP", resp.StatusCode)
		}
	}
	if decoded.Meta.Error != nil && strings.EqualFold(decoded.Meta.Error.Status, "NOT_FOUND") {
		return errSEMPNotFound
	}
	if resp.StatusCode == http.StatusNotFound {
		return errSEMPNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decoded.Meta.Error != nil {
			return fmt.Errorf("SEMP HTTP %d: %s", resp.StatusCode, decoded.Meta.Error.Description)
		}
		return fmt.Errorf("SEMP HTTP %d", resp.StatusCode)
	}
	if decoded.Meta.Error != nil {
		return fmt.Errorf("SEMP error %s: %s", decoded.Meta.Error.Status, decoded.Meta.Error.Description)
	}
	if out != nil {
		*out = decoded
	}
	return nil
}
