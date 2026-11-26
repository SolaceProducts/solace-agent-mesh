// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/visualization"
)

func buildVisualizationEvents(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		payload, ok := m["payload"].(map[string]any)
		if !ok {
			continue
		}
		topic, _ := m["topic"].(string)
		evt := visualization.InferEventDetails(topic, payload)
		if wallTime, _ := m["wall_time"].(string); wallTime != "" {
			evt.Timestamp = wallTime
		}
		b, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(b, &record); err != nil {
			continue
		}
		out = append(out, record)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
