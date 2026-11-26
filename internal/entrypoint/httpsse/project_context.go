// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func (h *messageHandler) accessibleProject(ctx context.Context, projectID, userID string) (*store.Project, error) {
	if h.projectRepo == nil {
		return nil, nil
	}
	project, err := h.projectRepo.GetByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, nil
	}
	if artifact.SameOwner(project.UserID, userID) {
		return project, nil
	}
	if svc := h.core.ProjectSharing(); svc != nil {
		hasAccess, err := svc.HasViewAccess(ctx, projectID, userID)
		if err != nil {
			return nil, err
		}
		if hasAccess {
			return project, nil
		}
	}
	return nil, nil
}

func injectProjectContext(msg *a2a.Message, project *store.Project) {
	if project == nil {
		return
	}
	framing := buildProjectContext(project)
	for i := range msg.Parts {
		if msg.Parts[i].Kind == "text" {
			msg.Parts[i].Text = framing + "\n\nUSER QUERY:\n" + msg.Parts[i].Text
			return
		}
	}
	msg.Parts = append([]a2a.Part{a2a.NewTextPart(framing)}, msg.Parts...)
}

func buildProjectContext(project *store.Project) string {
	parts := []string{fmt.Sprintf("You are working in the project workspace: %q", project.Name)}
	if project.SystemPrompt != nil {
		if sp := strings.TrimSpace(*project.SystemPrompt); sp != "" {
			parts = append(parts, "\n"+sp)
		}
	}
	if project.Description != nil {
		if d := strings.TrimSpace(*project.Description); d != "" {
			parts = append(parts, "\nProject Description: "+d)
		}
	}
	return strings.Join(parts, "\n")
}
