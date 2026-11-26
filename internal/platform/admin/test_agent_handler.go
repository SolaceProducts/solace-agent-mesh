// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
)

type testAgentHandler struct {
	svc    *TestAgentService
	logger *slog.Logger
}

type CreateTestAgentRequest struct {
	ComponentName string `json:"componentName" validate:"required,safename"`
	ComponentKind string `json:"componentKind" validate:"required,oneof=agent workflow skill a2a_agent"`
}

func (h *testAgentHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if err := validatePathUUID(sessionID); err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}

	var req CreateTestAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if err := validateRequest(&req); err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}

	userID := userIDFromRequest(r)
	resp, err := h.svc.CreateForSession(r.Context(), sessionID, req.ComponentName, req.ComponentKind, userID)
	if err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *testAgentHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if err := validatePathUUID(sessionID); err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}

	userID := userIDFromRequest(r)
	resp, err := h.svc.GetForSession(r.Context(), sessionID, userID)
	if err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}
	if resp == nil {
		handleServiceError(w, &NotFoundError{Resource: "test-agent", ID: sessionID})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *testAgentHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if err := validatePathUUID(sessionID); err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}

	userID := userIDFromRequest(r)
	if err := h.svc.DeleteForSession(r.Context(), sessionID, userID); err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type LookupTestAgentParentResponse struct {
	BuilderSessionID string `json:"builderSessionId"`
	ComponentName    string `json:"componentName"`
	ComponentKind    string `json:"componentKind,omitempty"`
	TestAgentName    string `json:"testAgentName"`
	TestAgentID      string `json:"testAgentId"`
}

func (h *testAgentHandler) handleLookupParent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		handleServiceError(w, &ValidationError{Message: "name query parameter is required"})
		return
	}
	if len(name) > 256 {
		handleServiceError(w, &ValidationError{Message: "name query parameter exceeds 256 characters"})
		return
	}

	userID := userIDFromRequest(r)
	parent, err := h.svc.LookupParentByTestAgentName(r.Context(), name, userID)
	if err != nil {
		handleServiceErrorWithCause(w, err)
		return
	}
	if parent == nil {
		handleServiceError(w, &NotFoundError{Resource: "test-agent", ID: name})
		return
	}
	writeJSON(w, http.StatusOK, LookupTestAgentParentResponse{
		BuilderSessionID: parent.BuilderSessionID,
		ComponentName:    parent.ComponentName,
		ComponentKind:    parent.ComponentKind,
		TestAgentName:    parent.TestAgentName,
		TestAgentID:      parent.TestAgentInstanceID,
	})
}
