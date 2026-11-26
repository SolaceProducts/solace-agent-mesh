// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package entrypoint

type SubmitRejection struct {
	TraceID   string
	Surface   string
	Reason    string
	SessionID string
}

type analyticsWiring struct{}

func (w *analyticsWiring) register(*GatewayCore) {
}

func (g *GatewayCore) ReportSubmitRejected(SubmitRejection) {
}

func (g *GatewayCore) runAgentType(string) string { return "" }
