// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func withSchemaVersion(t *testing.T, e spend.Event, version int) []byte {
	t.Helper()
	e.SchemaVersion = 0
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if version != 0 {
		fields["schema_version"] = mustMarshal(t, version)
	}
	return mustMarshal(t, fields)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestGovernor_FilesAnEventWithNoSchemaVersionAsVersionOne(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)

	r.publish(t, withSchemaVersion(t, e, 0))

	row := waitForRow(t, r.db, e.ID)
	assertCosts(t, row, spend.CostOf(e.Tokens, r.models.flatFor))
}

func TestGovernor_FilesAVersionOneEventAsPublished(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.SchemaVersion = 1
	e.AgentName, e.ToolName, e.SessionID = "yaml-agent", "search", "session-1"

	r.publishEvent(t, e)

	row := waitForRow(t, r.db, e.ID)
	if deref(row.AgentName) != "yaml-agent" || deref(row.UserSub) != "user-1" ||
		deref(row.ModelConfigID) != r.models.flat.ID || row.CompletedTime != e.CompletedTime {
		t.Errorf("version 1 event not filed as published: %+v", row)
	}
	assertCosts(t, row, spend.CostOf(e.Tokens, r.models.flatFor))
	if d := waitForDetail(t, r.db, e.ID); deref(d.ToolName) != "search" || deref(d.SessionID) != "session-1" {
		t.Errorf("version 1 details = %+v, want the tool and session published", d)
	}
}

const heldMsg = "spend event held: this governor cannot read its schema; a governor that can will file it"

func TestGovernor_HoldsANewerSchemaUnsettledForTheGovernorThatKnowsIt(t *testing.T) {
	logs := &logCapture{}
	d := &deliveries{}
	r := startRig(t, recordDeliveries(d), func(cfg *Config) { cfg.Logger = logs.logger() })
	newer := newEvent(r.models.flat.Alias)
	newerPayload := withSchemaVersion(t, newer, spend.EventSchemaVersion+1)
	current := newEvent(r.models.flat.Alias)

	r.publish(t, newerPayload)
	r.publishEvent(t, current)
	waitForRow(t, r.db, current.ID)
	eventually(t, "the current event to be acked", func() bool {
		list := d.snapshot()
		return len(list) == 2 && list[1].acked.Load()
	})

	held := d.snapshot()[0]
	if held.acked.Load() || held.nacked.Load() {
		t.Errorf("newer event acked=%t nacked=%t, want neither: it waits for a newer governor", held.acked.Load(), held.nacked.Load())
	}
	if readRow(t, r.db, newer.ID) != nil {
		t.Error("an event in a newer schema was filed by a governor that does not know it")
	}
	line := logLine(t, logs, heldMsg)
	for _, want := range []string{"level=WARN", "schemaVersion=2", "newestSchemaVersion=1", "held=1"} {
		if !strings.Contains(line, want) {
			t.Errorf("hold log is missing %q:\n%s", want, line)
		}
	}

	if err := r.governor.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	next := &deliveries{}
	successor := New(Config{
		Namespace: testNamespace,
		Broker:    &tappedBroker{Broker: r.broker, tap: next.tap},
		Ledger:    store.NewSpendLedgerRepo(r.db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(r.db), discardLogger),
		Logger:    discardLogger,
	})
	successor.setTiming(fastTiming)
	successor.schemas.accept(spend.EventSchemaVersion+1, decodeV1)
	if err := successor.Start(t.Context()); err != nil {
		t.Fatalf("start the successor: %v", err)
	}
	t.Cleanup(func() { _ = successor.Stop(t.Context()) })
	r.publish(t, newerPayload)

	waitForRow(t, r.db, newer.ID)
	eventually(t, "the successor to ack the newer event", func() bool { return next.allAcked(1) })
}

