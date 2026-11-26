// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"
)

func TestSubTaskGuidanceStatesItsCost(t *testing.T) {
	lower := strings.ToLower((&subTaskTool{}).Description())

	if !strings.Contains(lower, "fresh agent loop") {
		t.Error("guidance never says a sub-task is a fresh agent loop, so the model has no way " +
			"to know it pays for one")
	}
	if !strings.Contains(lower, "system prompt") || !strings.Contains(lower, "tool definitions") {
		t.Error("guidance does not name what gets re-paid (system prompt and tool definitions), " +
			"which is the whole basis for judging whether delegating is worth it")
	}
}

func TestSubTaskGuidanceDescribesSkillsPerMode(t *testing.T) {
	description := (&subTaskTool{}).Description()
	for _, want := range []string{"from those available to you", "whole skill set", "added to the skills you already have loaded"} {
		if !strings.Contains(description, want) {
			t.Errorf("description no longer says %q about sub-task skills", want)
		}
	}
}

func TestSubTaskGuidanceSaysWhenToForkOrStartFresh(t *testing.T) {
	description := (&subTaskTool{}).Description()
	if !strings.Contains(description, "Use 'fork' when the sub-task needs this conversation") ||
		!strings.Contains(description, "'fresh' for independent work") {
		t.Error("description no longer says when to fork and when to start fresh")
	}
}

func TestSubTaskGuidanceIsStatedOnce(t *testing.T) {
	if got := (&subTaskTool{}).Instructions(); got != "" {
		t.Errorf("sub_task repeats its guidance in the system prompt: %q", got)
	}
}

func TestSubTaskGuidanceKeepsParallelFanOut(t *testing.T) {
	tool := &subTaskTool{}
	lower := strings.ToLower(tool.Description())

	for _, want := range []string{"multiple times in one response", "parallel", "each producing an artifact"} {
		if !strings.Contains(lower, want) {
			t.Errorf("description no longer conveys %q; parallel artifact generation is the case "+
				"delegation is for and must survive the cost warning", want)
		}
	}
}

func TestSubTaskGuidanceWarnsFreshSeesOnlyTheInstruction(t *testing.T) {
	tool := &subTaskTool{}
	lower := strings.ToLower(tool.Description())

	if !strings.Contains(lower, "cannot see this conversation") {
		t.Error("description does not warn that a fresh sub-task cannot see the caller's conversation")
	}
	if !strings.Contains(lower, "rediscover") {
		t.Error("description does not warn that an under-specified instruction makes the sub-task " +
			"rediscover what the caller already knows, which is how delegation silently costs more " +
			"than doing the work inline")
	}
}

func TestSubTaskGuidanceDoesNotRecommendDelegatingLookups(t *testing.T) {
	combined := strings.ToLower((&subTaskTool{}).Description())

	if strings.Contains(combined, "prefer that over doing independent lookups serially") {
		t.Error("guidance still tells the model to prefer delegating serial lookups")
	}
	if !strings.Contains(combined, "short lookup") {
		t.Error("guidance should name short lookups as work to do inline; without it the model has no " +
			"concrete floor for what is too small to delegate")
	}
	if !strings.Contains(combined, "single step") {
		t.Error("guidance should tell the model not to delegate a single step it could take itself")
	}
}
