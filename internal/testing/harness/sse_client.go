// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

type SSEEvent struct {
	ID    string
	Event string
	Data  json.RawMessage
}

type SSEClient struct {
	url         string
	client      *http.Client
	logger      *slog.Logger
	Headers     map[string]string
	OnConnected func()
}

func NewSSEClient(url string, client *http.Client, logger *slog.Logger) *SSEClient {
	return &SSEClient{
		url:    url,
		client: client,
		logger: logger,
	}
}

func (c *SSEClient) Subscribe(ctx context.Context, events chan<- SSEEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("sse: connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sse: unexpected status %d", resp.StatusCode)
	}

	if c.OnConnected != nil {
		c.OnConnected()
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		eventType string
		eventID   string
		dataBuf   strings.Builder
	)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if dataBuf.Len() > 0 {
				evt := SSEEvent{
					ID:    eventID,
					Event: eventType,
					Data:  json.RawMessage(dataBuf.String()),
				}
				select {
				case events <- evt:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			eventType = ""
			eventID = ""
			dataBuf.Reset()
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			eventType = value
		case "data":
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(value)
		case "id":
			eventID = value
		case "retry":
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("sse: read: %w", err)
	}

	return nil
}