func TestWriteLoop_EscalatesEventsHeldPastTheOutageAlertToAnError(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	quickAlert := fastTiming
	quickAlert.outageAlertAfter = 20 * time.Millisecond
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), quickAlert)
	newer := newEvent("")
	msg := acks.pending(newer).msg
	msg.Payload = withSchemaVersion(t, newer, spend.EventSchemaVersion+1)

	g.enqueue(t.Context())(msg)
	g.enqueue(t.Context())(msg)
	runWriteLoop(t.Context(), t, g)

	eventually(t, "the hold to be escalated", func() bool { return strings.Contains(logs.String(), heldAlertMsg) })
	if line := logLine(t, logs, heldAlertMsg); !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "held=2") {
		t.Errorf("want an ERROR counting both held events:\n%s", line)
	}
	if n := strings.Count(logs.String(), heldMsg); n != 1 {
		t.Errorf("hold warnings = %d, want 1 within the interval", n)
	}
	if acks.total() != 0 || acks.totalNacks() != 0 {
		t.Errorf("held events acked %d, nacked %d times; want neither", acks.total(), acks.totalNacks())
	}
}

const heldAlertMsg = "spend events are held unfiled and filing stops once the unacked window fills: the platform is older than the components publishing spend; upgrade it"

func TestWriteLoop_EscalatesHeldEventsWhileRetryingALoneRefusedEvent(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	quickAlert := fastTiming
	quickAlert.outageAlertAfter = 20 * time.Millisecond
	refused := newEvent("")
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error { return errInvalidText }}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), quickAlert)
	runWriteLoop(t.Context(), t, g, acks.pending(refused))
	eventually(t, "the lone event to be held", func() bool {
		return strings.Contains(logs.String(), "holding it until another event files")
	})

	newer := newEvent("")
	msg := acks.pending(newer).msg
	msg.Payload = withSchemaVersion(t, newer, spend.EventSchemaVersion+1)
	g.enqueue(t.Context())(msg)

	eventually(t, "the schema hold to be escalated", func() bool { return strings.Contains(logs.String(), heldAlertMsg) })
	if acks.total() != 0 {
		t.Errorf("%d events acked; want both still unfiled", acks.total())
	}
}

func TestSchemas_DropsAVersionNoUpconverterReads(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	e := newEvent("")
	msg := acks.pending(e).msg
	msg.Payload = withSchemaVersion(t, e, -1)

	g.enqueue(t.Context())(msg)

	if acks.count(e.ID) != 1 {
		t.Error("an event in no known schema was not acked away")
	}
	if line := logLine(t, logs, "spend event dropped: not decodable"); !strings.Contains(line, "schemaVersion=-1") {
		t.Errorf("want the drop to name the version:\n%s", line)
	}
}

func TestDefaultSchemas_RegistersAnUpconverterForEveryVersionUpToTheNewest(t *testing.T) {
	s := defaultSchemas()

	for version := 1; version <= spend.EventSchemaVersion; version++ {
		if s.upconverters[version] == nil {
			t.Errorf("no upconverter for schema version %d: a bump must register the reader for its own version", version)
		}
	}
}

func TestSchemas_HoldsAnEventAtAKnownVersionNoUpconverterReads(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	g.schemas.accept(spend.EventSchemaVersion+2, decodeV1)
	skipped := spend.EventSchemaVersion + 1
	e := newEvent("")
	msg := acks.pending(e).msg
	msg.Payload = withSchemaVersion(t, e, skipped)

	g.enqueue(t.Context())(msg)

	if acks.count(e.ID) != 0 || acks.nackCount(e.ID) != 0 {
		t.Errorf("event acked %d, nacked %d times; want neither: a build with the reader will file it", acks.count(e.ID), acks.nackCount(e.ID))
	}
	if len(g.pending) != 0 {
		t.Error("an event with no upconverter was handed to the writer")
	}
	line := logLine(t, logs, heldMsg)
	for _, want := range []string{"level=WARN", fmt.Sprintf("schemaVersion=%d", skipped), "held=1", "no upconverter"} {
		if !strings.Contains(line, want) {
			t.Errorf("hold log is missing %q:\n%s", want, line)
		}
	}
}
