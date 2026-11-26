// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/sethvargo/go-retry"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const teamsGatewayName = "TeamsGateway"

const (
	teamsOpSendMessage        = "send_message"
	teamsOpUpdateMessage      = "update_message"
	teamsOpDeleteMessage      = "delete_message"
	teamsOpGetBotToken        = "get_bot_token"
	teamsOpDownloadAttachment = "download_attachment"
	teamsOpCreateConversation = "create_conversation"
)

const (
	oauthTokenURL = "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token"
	oauthScope    = "https://api.botframework.com/.default"
)

const activityPathFmt = "/v3/conversations/%s/activities/%s"

type BotClient struct {
	appID       string
	appPassword string
	tenantID    string
	apiBaseURL  string
	client      *http.Client
	logger      *slog.Logger

	mu          sync.RWMutex
	token       string
	tokenExpiry time.Time
}

func NewBotClient(appID, appPassword, tenantID string, logger *slog.Logger) *BotClient {
	return &BotClient{
		appID:       appID,
		appPassword: appPassword,
		tenantID:    tenantID,
		client:      &http.Client{Timeout: 30 * time.Second},
		logger:      logger,
	}
}

func (c *BotClient) SendActivity(ctx context.Context, serviceURL, conversationID string, activity *Activity) (string, error) {
	url := c.buildURL(serviceURL, fmt.Sprintf("/v3/conversations/%s/activities", conversationID))

	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpSendMessage)
	var resp ResourceResponse
	err := c.doJSON(ctx, http.MethodPost, url, activity, &resp)
	m.Stop(&err)
	if err != nil {
		return "", fmt.Errorf("send activity: %w", err)
	}
	return resp.ID, nil
}

func (c *BotClient) CreatePersonalConversation(ctx context.Context, serviceURL, userID, tenantID string) (string, error) {
	url := c.buildURL(serviceURL, "/v3/conversations")

	body := createConversationRequest{
		IsGroup: false,
		Bot:     Account{ID: c.appID},
		Members: []Account{{ID: userID}},
	}
	body.ChannelData.Tenant.ID = tenantID

	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpCreateConversation)
	var resp ResourceResponse
	var err error
	defer func() { m.Stop(&err) }()
	if err = c.doJSON(ctx, http.MethodPost, url, body, &resp); err != nil {
		return "", fmt.Errorf("create personal conversation: %w", err)
	}
	if resp.ID == "" {
		err = fmt.Errorf("create personal conversation: empty conversation id in response")
		return "", err
	}
	return resp.ID, nil
}

type createConversationRequest struct {
	IsGroup     bool      `json:"isGroup"`
	Bot         Account   `json:"bot"`
	Members     []Account `json:"members"`
	ChannelData struct {
		Tenant struct {
			ID string `json:"id"`
		} `json:"tenant"`
	} `json:"channelData"`
}

func (c *BotClient) UpdateActivity(ctx context.Context, serviceURL, conversationID, activityID string, activity *Activity) error {
	url := c.buildURL(serviceURL, fmt.Sprintf(activityPathFmt, conversationID, activityID))

	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpUpdateMessage)
	err := c.doJSON(ctx, http.MethodPut, url, activity, nil)
	m.Stop(&err)
	if err != nil {
		return fmt.Errorf("update activity: %w", err)
	}
	return nil
}

func (c *BotClient) DeleteActivity(ctx context.Context, serviceURL, conversationID, activityID string) error {
	url := c.buildURL(serviceURL, fmt.Sprintf(activityPathFmt, conversationID, activityID))

	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpDeleteMessage)
	err := c.doJSON(ctx, http.MethodDelete, url, nil, nil)
	m.Stop(&err)
	if err != nil {
		return fmt.Errorf("delete activity: %w", err)
	}
	return nil
}

func (c *BotClient) ReplyToActivity(ctx context.Context, serviceURL, conversationID, activityID string, activity *Activity) (string, error) {
	url := c.buildURL(serviceURL, fmt.Sprintf(activityPathFmt, conversationID, activityID))

	activity.ReplyToID = activityID
	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpSendMessage)
	var resp ResourceResponse
	err := c.doJSON(ctx, http.MethodPost, url, activity, &resp)
	m.Stop(&err)
	if err != nil {
		return "", fmt.Errorf("reply to activity: %w", err)
	}
	return resp.ID, nil
}

func (c *BotClient) SendTyping(ctx context.Context, serviceURL, conversationID string, from Account) error {
	activity := &Activity{
		Type: ActivityTypeTyping,
		From: from,
	}
	_, err := c.SendActivity(ctx, serviceURL, conversationID, activity)
	return err
}

func (c *BotClient) buildURL(serviceURL, path string) string {
	if c.apiBaseURL != "" {
		return strings.TrimRight(c.apiBaseURL, "/") + path
	}
	return strings.TrimRight(serviceURL, "/") + path
}

func (c *BotClient) doJSON(ctx context.Context, method, url string, body any, result any) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	backoff := retry.WithMaxRetries(4, retry.NewExponential(1*time.Second))
	backoff = retry.WithCappedDuration(30*time.Second, backoff)

	return retry.Do(ctx, backoff, func(ctx context.Context) error {
		if body != nil {
			data, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(data)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return retry.RetryableError(err)
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if result != nil && len(respBody) > 0 {
				if err := json.Unmarshal(respBody, result); err != nil {
					return fmt.Errorf("decode response: %w", err)
				}
			}
			return nil
		}

		if isNonRetryableBotStatus(resp.StatusCode) {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}
		redactedURL := "[redacted]"
		if u, err := neturl.Parse(url); err == nil {
			redactedURL = u.Scheme + "://" + u.Host
		}
		c.logger.Warn("retryable Bot Framework API error",
			"httpStatus", resp.StatusCode, "url", redactedURL)
		return retry.RetryableError(fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody)))
	})
}

func (c *BotClient) getToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		token := c.token
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	token, expiresIn, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}

	c.token = token
	c.tokenExpiry = time.Now().Add(time.Duration(expiresIn)*time.Second - 5*time.Minute)
	c.logger.Debug("Bot Framework OAuth token refreshed", "expires_in_seconds", expiresIn)
	return token, nil
}

func (c *BotClient) fetchToken(ctx context.Context) (token string, expiresIn int, err error) {
	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpGetBotToken)
	defer m.Stop(&err)

	data := neturl.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.appID},
		"client_secret": {c.appPassword},
		"scope":         {oauthScope},
	}

	tokenURL := oauthTokenURL
	if c.tenantID != "" {
		tokenURL = homeTenantTokenURL(c.tenantID)
	}
	if c.apiBaseURL != "" {
		tokenURL = strings.TrimRight(c.apiBaseURL, "/") + "/botframework.com/oauth2/v2.0/token"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("token request returned HTTP %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", 0, fmt.Errorf("decode token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", 0, fmt.Errorf("empty access token in response")
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}

func homeTenantTokenURL(tenantID string) string {
	return "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token"
}
