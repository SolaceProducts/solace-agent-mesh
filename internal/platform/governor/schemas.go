// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type upconverter func(raw json.RawMessage) (spend.Event, error)

var (
	errNewerSchema = errors.New("spend event schema is newer than this governor files")

	errNoUpconverter = errors.New("spend event schema has no upconverter in this governor")
)

type schemas struct {
	newest       int
	upconverters map[int]upconverter
}

func defaultSchemas() schemas {
	return schemas{
		newest:       spend.EventSchemaVersion,
		upconverters: map[int]upconverter{1: decodeV1},
	}
}

func decodeV1(raw json.RawMessage) (spend.Event, error) {
	var event spend.Event
	err := json.Unmarshal(raw, &event)
	return event, err
}

func (s *schemas) accept(version int, convert upconverter) {
	s.upconverters = maps.Clone(s.upconverters)
	s.upconverters[version] = convert
	s.newest = max(s.newest, version)
}

func (s schemas) decode(payload []byte) (int, spend.Event, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return 0, spend.Event{}, err
	}
	version := cmp.Or(header.SchemaVersion, 1)
	if version > s.newest {
		return version, spend.Event{}, errNewerSchema
	}
	if version < 1 {
		return version, spend.Event{}, fmt.Errorf("unknown schema version %d", version)
	}
	convert, ok := s.upconverters[version]
	if !ok {
		return version, spend.Event{}, errNoUpconverter
	}
	event, err := convert(payload)
	return version, event, err
}

type heldEvents struct {
	warnings *logThrottle
	alerts   *logThrottle

	mu    sync.Mutex
	n     int
	since time.Time
}

func (h *heldEvents) add(now time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == 0 {
		h.since = now
	}
	h.n++
	return h.n
}

func (h *heldEvents) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

func (h *heldEvents) oldest() (since time.Time, n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.since, h.n
}

func (g *Governor) hold(version int, cause error, now time.Time) {
	n := g.held.add(now)
	g.metrics.Counter(metricEventsHeld, 1)
	if ok, suppressed := g.held.warnings.admit(strconv.Itoa(version), now); ok {
		g.logger.Warn("spend event held: this governor cannot read its schema; a governor that can will file it",
			"schemaVersion", version, "newestSchemaVersion", g.schemas.newest, "held", n, "error", cause, "suppressed", suppressed)
	}
}

func (g *Governor) alertIfHeldTooLong(now time.Time) {
	since, n := g.held.oldest()
	if n == 0 || now.Sub(since) < g.timing.outageAlertAfter {
		return
	}
	if ok, suppressed := g.held.alerts.admit("held", now); ok {
		g.logger.Error("spend events are held unfiled and filing stops once the unacked window fills: the platform is older than the components publishing spend; upgrade it",
			"held", n, "elapsed", now.Sub(since), "newestSchemaVersion", g.schemas.newest, "suppressed", suppressed)
	}
}
