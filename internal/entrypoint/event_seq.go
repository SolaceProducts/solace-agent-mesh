// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"log/slog"
	"strconv"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const maxPublishersPerTask = 64

const seqTrackerShards = 64

type EventSeqTracker struct {
	shards [seqTrackerShards]seqShard
	logger *slog.Logger
}

type seqShard struct {
	mu      sync.Mutex
	perTask map[string]*taskSeqState
}

type taskSeqState struct {
	last   map[string]uint64
	capped bool
}

func NewEventSeqTracker(logger *slog.Logger) *EventSeqTracker {
	if logger == nil {
		logger = slog.Default()
	}
	t := &EventSeqTracker{logger: logger}
	for i := range t.shards {
		t.shards[i].perTask = make(map[string]*taskSeqState)
	}
	return t
}

func (t *EventSeqTracker) shardFor(taskID string) *seqShard {
	return &t.shards[hashTaskID(taskID)%seqTrackerShards]
}

func (t *EventSeqTracker) Observe(taskID, traceID string, userProps map[string]string) {
	if t == nil || taskID == "" || len(userProps) == 0 {
		return
	}
	publisherID := userProps[a2a.PropPublisherID]
	seqStr := userProps[a2a.PropEventSeq]
	if publisherID == "" || seqStr == "" {
		return
	}
	seq, err := strconv.ParseUint(seqStr, 10, 64)
	if err != nil {
		t.logger.Warn("event sequence: unparseable seq value",
			"taskID", taskID,
			"traceID", traceID,
			"publisherID", publisherID,
			"rawSeq", seqStr,
		)
		return
	}

	sh := t.shardFor(taskID)
	sh.mu.Lock()
	st, ok := sh.perTask[taskID]
	if !ok {
		st = &taskSeqState{last: make(map[string]uint64)}
		sh.perTask[taskID] = st
	}
	last, seen := st.last[publisherID]
	switch {
	case !seen:
		if len(st.last) >= maxPublishersPerTask {
			if !st.capped {
				st.capped = true
				sh.mu.Unlock()
				t.logger.Warn("event sequence: publisher cap reached, halting tracking for new publishers on this task",
					"taskID", taskID,
					"traceID", traceID,
					"publisherCount", maxPublishersPerTask,
				)
				return
			}
			sh.mu.Unlock()
			return
		}
		st.last[publisherID] = seq
		sh.mu.Unlock()
	case seq == last+1:
		st.last[publisherID] = seq
		sh.mu.Unlock()
	case seq > last+1:
		gap := seq - last - 1
		st.last[publisherID] = seq
		sh.mu.Unlock()
		t.logger.Error("event sequence: gap detected on per-publisher stream",
			"taskID", taskID,
			"traceID", traceID,
			"publisherID", publisherID,
			"lastSeq", last,
			"currentSeq", seq,
			"gap", gap,
		)
	case seq == 1 && last > 1:
		st.last[publisherID] = seq
		sh.mu.Unlock()
		t.logger.Debug("event sequence: publisher stream reset",
			"taskID", taskID,
			"traceID", traceID,
			"publisherID", publisherID,
			"lastSeq", last,
			"currentSeq", seq,
		)
	default:
		sh.mu.Unlock()
		t.logger.Warn("event sequence: duplicate or out-of-order event",
			"taskID", taskID,
			"traceID", traceID,
			"publisherID", publisherID,
			"lastSeq", last,
			"currentSeq", seq,
		)
	}
}

func (t *EventSeqTracker) Forget(taskID string) {
	if t == nil || taskID == "" {
		return
	}
	sh := t.shardFor(taskID)
	sh.mu.Lock()
	delete(sh.perTask, taskID)
	sh.mu.Unlock()
}
