// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	gwa2ui "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const (
	defaultPromptTimeout = 45 * time.Minute

	sourceToolApproval = "tool_approval"
	sourceAskQuestion  = "ask_user_question"
)

const (
	maxPromptQuestions     = 4
	maxPromptOptions       = 4
	maxQuestionHeaderChars = 12
	maxPromptTextBytes     = 2048
	maxApprovalArgs        = 64
	maxApprovalArgsBytes   = 64 << 10
)

type openPrompt struct {
	taskID    string
	connID    string
	approval  bool
	questions int
	timer     *time.Timer
}

func (s *agentState) handlePrompt(ctx context.Context, c *conn, f inFrame) {
	refuse := func(code, message string) {
		refused := errorFrame{Type: typeError, Code: code, Message: message, Ref: boundedRef(f.TaskID)}
		if validTopicLevel(f.RequestID) {
			refused.RequestID = f.RequestID
		}
		c.send(refused)
	}
	if !validTopicLevel(f.RequestID) {
		refuse(errCodeBadMessage, "requestId must be a short identifier")
		return
	}
	if code, problem := promptShapeProblem(f); problem != "" {
		refuse(code, problem)
		return
	}
	timeout := s.promptTimeout(f.TimeoutSeconds)

	s.mu.Lock()
	b, refusal := s.openTaskLocked(c, typePrompt, f.TaskID)
	if b == nil {
		s.mu.Unlock()
		refuse(errCodeUnknownTask, refusal)
		return
	}
	if b.cancelled {
		s.mu.Unlock()
		refuse(errCodeForbidden, "the task was cancelled")
		return
	}
	if _, asked := s.asks[f.RequestID]; asked {
		s.mu.Unlock()
		refuse(errCodeBadMessage, "a prompt with this requestId is open")
		return
	}
	if len(s.asks)+s.heldLocked(typePromptAnswer) >= maxOpenPrompts {
		s.mu.Unlock()
		refuse(errCodeLimit, "too many open prompts")
		return
	}
	run := s.runLocked(f.TaskID, b)
	shown := canPromptUser(a2a.DecodeUserProperties(b.props))
	requestID := f.RequestID
	asked := &openPrompt{
		taskID:    f.TaskID,
		connID:    c.id,
		approval:  f.Approval != nil,
		questions: len(f.Questions),
		timer:     time.AfterFunc(timeout, func() { s.closePrompt(requestID, answerTimedOut) }),
	}
	s.asks[requestID] = asked
	s.mu.Unlock()

	if !shown {
		s.a.logger.Debug("agent link answered a prompt its task's channel cannot show", "agentName", s.cfg.Name, "taskID", f.TaskID, "requestID", requestID)
		s.closePrompt(requestID, answerCanceled)
		return
	}
	source, surface, err := s.promptSurface(f, timeout)
	if err != nil {
		s.withdrawPrompt(requestID, asked)
		refuse(errCodeBadMessage, "the prompt could not be shown")
		return
	}
	s.boundedPublish(ctx, func(ctx context.Context) {
		run.sp.PublishUserInputRequest(ctx, a2a.UserInputRequestData{
			Type:      string(a2a.SignalUserInputRequest),
			RequestID: requestID,
			ExpiresAt: s.a.now().Add(timeout).UTC().Format(time.RFC3339),
			Source:    source,
			Surface:   surface,
		})
	})
}

func (s *agentState) withdrawPrompt(requestID string, asked *openPrompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asks[requestID] == asked {
		delete(s.asks, requestID)
		asked.timer.Stop()
	}
}

func (s *agentState) promptTimeout(seconds float64) time.Duration {
	timeout := time.Duration(seconds * float64(time.Second))
	if timeout <= 0 {
		timeout = defaultPromptTimeout
	}
	return min(timeout, s.a.cfg.TaskTTL)
}

func promptShapeProblem(f inFrame) (string, string) {
	if (f.Approval == nil) == (len(f.Questions) == 0) {
		return errCodeBadMessage, "a prompt carries exactly one of approval or questions"
	}
	if a := f.Approval; a != nil {
		if f.Message != "" {
			return errCodeBadMessage, "an approval's message goes in approval.message"
		}
		if a.Tool == "" || len(a.Tool) > maxNameBytes {
			return errCodeBadMessage, "an approval names its tool, in at most 256 bytes"
		}
		if len(a.Args) > maxApprovalArgsBytes {
			return errCodeLimit, "approval arguments are at most 65536 bytes"
		}
		args, isObject := objectMembers(a.Args)
		if !isObject {
			return errCodeBadMessage, "approval arguments must be a JSON object"
		}
		if args > maxApprovalArgs {
			return errCodeLimit, "an approval shows at most 64 arguments"
		}
		return "", ""
	}
	if len(f.Questions) > maxPromptQuestions {
		return errCodeLimit, "a prompt asks at most 4 questions"
	}
	for _, q := range f.Questions {
		if q.Question == "" {
			return errCodeBadMessage, "every question has its text"
		}
		if len(q.Options) > maxPromptOptions {
			return errCodeLimit, "a question offers at most 4 options"
		}
		if len(q.Question) > maxPromptTextBytes || utf8.RuneCountInString(q.Header) > maxQuestionHeaderChars {
			return errCodeBadMessage, "a question's text is at most 2048 bytes and its header 12 characters"
		}
		for _, o := range q.Options {
			if len(o.Label) > maxPromptTextBytes || len(o.Description) > maxPromptTextBytes {
				return errCodeBadMessage, "an option's label and description are at most 2048 bytes each"
			}
		}
	}
	return "", ""
}

