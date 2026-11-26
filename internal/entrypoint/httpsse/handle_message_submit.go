// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type submitClient struct {
	surface  string
	session  string
	timezone string
	bearer   string
}

func submitClientOf(r *http.Request) submitClient {
	return submitClient{
		surface:  httputil.ClientSurface(r),
		session:  httputil.ClientSession(r),
		timezone: httputil.UserTimezone(r),
		bearer:   bearerToken(r),
	}
}

type submitInput struct {
	reqID   string
	traceID string
	message MessageDTO
	userID  string
	caller  *auth.Subject
	client  submitClient
}

type rejection struct {
	status int
	code   int
	msg    string
	reason string
}

type submitRefusal struct {
	rejection
	retryAt    time.Time
	unanswered bool
}

func refusal(rej rejection) *submitRefusal { return &submitRefusal{rejection: rej} }

func abandoned() *submitRefusal { return &submitRefusal{unanswered: true} }

func (h *messageHandler) reportRefusal(traceID string, client submitClient, reason string) {
	h.core.ReportSubmitRejected(entrypoint.SubmitRejection{TraceID: traceID, Surface: client.surface, Reason: reason, SessionID: client.session})
}

func (h *messageHandler) rejectSubmit(w http.ResponseWriter, r *http.Request, id, traceID string, rej rejection) {
	h.reportRefusal(traceID, submitClientOf(r), rej.reason)
	writeJSON(w, rej.status, newRejectedResponse(id, rej.code, rej.msg, traceID))
}

func (h *messageHandler) answerRefusal(w http.ResponseWriter, in submitInput, ref *submitRefusal) {
	if ref.unanswered {
		return
	}
	if !ref.retryAt.IsZero() {
		budget.SetRetryAfter(w, ref.retryAt, time.Now())
	}
	h.reportRefusal(in.traceID, in.client, ref.reason)
	writeJSON(w, ref.status, newRejectedResponse(in.reqID, ref.code, ref.msg, in.traceID))
}

const submitFailedMsg = "failed to submit task"

func (h *messageHandler) resolveSubmitSession(ctx context.Context, traceID, contextID, userID string) (*store.Session, *submitRefusal) {
	if h.sessRepo == nil {
		return nil, nil
	}
	session, err := h.sessRepo.GetByIDIncludingDeleted(ctx, contextID)
	if err != nil {
		if ctx.Err() != nil {
			h.log().Debug("submit message: client gone during session lookup", "error", err, "sessionID", contextID, "userID", userID, "traceID", traceID)
			return nil, abandoned()
		}
		h.log().Error("submit message: look up session", "error", err, "sessionID", contextID, "userID", userID, "traceID", traceID)
		return nil, refusal(rejection{status: http.StatusInternalServerError, code: codeInternalError, msg: submitFailedMsg, reason: taxonomy.RejectInternal})
	}
	if session != nil && session.UserID != userID {
		h.log().Warn("submit message: session belongs to another user", "sessionID", contextID, "userID", userID, "traceID", traceID)
		return nil, refusal(rejection{status: http.StatusNotFound, code: codeInvalidParams, msg: sessionNotFoundMsg, reason: taxonomy.RejectForbidden})
	}
	if session != nil && session.DeletedAt != nil {
		return nil, nil
	}
	return session, nil
}

