// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"sync"
	"time"
)

const traceChannelSize = 4096

const traceFlushInterval = 100 * time.Millisecond

type TraceRecord struct {
	Timestamp      time.Time      `json:"timestamp"`
	Type           string         `json:"type"`
	SourceClientID string         `json:"source_client_id"`
	Topic          string         `json:"topic,omitempty"`
	UserProperties map[string]any `json:"user_properties,omitempty"`
	Payload        any            `json:"payload,omitempty"`
	MatchingQueues []string       `json:"matching_queues,omitempty"`
	MessageID      string         `json:"message_id,omitempty"`
}

type TraceWriter struct {
	ch        chan TraceRecord
	filters   []*regexp.Regexp
	traceAcks bool
	done      chan struct{}
	wg        sync.WaitGroup
}

func NewTraceWriter(filePath string, filters []string, traceAcks bool) (*TraceWriter, error) {
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	var compiled []*regexp.Regexp
	for _, pattern := range filters {
		compiled = append(compiled, subscriptionToRegex(pattern))
	}

	tw := &TraceWriter{
		ch:        make(chan TraceRecord, traceChannelSize),
		filters:   compiled,
		traceAcks: traceAcks,
		done:      make(chan struct{}),
	}

	tw.wg.Go(func() {
		tw.writeLoop(f)
	})

	return tw, nil
}

func (tw *TraceWriter) Trace(rec TraceRecord) {
	tw.ch <- rec
}

func (tw *TraceWriter) TraceAcksEnabled() bool {
	return tw.traceAcks
}

func (tw *TraceWriter) matchesFilter(topic string) bool {
	if len(tw.filters) == 0 {
		return true
	}
	for _, re := range tw.filters {
		if re.MatchString(topic) {
			return true
		}
	}
	return false
}

func (tw *TraceWriter) Close() {
	close(tw.ch)
	tw.wg.Wait()
}

func (tw *TraceWriter) writeLoop(f *os.File) {
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 64*1024)
	enc := json.NewEncoder(w)
	ticker := time.NewTicker(traceFlushInterval)
	defer ticker.Stop()

	unflushed := 0
	for {
		select {
		case rec, ok := <-tw.ch:
			if !ok {
				_ = w.Flush()
				return
			}
			_ = enc.Encode(rec)
			unflushed++
		case <-ticker.C:
			if unflushed > 0 {
				_ = w.Flush()
				unflushed = 0
			}
		}
	}
}
