// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func (s *Service) FeedbackRows(ctx context.Context, vis store.FeedbackVisibility, f store.CrossUserFeedbackFilter, limit, offset int) ([]ActivityFeedbackRow, int, error) {
	rows, total, err := s.feedbackRepo.SearchCrossUser(ctx, vis, f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return s.activityFeedbackRows(rows), total, nil
}

func (s *Service) SubjectsWithFeedback(ctx context.Context, ids []string, vis store.FeedbackVisibility) (map[string]bool, error) {
	return s.feedbackRepo.SubjectsWithFeedback(ctx, ids, vis)
}

func (s *Service) activityFeedbackRows(rows []*store.Feedback) []ActivityFeedbackRow {
	out := make([]ActivityFeedbackRow, 0, len(rows))
	for _, f := range rows {
		out = append(out, ActivityFeedbackRow{
			ID:             f.ID,
			TaskID:         f.TaskID,
			UserID:         f.UserID,
			Rating:         f.Rating,
			Comment:        f.Comment,
			Subject:        s.subjectRef(f.SubjectID, f.SubjectType),
			EntrypointType: f.EntrypointType,
			UpdatedAt:      epochMsToISO8601(f.UpdatedAt),
		})
	}
	return out
}
