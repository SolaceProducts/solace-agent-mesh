// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const maxTriggerValueRunes = 128

type eventSender struct {
	eventHeader    string
	deliveryHeader string
	actionInBody   bool
}

var eventSenders = []eventSender{
	{eventHeader: "X-GitHub-Event", deliveryHeader: "X-GitHub-Delivery", actionInBody: true},
	{eventHeader: "X-Gitlab-Event", deliveryHeader: "X-Gitlab-Event-UUID"},
}

func triggerSource(r *http.Request, route *RouteConfig, body []byte) a2a.TriggerSource {
	source := a2a.TriggerSource{TriggerRoute: sanitizeTriggerValue(route.Name)}
	for _, sender := range eventSenders {
		event := r.Header.Get(sender.eventHeader)
		if event == "" {
			continue
		}
		if sender.actionInBody {
			if action := bodyAction(body); action != "" {
				event += "." + action
			}
		}
		source.TriggerEvent = sanitizeTriggerValue(event)
		source.TriggerDeliveryID = sanitizeTriggerValue(r.Header.Get(sender.deliveryHeader))
		break
	}
	return source
}

func bodyAction(body []byte) string {
	var payload struct {
		Action any `json:"action"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	action, _ := payload.Action.(string)
	return action
}

func sanitizeTriggerValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if !unicode.IsGraphic(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if runes := []rune(value); len(runes) > maxTriggerValueRunes {
		value = string(runes[:maxTriggerValueRunes])
	}
	return value
}
