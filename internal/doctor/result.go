// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
)

type Status string

const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

var statusOrder = map[Status]int{
	StatusPass: 0,
	StatusSkip: 1,
	StatusWarn: 2,
	StatusFail: 3,
}

func WorstStatus(s []Status) Status {
	worst := StatusPass
	for _, x := range s {
		if statusOrder[x] > statusOrder[worst] {
			worst = x
		}
	}
	return worst
}

type CheckResult struct {
	Name   string
	Status Status
	Detail string
	Reason string
}

func (r CheckResult) Validate() error {
	if r.Status != StatusPass && r.Reason == "" {
		return fmt.Errorf("doctor: check %q with status %s has no reason", r.Name, r.Status)
	}
	return nil
}

type Check interface {
	Name() string

	DisplayName() string

	Run(ctx context.Context, env Env, doctorCtx Context) CheckResult
}