func objectMembers(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return 0, true
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return 0, false
	}
	members, depth := 0, 0
	inString, escaped := false, false
	for _, b := range trimmed {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
			if depth == 1 && members == 0 {
				members = 1
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 1 {
				members++
			}
		}
	}
	return members, true
}

func (s *agentState) promptSurface(f inFrame, timeout time.Duration) (string, json.RawMessage, error) {
	if a := f.Approval; a != nil {
		args, err := decodeArgs(a.Args)
		if err != nil {
			return "", nil, err
		}
		surface, err := hilsurface.Generate(hilsurface.Request{
			RequestID:      f.RequestID,
			ToolName:       redactedText(a.Tool),
			RequesterKind:  hilsurface.RequesterAgent,
			RequesterName:  s.displayName(),
			ToolArgs:       args,
			LiteralMessage: redactedText(a.Message),
			ShowArgs:       true,
			Timeout:        timeout,
		})
		return sourceToolApproval, surface, err
	}
	questions := make([]any, 0, len(f.Questions))
	for _, q := range f.Questions {
		options := make([]any, 0, len(q.Options))
		for _, o := range q.Options {
			options = append(options, map[string]any{"label": redactedText(o.Label), "description": redactedText(o.Description)})
		}
		questions = append(questions, map[string]any{"question": redactedText(q.Question), "header": redactedText(q.Header),
			"options": options, "multiSelect": q.MultiSelect})
	}
	form := map[string]any{"questions": questions}
	if f.Message != "" {
		form["message"] = redactedText(f.Message)
	}
	surface, err := gwa2ui.QuestionSurface(f.RequestID, form)
	return sourceAskQuestion, surface, err
}

func redactedText(text string) string {
	redacted, _ := hilsurface.RedactValue(text).(string)
	return redacted
}

func (s *agentState) takeAnswer(ctx context.Context, req jsonrpcEnvelope, props a2a.UserProperties, requestID string) bool {
	resp := s.userInputResponse(req.Params, requestID)
	if resp == nil {
		return false
	}
	s.mu.Lock()
	p := s.asks[requestID]
	if p == nil {
		s.mu.Unlock()
		relayed, refused := s.router.RelayUserInputResponse(ctx, s.ns(), resp, props.UserID)
		return relayed || refused
	}
	b := s.tasks[p.taskID]
	if b == nil {
		s.mu.Unlock()
		s.a.logger.Debug("agent link dropped an answer to a prompt whose task is gone", "agentName", s.cfg.Name, "taskID", p.taskID, "requestID", requestID)
		return true
	}
	if !artifact.SameOwner(b.props[a2a.PropUserID], props.UserID) {
		s.mu.Unlock()
		s.a.logger.Warn("agent link refused an answer from someone other than the task's user", "agentName", s.cfg.Name, "taskID", p.taskID, "requestID", requestID)
		return true
	}
	delete(s.asks, requestID)
	p.timer.Stop()
	if !b.final && !b.cancelled {
		b.deadline = s.a.now().Add(s.a.cfg.TaskTTL)
	}
	target := s.promptConnLocked(p)
	s.mu.Unlock()
	s.sendOrHold(target, p.taskID, answerFrame(requestID, p, resp.Status, resp.Data))
	return true
}

func (s *agentState) closePrompt(requestID, status string) {
	s.mu.Lock()
	p := s.asks[requestID]
	if p == nil {
		s.mu.Unlock()
		return
	}
	delete(s.asks, requestID)
	p.timer.Stop()
	target := s.promptConnLocked(p)
	s.mu.Unlock()
	s.sendOrHold(target, p.taskID, answerFrame(requestID, p, status, nil))
}

func (s *agentState) promptConnLocked(p *openPrompt) *conn {
	return s.askersConnLocked(p.connID, p.taskID)
}

func (s *agentState) askersConnLocked(connID, taskID string) *conn {
	if c := s.conns[connID]; c != nil && !c.isClosed() {
		return c
	}
	if b := s.tasks[taskID]; b != nil {
		return s.liveConnLocked(b.connID)
	}
	return s.liveConnLocked("")
}

func answerFrame(requestID string, p *openPrompt, status string, data map[string]any) outFrame {
	if status != answerAnswered && status != answerTimedOut {
		status = answerCanceled
	}
	f := outFrame{Type: typePromptAnswer, TaskID: p.taskID, RequestID: requestID, Status: status}
	if p.approval {
		approved, _ := data["approved"].(bool)
		approved = approved && status == answerAnswered
		f.Approved = &approved
		return f
	}
	if status == answerAnswered {
		f.Answers = make([]string, p.questions)
		for i := range f.Answers {
			if gwa2ui.HasQuestionAnswer(data, i) {
				f.Answers[i] = gwa2ui.QuestionAnswer(data, i)
			}
		}
	}
	return f
}

func (s *agentState) userInputResponse(params json.RawMessage, requestID string) *a2a.UserInputResponseData {
	facts, ok := s.a.readRequest(params)
	if !ok {
		return nil
	}
	for _, part := range facts.message.Parts {
		data, _ := part.Data.(map[string]any)
		if part.Kind != "data" || data["type"] != string(a2a.SignalUserInputResponse) {
			continue
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil
		}
		var resp a2a.UserInputResponseData
		if json.Unmarshal(raw, &resp) != nil || resp.RequestID != requestID {
			return nil
		}
		return &resp
	}
	return nil
}
