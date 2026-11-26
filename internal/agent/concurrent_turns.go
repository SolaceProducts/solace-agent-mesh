// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const siblingInProgressContent = "[This request is still being worked on in a separate task that has not finished yet. Do not answer or re-execute it.]"

type historyTurns struct {
	currentTaskID  string
	liveSiblings   map[string]bool
	ownRows        ownRowsPlacement
	question       *llm.ChatMessage
	stubBatchBytes int
}

type ownRowsPlacement int

const (
	ownRowsInHistory ownRowsPlacement = iota
	ownRowsAfterQuestion
	ownRowsAfterQuestionStubbed
	ownRowsOmitted
)

func splitOwnRows(msgs []session.Message, taskID string) (history []session.Message, question *session.Message, own []session.Message) {
	for i, m := range msgs {
		if taskID == "" || m.TaskID != taskID {
			history = append(history, m)
			continue
		}
		if question == nil && len(own) == 0 && m.Role == "user" {
			question = &msgs[i]
			continue
		}
		own = append(own, m)
	}
	return history, question, own
}

func openSiblingTasks(msgs []session.Message, currentTaskID string) map[string]bool {
	last := make(map[string]session.Message)
	for _, m := range msgs {
		if m.TaskID != "" && m.TaskID != currentTaskID {
			last[m.TaskID] = m
		}
	}
	var open map[string]bool
	for taskID, m := range last {
		if m.Role == "user" || m.Role == "tool" || (m.Role == "assistant" && len(m.ToolCalls) > 0) {
			if open == nil {
				open = make(map[string]bool)
			}
			open[taskID] = true
		}
	}
	return open
}

type turnGroup struct {
	taskID string
	rows   []session.Message
}

func arrangeSessionTurns(msgs []session.Message, turns historyTurns) []session.Message {
	groups := groupTurnsByTask(msgs)
	if len(groups) <= 1 && len(turns.liveSiblings) == 0 {
		return msgs
	}
	out := make([]session.Message, 0, len(msgs)+len(turns.liveSiblings))
	for _, g := range groups {
		if g.taskID == "" || g.taskID == turns.currentTaskID || !turns.liveSiblings[g.taskID] {
			out = append(out, g.rows...)
			continue
		}
		out = appendInProgressTurn(out, g)
	}
	return out
}

func groupTurnsByTask(msgs []session.Message) []*turnGroup {
	var groups []*turnGroup
	byTask := make(map[string]*turnGroup)
	for _, m := range msgs {
		if m.TaskID == "" {
			if n := len(groups); n > 0 && groups[n-1].taskID == "" {
				groups[n-1].rows = append(groups[n-1].rows, m)
			} else {
				groups = append(groups, &turnGroup{rows: []session.Message{m}})
			}
			continue
		}
		g := byTask[m.TaskID]
		if g == nil {
			g = &turnGroup{taskID: m.TaskID}
			byTask[m.TaskID] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, m)
	}
	return groups
}

func appendInProgressTurn(out []session.Message, g *turnGroup) []session.Message {
	requested := false
	for _, m := range g.rows {
		if m.Role == "user" {
			out = append(out, m)
			requested = true
		}
	}
	if !requested {
		return out
	}
	return append(out, session.Message{Role: "assistant", Content: siblingInProgressContent, TaskID: g.taskID})
}
