// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func parseSSEStream(r io.Reader) <-chan proxy.ResponseEvent {
	ch := make(chan proxy.ResponseEvent, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var dataLines []string

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimPrefix(data, " ")
				dataLines = append(dataLines, data)
				continue
			}

			if line == "" && len(dataLines) > 0 {
				fullData := strings.Join(dataLines, "\n")
				dataLines = dataLines[:0]

				evt, err := parseSSEData(fullData)
				if err != nil {
					ch <- proxy.ResponseEvent{Error: fmt.Errorf("parse SSE event: %w", err)}
					return
				}
				ch <- evt
				continue
			}
		}

		if len(dataLines) > 0 {
			fullData := strings.Join(dataLines, "\n")
			evt, err := parseSSEData(fullData)
			if err != nil {
				ch <- proxy.ResponseEvent{Error: fmt.Errorf("parse final SSE event: %w", err)}
				return
			}
			ch <- evt
		}

		if err := scanner.Err(); err != nil {
			ch <- proxy.ResponseEvent{Error: fmt.Errorf("SSE stream read: %w", err)}
		}
	}()
	return ch
}

func parseSSEData(data string) (proxy.ResponseEvent, error) {
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *a2a.Error      `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
		return proxy.ResponseEvent{}, fmt.Errorf("unmarshal JSON-RPC: %w", err)
	}
	if rpcResp.Error != nil {
		return proxy.ResponseEvent{Error: fmt.Errorf("downstream error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)}, nil
	}

	resultData := rpcResp.Result
	if resultData == nil {
		resultData = json.RawMessage(data)
	}

	var kindProbe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(resultData, &kindProbe); err != nil {
		return proxy.ResponseEvent{}, fmt.Errorf("probe kind: %w", err)
	}

	switch kindProbe.Kind {
	case "task":
		var task a2a.Task
		if err := json.Unmarshal(resultData, &task); err != nil {
			return proxy.ResponseEvent{}, fmt.Errorf("unmarshal task: %w", err)
		}
		return proxy.ResponseEvent{Task: &task}, nil

	case "status-update":
		var su a2a.TaskStatusUpdateEvent
		if err := json.Unmarshal(resultData, &su); err != nil {
			return proxy.ResponseEvent{}, fmt.Errorf("unmarshal status update: %w", err)
		}
		return proxy.ResponseEvent{StatusUpdate: &su}, nil

	case "artifact-update":
		var au a2a.TaskArtifactUpdateEvent
		if err := json.Unmarshal(resultData, &au); err != nil {
			return proxy.ResponseEvent{}, fmt.Errorf("unmarshal artifact update: %w", err)
		}
		return proxy.ResponseEvent{ArtifactUpdate: &au}, nil

	default:
		slog.Default().With("logger_name", "proxy-sse").Debug("skipping unknown SSE event kind", "kind", kindProbe.Kind)
		return proxy.ResponseEvent{}, nil
	}
}
