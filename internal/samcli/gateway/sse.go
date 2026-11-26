// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SSEStream struct {
	client  *Client
	timeout time.Duration
}

func NewSSEStream(client *Client, timeout time.Duration) *SSEStream {
	return &SSEStream{
		client:  client,
		timeout: timeout,
	}
}

func (s *SSEStream) Subscribe(taskID string, handler func(SSEEvent) error) error {
	url := fmt.Sprintf("%s/api/v1/sse/subscribe/%s", s.client.baseURL(), taskID)
	return s.subscribe(url, handler)
}

func (s *SSEStream) SubscribeFrom(taskID string, lastSequence int64, handler func(SSEEvent) error) error {
	url := fmt.Sprintf("%s/api/v1/sse/subscribe/%s?reconnect=true&lastSequence=%d",
		s.client.baseURL(), taskID, lastSequence)
	return s.subscribe(url, handler)
}

func (s *SSEStream) subscribe(url string, handler func(SSEEvent) error) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	s.client.setHeaders(req)

	streamingClient := &http.Client{}

	resp, err := streamingClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE subscribe: HTTP %d", resp.StatusCode)
	}

	return s.parseStream(resp, handler)
}

func (s *SSEStream) parseStream(resp *http.Response, handler func(SSEEvent) error) error {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var eventType string
	var dataLines []string
	var lastSequence int64
	startTime := time.Now()

	for scanner.Scan() {
		if time.Since(startTime) > s.timeout {
			return fmt.Errorf("SSE stream timeout after %v", s.timeout)
		}

		line := scanner.Text()

		if line == "" {
			if len(dataLines) > 0 {
				if eventType == "" {
					eventType = "message"
				}

				rawData := strings.Join(dataLines, "\n")
				var data map[string]any
				if err := json.Unmarshal([]byte(rawData), &data); err != nil {
					data = map[string]any{"raw": rawData}
				}

				event := SSEEvent{
					EventType: eventType,
					Data:      data,
					RawData:   rawData,
					Timestamp: time.Now(),
					Sequence:  lastSequence,
				}

				if err := handler(event); err != nil {
					return err
				}

				eventType = ""
				dataLines = nil
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(line[5:]))
		} else if strings.HasPrefix(line, "id:") {
			if seq, err := strconv.ParseInt(strings.TrimSpace(line[3:]), 10, 64); err == nil {
				lastSequence = seq
			}
		} else if line[0] == ':' {
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE stream read: %w", err)
	}

	return nil
}
