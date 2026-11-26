// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	runAsKey    = "runAs"
	runAsColumn = "run_as"
	enabledKey  = "enabled"
)

const msgRunAsVerifyFailed = "runAs could not be verified; try again"

func requestAuthor(r *http.Request) *auth.Subject {
	author, _ := auth.SubjectFromContext(r.Context())
	return author
}

func (h *scheduledTaskHandler) checkRunAsWrite(w http.ResponseWriter, r *http.Request, runAs string) bool {
	err := h.service.CheckRunAsWrite(r.Context(), requestAuthor(r), runAs)
	switch {
	case err == nil:
		return true
	case errors.Is(err, entrypoint.ErrRunAsForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, entrypoint.ErrRunAsDisabled),
		errors.Is(err, entrypoint.ErrRunAsUnresolvable),
		errors.Is(err, entrypoint.ErrRunAsUnknownPrincipal),
		errors.Is(err, entrypoint.ErrRunAsPlatformTooOld),
		errors.Is(err, auth.ErrInvalidScheduledRunAs):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Warn("scheduled task runAs verification failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, msgRunAsVerifyFailed)
	}
	return false
}

func (h *scheduledTaskHandler) createRunAs(w http.ResponseWriter, r *http.Request, runAs *string) (stored *string, ok bool) {
	if runAs == nil || *runAs == "" {
		return nil, true
	}
	if !h.checkRunAsWrite(w, r, *runAs) {
		return nil, false
	}
	return runAs, true
}

func (h *scheduledTaskHandler) applyRunAsUpdate(w http.ResponseWriter, r *http.Request, task *store.ScheduledTask, raw map[string]json.RawMessage, fields map[string]any) bool {
	rawRunAs, present := raw[runAsKey]
	if present {
		var runAs *string
		if err := json.Unmarshal(rawRunAs, &runAs); err != nil {
			writeError(w, http.StatusBadRequest, "runAs must be a string or null")
			return false
		}
		if runAs == nil || *runAs == "" {
			fields[runAsColumn] = nil
			return true
		}
		if !h.checkRunAsWrite(w, r, *runAs) {
			return false
		}
		fields[runAsColumn] = runAs
		return true
	}
	if !editsBeyondEnabled(raw) {
		return true
	}
	if err := entrypoint.CheckRunAsTaskEdit(requestAuthor(r), task); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return false
	}
	return true
}

func editsBeyondEnabled(raw map[string]json.RawMessage) bool {
	for key := range raw {
		if key != enabledKey {
			return true
		}
	}
	return false
}
