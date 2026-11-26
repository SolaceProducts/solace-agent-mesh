// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

var withheldStops []func(*GatewayExecutor) error

func (e *GatewayExecutor) stopWithheld() []error {
	errs := make([]error, 0, len(withheldStops))
	for _, stop := range withheldStops {
		errs = append(errs, stop(e))
	}
	return errs
}

func (e *GatewayExecutor) WithheldState(key any, newState func() any) any {
	if state, ok := e.withheld.Load(key); ok {
		return state
	}
	state, _ := e.withheld.LoadOrStore(key, newState())
	return state
}
