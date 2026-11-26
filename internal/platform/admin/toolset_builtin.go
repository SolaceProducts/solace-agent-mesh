// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "context"

func (s *ToolsetService) GetBuiltin(ctx context.Context, id string) (*ToolsetResponse, error) {
	for _, b := range visibleBuiltinToolsets(s.featureRegistry) {
		if b.ID == id {
			one := []ToolsetResponse{b}
			s.attachBuiltinToolsetAgents(ctx, one)
			return &one[0], nil
		}
	}
	return nil, &NotFoundError{Resource: "built-in toolset", ID: id}
}
