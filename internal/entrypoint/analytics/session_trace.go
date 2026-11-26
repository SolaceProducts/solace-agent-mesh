// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type SessionInput struct {
	RatedRoot *store.Task
	Roots     []*store.Task
	Forest    []store.ForestNode
}

func ServedTurns(in SessionInput) (turns []*store.Task, earlierOmitted bool) {
	turns = make([]*store.Task, 0, len(in.Roots))
	for _, r := range in.Roots {
		if r.ParentTaskID != nil {
			continue
		}
		if in.RatedRoot != nil && r.StartTime > in.RatedRoot.StartTime {
			continue
		}
		turns = append(turns, r)
	}
	isRated := func(t *store.Task) bool { return in.RatedRoot != nil && t.ID == in.RatedRoot.ID }
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartTime != turns[j].StartTime {
			return turns[i].StartTime < turns[j].StartTime
		}
		return !isRated(turns[i]) && isRated(turns[j])
	})
	if len(turns) > MaxSessionTurns {
		return turns[len(turns)-MaxSessionTurns:], true
	}
	return turns, false
}

func (s *Service) SessionTrace(ctx context.Context, fb *store.Feedback, in SessionInput) (*SessionTraceResponse, error) {
	sorted, omitted := ServedTurns(in)
	resp := s.feedbackHeader(fb)
	resp.RatedTaskID = fb.TaskID
	resp.EarlierTurnsOmitted = omitted

	layout := turnLayouts(sorted, in.Forest)
	children, err := s.loadTurnChildren(ctx, layout)
	if err != nil {
		return nil, err
	}
	eventsByTask, noiseDropped, counts, err := s.loadTurnEvents(ctx, sorted, children)
	if err != nil {
		return nil, err
	}

	ratedRootID := ""
	if in.RatedRoot != nil {
		ratedRootID = in.RatedRoot.ID
	}
	resp.RetentionGap = true
	resp.Turns = make([]TaskTreeResponse, 0, len(sorted))
	for _, root := range sorted {
		tree := s.assembleTaskTree(root.ID, []store.Task{*root}, children[root.ID], eventsByTask, noiseDropped, counts, len(children[root.ID]) >= MaxTreeTasks)
		if root.ID == ratedRootID {
			resp.RetentionGap = false
			resp.Owner = tree.Owner
		}
		resp.Turns = append(resp.Turns, *tree)
	}

	fitSessionToBudget(resp)
	return resp, nil
}

func (s *Service) RefusedSession(fb *store.Feedback, refusal string) *SessionTraceResponse {
	resp := s.feedbackHeader(fb)
	resp.Refusal = refusal
	return resp
}

func (s *Service) feedbackHeader(fb *store.Feedback) *SessionTraceResponse {
	resp := &SessionTraceResponse{
		FeedbackID: fb.ID,
		Rating:     fb.Rating,
		Subject:    s.subjectRef(fb.SubjectID, fb.SubjectType),
		Owner:      fb.UserID,
	}
	if fb.UpdatedAt > 0 {
		resp.RatedAt = epochMsToISO8601(fb.UpdatedAt)
	}
	if fb.Comment != nil {
		resp.Comment = *fb.Comment
	}
	return resp
}

func turnLayouts(roots []*store.Task, forest []store.ForestNode) map[string][]store.ForestNode {
	kept := make(map[string]bool, len(roots))
	for _, r := range roots {
		kept[r.ID] = true
	}
	out := make(map[string][]store.ForestNode, len(roots))
	for _, n := range forest {
		if !kept[n.RootID] || n.Depth < 1 || n.Depth > MaxTreeWalkDownDepth+1 {
			continue
		}
		out[n.RootID] = append(out[n.RootID], n)
	}
	for rootID, nodes := range out {
		sort.Slice(nodes, func(i, j int) bool {
			a, b := nodes[i], nodes[j]
			if a.Depth != b.Depth {
				return a.Depth < b.Depth
			}
			if a.StartTime != b.StartTime {
				return a.StartTime < b.StartTime
			}
			return a.ID < b.ID
		})
		if len(nodes) > MaxTreeTasks {
			out[rootID] = nodes[:MaxTreeTasks]
		}
	}
	return out
}

func (s *Service) loadTurnChildren(ctx context.Context, layout map[string][]store.ForestNode) (map[string][]store.Task, error) {
	var ids []string
	for _, nodes := range layout {
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
	}
	byID := make(map[string]*store.Task, len(ids))
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		rows, err := s.taskRepo.GetByIDs(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("analytics.SessionTrace nodes: %w", err)
		}
		for _, r := range rows {
			byID[r.ID] = r
		}
	}
	out := make(map[string][]store.Task, len(layout))
	for rootID, nodes := range layout {
		for _, n := range nodes {
			if t, ok := byID[n.ID]; ok {
				out[rootID] = append(out[rootID], *t)
			}
		}
	}
	return out, nil
}

func (s *Service) loadTurnEvents(ctx context.Context, roots []*store.Task, children map[string][]store.Task) (map[string][]*store.TaskEvent, map[string]int, map[string]int, error) {
	ids := make([]string, 0, len(roots))
	for _, r := range roots {
		ids = append(ids, r.ID)
		for _, c := range children[r.ID] {
			ids = append(ids, c.ID)
		}
	}
	var all []*store.TaskEvent
	counts := make(map[string]int, len(ids))
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		events, chunkCounts, err := s.taskEventRepo.GetTreeEventsByTaskIDs(ctx, chunk, DefaultTreeEventsPerTask)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("analytics.SessionTrace events: %w", err)
		}
		all = append(all, events...)
		maps.Copy(counts, chunkCounts)
	}
	eventsByTask, noiseDropped := groupTreeEvents(all)
	return eventsByTask, noiseDropped, counts, nil
}

func sessionSizeBytes(resp *SessionTraceResponse) int {
	b, _ := json.Marshal(resp)
	return len(b)
}

func fitSessionToBudget(resp *SessionTraceResponse) {
	if sessionSizeBytes(resp) <= maxSessionBytes {
		return
	}
	resp.Comment = truncateRunes(resp.Comment, maxSessionCommentRunes)
	if sessionSizeBytes(resp) <= maxSessionBytes {
		return
	}
	for i := range resp.Turns {
		turn := &resp.Turns[i]
		for _, evCap := range treeShrinkLadder {
			if resampleTreeEvents(turn, evCap) {
				turn.TreeEventsCapped = true
			}
			if sessionSizeBytes(resp) <= maxSessionBytes {
				return
			}
		}
		markEventsOmitted(turn)
		if sessionSizeBytes(resp) <= maxSessionBytes {
			return
		}
	}
	for i := range resp.Turns {
		hardClipErrors(&resp.Turns[i], minErrorMessageChars)
	}
	for sessionSizeBytes(resp) > maxSessionBytes && len(resp.Turns) > 1 {
		resp.Turns = resp.Turns[1:]
		resp.EarlierTurnsOmitted = true
	}
}

func markEventsOmitted(turn *TaskTreeResponse) {
	mark := func(n *TreeNodeDTO) {
		if len(n.Events) == 0 && n.EventsTotal > 0 {
			n.EventsOmitted = true
		}
	}
	mark(&turn.Root)
	for i := range turn.Tree {
		mark(&turn.Tree[i])
	}
}
