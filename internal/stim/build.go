// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
)

type Event struct {
	TaskID      string
	CreatedTime int64
	Topic       string
	Direction   string
	Payload     string
	UserID      *string
}

type Header struct {
	RootTaskID         string
	UserID             string
	StartTime          int64
	EndTime            int64
	Status             string
	InitialRequestText string
	TotalTasks         int
	IncludesChildTasks bool
}

func BuildDocument(hdr Header, events []Event) map[string]any {
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b Event) int {
		return cmp.Compare(a.CreatedTime, b.CreatedTime)
	})

	idPrefix := hdr.RootTaskID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}

	flow := make([]map[string]any, 0, len(sorted))
	for i, evt := range sorted {
		var payloadObj any
		if err := json.Unmarshal([]byte(evt.Payload), &payloadObj); err != nil {
			payloadObj = evt.Payload
		}

		entry := map[string]any{
			"id":           fmt.Sprintf("evt-%s-%03d", idPrefix, i),
			"task_id":      evt.TaskID,
			"created_time": evt.CreatedTime,
			"topic":        evt.Topic,
			"direction":    evt.Direction,
			"payload":      payloadObj,
		}
		if evt.UserID != nil {
			entry["user_id"] = *evt.UserID
		}
		flow = append(flow, entry)
	}

	return map[string]any{
		"invocation_details": map[string]any{
			"log_file_version":     "2.0",
			"task_id":              hdr.RootTaskID,
			"user_id":              hdr.UserID,
			"start_time":           hdr.StartTime,
			"end_time":             hdr.EndTime,
			"status":               hdr.Status,
			"initial_request_text": hdr.InitialRequestText,
			"includes_child_tasks": hdr.IncludesChildTasks,
			"total_tasks":          hdr.TotalTasks,
		},
		"invocation_flow": flow,
	}
}