func (h *messageHandler) submit(ctx context.Context, in submitInput) (TaskResult, *submitRefusal) {
	traceID, userID, message := in.traceID, in.userID, in.message

	if ref := h.refuseControlResponseParts(message.Parts, userID, traceID); ref != nil {
		return TaskResult{}, ref
	}

	requested, _ := message.Metadata["agent_name"].(string)
	if requested == "" {
		return TaskResult{}, refusal(rejection{status: http.StatusBadRequest, code: codeInvalidParams, msg: "metadata.agent_name is required", reason: taxonomy.RejectMalformed})
	}
	card := h.core.AgentRegistry().FindByConfiguredName(requested)
	if card == nil {
		return TaskResult{}, refusal(rejection{status: http.StatusNotFound, code: codeAgentNotFound, msg: "agent not found: " + requested, reason: taxonomy.RejectAgentNotFound})
	}
	agentName := card.Name
	if agentName != requested {
		message.Metadata["agent_name"] = agentName
	}

	taskID := idgen.New()
	contextID := message.ContextID
	var existingSession *store.Session
	if contextID == "" {
		contextID = idgen.New()
	} else {
		parsed, err := uuid.Parse(contextID)
		if err != nil {
			return TaskResult{}, refusal(rejection{status: http.StatusBadRequest, code: codeInvalidParams, msg: "contextId must be a UUID", reason: taxonomy.RejectMalformed})
		}
		contextID = parsed.String()
		session, ref := h.resolveSubmitSession(ctx, traceID, contextID, userID)
		if ref != nil {
			return TaskResult{}, ref
		}
		existingSession = session
	}
	messageID := message.MessageID
	if messageID == "" {
		messageID = idgen.New()
	}

	msg := convertMessage(message, taskID, contextID, messageID)

	if ref := h.authorizeArtifactParts(ctx, msg, userID, traceID); ref != nil {
		return TaskResult{}, ref
	}

	h.sseManager.RegisterTask(taskID, contextID, userID)

	var projectID string
	if existingSession != nil && existingSession.ProjectID != nil {
		projectID = *existingSession.ProjectID
	}
	if projectID == "" {
		if pid, _ := message.Metadata["project_id"].(string); pid != "" {
			if _, err := uuid.Parse(pid); err != nil {
				h.sseManager.CleanupTask(taskID)
				return TaskResult{}, refusal(rejection{status: http.StatusBadRequest, code: codeInvalidParams, msg: "project_id must be a UUID", reason: taxonomy.RejectMalformed})
			}
			projectID = pid
		}
	}

	stripClientOwnedMetadata(&msg)

	var projectOwnerID string
	if projectID != "" {
		ownerID, allowed, err := resolveProjectOwner(ctx, h.projectRepo, h.core.ProjectSharing(), projectID, userID)
		switch {
		case err != nil:
			h.log().Warn("project access check failed; serving message without project context",
				"error", err, "taskID", taskID, "projectID", projectID)
			projectID = ""
		case !allowed:
			h.log().Info("project no longer accessible; serving message without project context",
				"taskID", taskID, "projectID", projectID)
			projectID = ""
		default:
			projectOwnerID = ownerID
		}
	}

	if projectID != "" && message.ContextID == "" {
		if project, err := h.accessibleProject(ctx, projectID, userID); err != nil {
			h.log().Error("project context injection skipped", "error", err, "projectID", projectID, "userID", userID, "taskID", taskID)
		} else {
			injectProjectContext(&msg, project)
		}
	}

	dropped := entrypoint.ConvertInlineFilePartsToArtifacts(
		ctx, &msg, h.core.Services().Artifacts(),
		entrypoint.ArtifactScope{UserID: userID, SessionID: contextID}, h.log())
	entrypoint.AppendDroppedFilesNote(&msg, dropped)

	if projectID != "" {
		h.attachProjectIndex(ctx, &msg, projectContext{id: projectID, ownerID: projectOwnerID}, in, taskID, contextID)
	}

	h.maybeAttachSchedulerHistory(ctx, &msg, existingSession, contextID, userID)

	submitOpts := []entrypoint.SubmitOption{
		entrypoint.FromClientSurface(in.client.surface),
		entrypoint.FromClientSession(in.client.session),
		entrypoint.WithUserTimezone(in.client.timezone),
		entrypoint.WithTraceID(traceID),
	}
	if allowedSessionSource(msg.Metadata) == evalSessionSource {
		submitOpts = append(submitOpts, entrypoint.WithChannel(a2a.ChannelEval))
	}
	if err := h.core.SubmitTaskAttributedTo(ctx, agentName, taskID, msg, in.caller, userID, submitOpts...); err != nil {
		h.sseManager.CleanupTask(taskID)
		return TaskResult{}, h.submitFailure(err, agentName, taskID, userID, traceID)
	}

	taskLogger := h.taskLogger(taskID, agentName, contextID)

	h.ensureSessionAgent(ctx, existingSession, chatSession{
		contextID: contextID, userID: userID, agentName: agentName, projectID: projectID,
		source: allowedSessionSource(message.Metadata),
	}, taskLogger)

	return TaskResult{ID: taskID, ContextID: contextID, Kind: "task", TraceID: traceID}, nil
}

func stripClientOwnedMetadata(msg *a2a.Message) {
	delete(msg.Metadata, "project_id")

	stripForgedSchedulerSource(msg.Metadata)

	delete(msg.Metadata, "parentTaskId")

	delete(msg.Metadata, "parentSessionID")

	delete(msg.Metadata, "system_purpose")
	delete(msg.Metadata, "response_format")
}

type projectContext struct {
	id      string
	ownerID string
}

func (h *messageHandler) attachProjectIndex(ctx context.Context, msg *a2a.Message, project projectContext, in submitInput, taskID, contextID string) {
	artSvc := h.core.Services().Artifacts()
	if artSvc == nil {
		return
	}
	indexLoc := artSvc.Resolve("", project.ownerID, projectSessionPrefix+project.id, artifact.BM25IndexFilename)
	if data, _, err := artSvc.Inner.Load(ctx, indexLoc, nil); err == nil && h.indexSearchable(ctx, in.client.bearer, project, data) {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["project_id"] = project.id
	}

	if _, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.id, project.ownerID, in.userID, contextID,
		true, false,
		h.log()); err != nil {
		h.log().Error("copy project artifacts on task; serving without project context",
			"error", err, "taskID", taskID, "projectID", project.id, "sessionID", contextID)
		delete(msg.Metadata, "project_id")
	}
}

func (h *messageHandler) submitFailure(err error, agentName, taskID, userID, traceID string) *submitRefusal {
	if errors.Is(err, entrypoint.ErrAgentDelegationDenied) {
		return refusal(rejection{status: http.StatusForbidden, code: codeForbidden, msg: err.Error(), reason: taxonomy.RejectForbidden})
	}
	entrypoint.LogSubmitFailure(h.log(), slog.LevelError, err, "taskID", taskID, "agentName", agentName, "userID", userID, "traceID", traceID)
	if retryAt, ok := budget.RetryAt(err); ok {
		ref := refusal(rejection{status: http.StatusTooManyRequests, code: entrypoint.BudgetRefusalCode(err), msg: entrypoint.BudgetRefusalText(err), reason: entrypoint.SubmitFailureReason(err)})
		ref.retryAt = retryAt
		return ref
	}
	return refusal(rejection{status: http.StatusInternalServerError, code: codeInternalError, msg: submitFailedMsg, reason: taxonomy.RejectInternal})
}
