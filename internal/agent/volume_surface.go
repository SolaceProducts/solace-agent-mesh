// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func generateVolumePromptSurface(requestID, promptMessage string, volumes []*volume.Volume, timeout time.Duration) (json.RawMessage, error) {
	var components []map[string]any
	var columnChildren []string

	components = append(components, map[string]any{
		"id":        "title",
		"component": "Text",
		"text":      a2uiLiteral("Volume Required"),
		"variant":   "h3",
	})
	columnChildren = append(columnChildren, "title")

	components = append(components, map[string]any{
		"id":        "prompt",
		"component": "Text",
		"text":      a2uiLiteral(promptMessage),
		"variant":   "body",
	})
	columnChildren = append(columnChildren, "prompt")

	if len(volumes) > 0 {
		var opts []map[string]any
		for _, v := range volumes {
			desc := v.Name
			if v.Description != "" {
				desc = fmt.Sprintf("%s — %s", v.Name, v.Description)
			}
			opts = append(opts, map[string]any{
				"label":       a2uiLiteral(v.Name),
				"value":       v.ID,
				"description": a2uiLiteral(desc),
			})
		}
		opts = append(opts, map[string]any{
			"label": a2uiLiteral("Other (enter ID manually)"),
			"value": "__other__",
		})

		components = append(components, map[string]any{
			"id":        "volume-picker",
			"component": "ChoicePicker",
			"label":     a2uiLiteral("Available Volumes"),
			"variant":   "mutuallyExclusive",
			"options":   opts,
			"value":     map[string]any{"path": "/selected_volume"},
		})
		columnChildren = append(columnChildren, "volume-picker")
	}

	components = append(components, map[string]any{
		"id":          "manual-id",
		"component":   "TextField",
		"label":       a2uiLiteral("Volume ID"),
		"placeholder": a2uiLiteral("Enter volume ID..."),
		"value":       map[string]any{"path": "/manual_volume_id"},
	})
	columnChildren = append(columnChildren, "manual-id")

	components = append(components,
		map[string]any{
			"id":        "submit-text",
			"component": "Text",
			"text":      a2uiLiteral("Select"),
		},
		map[string]any{
			"id":        "submit-btn",
			"component": "Button",
			"child":     "submit-text",
			"variant":   "primary",
			"action": map[string]any{
				"event": map[string]any{
					"name": "submit",
					"context": map[string]any{
						"selected_volume":  map[string]any{"path": "/selected_volume"},
						"manual_volume_id": map[string]any{"path": "/manual_volume_id"},
					},
					"completionText": "Selected",
				},
			},
		},
		map[string]any{
			"id":        "cancel-text",
			"component": "Text",
			"text":      a2uiLiteral("Cancel"),
		},
		map[string]any{
			"id":        "cancel-btn",
			"component": "Button",
			"child":     "cancel-text",
			"action": map[string]any{
				"event": map[string]any{
					"name":           "cancel",
					"context":        map[string]any{},
					"completionText": "Cancelled",
				},
			},
		},
		map[string]any{
			"id":        "actions",
			"component": "Row",
			"children":  map[string]any{"explicitList": []string{"submit-btn", "cancel-btn"}},
			"justify":   "end",
		},
	)
	columnChildren = append(columnChildren, "actions")

	expiryText := fmt.Sprintf("Expires in %d minutes", int(timeout.Minutes()))
	components = append(components, map[string]any{
		"id":        "countdown",
		"component": "Text",
		"text":      a2uiLiteral(expiryText),
		"variant":   "caption",
	})
	columnChildren = append(columnChildren, "countdown")

	components = append(components,
		map[string]any{
			"id":        "main-col",
			"component": "Column",
			"children":  map[string]any{"explicitList": columnChildren},
		},
		map[string]any{
			"id":        "root",
			"component": "Card",
			"child":     "main-col",
		},
	)

	surface := map[string]any{
		"surfaceId":  requestID,
		"catalogId":  "https://a2ui.org/specification/v0_9/basic_catalog.json",
		"components": components,
		"dataModel": map[string]any{
			"selected_volume":  "",
			"manual_volume_id": "",
		},
	}

	return json.Marshal(surface)
}
