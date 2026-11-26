// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

func TestEstimatedTokensIsFourBytesPerTokenRoundedDown(t *testing.T) {
	for bytes, want := range map[int64]int64{0: 0, -8: 0, 3: 0, 4: 1, 4095: 1023, 4096: 1024} {
		if got := EstimatedTokens(bytes); got != want {
			t.Errorf("EstimatedTokens(%d) = %d, want %d", bytes, got, want)
		}
	}
}

func TestAvoidedInputCostPricesAtTheCallsAverageInputRate(t *testing.T) {
	tokens := Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}
	cost := Cost{UncachedInput: 12_000_000, Output: 7_500_000, CacheRead: 1_200_000, CacheWrite: 7_500_000}

	got, ok := AvoidedInputCost(100, tokens, cost)
	if !ok || got != 2_070_000 {
		t.Errorf("avoided cost = %d, %v; want 100 × (20,700,000 ÷ 1,000) = 2,070,000 and ok", got, ok)
	}

	uncached := Tokens{UncachedInput: 1000, Output: 50}
	if got, ok := AvoidedInputCost(100, uncached, Cost{UncachedInput: 30_000_000, Output: 1}); !ok || got != 3_000_000 {
		t.Errorf("uncached avoided cost = %d, %v; want the full input rate, 3,000,000", got, ok)
	}
	if got, ok := AvoidedInputCost(0, tokens, cost); !ok || got != 0 {
		t.Errorf("nothing avoided = %d, %v; want 0 and ok", got, ok)
	}
	if _, ok := AvoidedInputCost(100, Tokens{Output: 50}, cost); ok {
		t.Error("a call with no input tokens has no rate; want ok = false")
	}
	if got, ok := AvoidedInputCost(100, tokens, Cost{}); !ok || got != 0 {
		t.Errorf("free model = %d, %v; want 0 and ok", got, ok)
	}
}

func TestAvoidedInputCostDoesNotOverflowOnTheLargestPriceableCall(t *testing.T) {
	tokens := Tokens{UncachedInput: pricing.MaxTokensBeforeCostOverflows}
	cost := Cost{UncachedInput: 1 << 62}
	got, ok := AvoidedInputCost(pricing.MaxTokensBeforeCostOverflows, tokens, cost)
	if !ok || got != 1<<62 {
		t.Errorf("avoided cost = %d, %v; want the call's whole input cost", got, ok)
	}
}

func TestNewEventCarriesTheAttributionsAvoidedBytesOnlyWhenTheyWereCounted(t *testing.T) {
	counted := NewEvent(&llm.SpendAttribution{UserSub: "u", Surface: "agent", AvoidedInputBytes: new(int64(4096))}, "m", Tokens{}, 0)
	if counted.AvoidedInputBytes == nil || *counted.AvoidedInputBytes != 4096 {
		t.Errorf("avoided = %v, want 4096", counted.AvoidedInputBytes)
	}
	zero := NewEvent(&llm.SpendAttribution{UserSub: "u", Surface: "agent", AvoidedInputBytes: new(int64(0))}, "m", Tokens{}, 0)
	if zero.AvoidedInputBytes == nil || *zero.AvoidedInputBytes != 0 {
		t.Errorf("a counted call that withheld nothing = %v, want 0, not none", zero.AvoidedInputBytes)
	}
	uncounted := NewEvent(&llm.SpendAttribution{UserSub: "u", Surface: "agent"}, "m", Tokens{}, 0)
	if uncounted.AvoidedInputBytes != nil {
		t.Errorf("an uncounted call = %d, want none", *uncounted.AvoidedInputBytes)
	}
	if data, err := json.Marshal(uncounted); err != nil || strings.Contains(string(data), "avoided_input_bytes") {
		t.Errorf("an uncounted call's payload %s, %v; want avoided_input_bytes omitted, as an older AWE omits it", data, err)
	}
}

func TestSanitizeClearsAnAvoidedFigureItCannotFileAndKeepsTheEvent(t *testing.T) {
	for name, bytes := range map[string]int64{
		"negative bytes":                -1,
		"more bytes than can be priced": (pricing.MaxTokensBeforeCostOverflows + 1) * BytesPerEstimatedToken,
	} {
		t.Run(name, func(t *testing.T) {
			e := Event{ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: "agent", ModelName: "m",
				AvoidedInputBytes: new(bytes)}
			repairs, err := e.Sanitize(time.Now())
			if err != nil {
				t.Fatalf("Sanitize: %v", err)
			}
			if e.AvoidedInputBytes != nil || len(repairs) != 1 || !strings.Contains(repairs[0], "avoided input cleared") {
				t.Errorf("avoided = %v, repairs %v; want cleared to unmeasured with one note", e.AvoidedInputBytes, repairs)
			}
		})
	}
	for name, bytes := range map[string]int64{"a saving": 4096, "a counted call that withheld nothing": 0} {
		e := Event{ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: "agent", ModelName: "m",
			AvoidedInputBytes: new(bytes)}
		if repairs, err := e.Sanitize(time.Now()); err != nil || len(repairs) != 0 || e.AvoidedInputBytes == nil || *e.AvoidedInputBytes != bytes {
			t.Errorf("%s = %v, repairs %v, err %v; want untouched", name, e.AvoidedInputBytes, repairs, err)
		}
	}
}
