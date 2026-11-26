// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "context"

func (s *GatewayService) setEmailInboundTestFnForTest(fn func(ctx context.Context, values map[string]any) (*TestConnectionResponse, error)) {
	s.inboundTestFn = fn
}

func (s *GatewayService) setEmailOutboundTestFnForTest(fn func(ctx context.Context, values map[string]any) (*TestConnectionResponse, error)) {
	s.outboundTestFn = fn
}
