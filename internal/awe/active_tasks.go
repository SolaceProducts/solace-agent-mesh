// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

type activeTaskReporter interface {
	ActiveTaskCounts() map[a2a.ActiveTaskKey]int
}

func (e *Executor) ActiveTaskCounts() map[a2a.ActiveTaskKey]int {
	totals := map[a2a.ActiveTaskKey]int{}
	for _, inst := range e.Instances() {
		reporter, ok := inst.(activeTaskReporter)
		if !ok {
			continue
		}
		for key, n := range reporter.ActiveTaskCounts() {
			totals[key] += n
		}
	}
	return totals
}

func (e *Executor) ActiveTaskCountsByAgent() map[string]int {
	totals := map[string]int{}
	for _, inst := range e.Instances() {
		if inst.Kind() != a2a.TaskKindAgent {
			continue
		}
		reporter, ok := inst.(activeTaskReporter)
		if !ok {
			continue
		}
		name := inst.Name()
		for _, n := range reporter.ActiveTaskCounts() {
			totals[name] += n
		}
	}
	return totals
}
