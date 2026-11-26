// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
)

func (s *SkillService) GetBuiltin(ctx context.Context, id string) (*SkillDetailResponse, error) {
	for _, b := range visibleBuiltinSkills(s.featureRegistry) {
		if b.ID == id {
			one := []SkillDetailResponse{b}
			s.populateBuiltinSkillAgents(ctx, one)
			return &one[0], nil
		}
	}
	return nil, &NotFoundError{Resource: "built-in skill", ID: id}
}

func (s *SkillService) ListWithBuiltins(ctx context.Context, pageNumber, pageSize int, origin string) (*PaginatedResponse[SkillDetailResponse], error) {
	var builtins []SkillDetailResponse
	if origin != "custom" {
		builtins = visibleBuiltinSkills(s.featureRegistry)
	}
	windowStart := (pageNumber - 1) * pageSize
	windowEnd := windowStart + pageSize

	paged := make([]SkillDetailResponse, 0, pageSize)
	paged = append(paged, builtins[min(windowStart, len(builtins)):min(windowEnd, len(builtins))]...)
	s.populateBuiltinSkillAgents(ctx, paged)

	total := len(builtins)
	if origin != "builtin" {
		offset := max(0, windowStart-len(builtins))
		limit := max(0, windowEnd-max(windowStart, len(builtins)))
		skills, customTotal, err := s.skillRepo.ListWithOffset(ctx, offset, limit)
		if err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		paged = append(paged, s.buildSkillResponses(ctx, skills)...)
		total += customTotal
	}
	return &PaginatedResponse[SkillDetailResponse]{Data: paged, Meta: NewPaginationMeta(pageNumber, pageSize, total)}, nil
}
