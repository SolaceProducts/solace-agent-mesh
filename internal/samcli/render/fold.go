// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"encoding/json"
	"regexp"
	"strings"
)

type Event struct {
	Seq  int64           `json:"seq,omitempty"`
	Type string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

type Part struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

type Bubble struct {
	Role         string `json:"role"`
	TaskID       string `json:"taskId,omitempty"`
	Parts        []Part `json:"parts"`
	IsError      bool   `json:"isError,omitempty"`
	IsWarning    bool   `json:"isWarning,omitempty"`
	InputRequest string `json:"inputRequest,omitempty"`
	AuthRequest  bool   `json:"authRequest,omitempty"`
	Complete     bool   `json:"complete,omitempty"`
}

const (
	roleAgent = "agent"
	roleUser  = "user"

	partText     = "text"
	partFile     = "file"
	partArtifact = "artifact"
	partData     = "data"

	statusInProgress = "in-progress"
	statusCompleted  = "completed"
	statusFailed     = "failed"

	taskTimeoutErrorCode = -32001

	workingTag = "__working"
)

var renderableDataTypes = map[string]bool{
	"deep_research_progress":  true,
	"deep_research_plan":      true,
	"compaction_notification": true,
	"redirect":                true,
	"request_build_activate":  true,
}

var artifactMarker = regexp.MustCompile(`«{1,3}artifact(?:_return)?:([^»]+)»{1,3}`)

type wirePart struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Data     map[string]any `json:"data"`
	File     *wireFile      `json:"file"`
	Metadata map[string]any `json:"metadata"`
}

type wireFile struct {
	Name string `json:"name"`
}

func hasWorkingTag(tags any) bool {
	list, _ := tags.([]any)
	for _, t := range list {
		if s, ok := t.(string); ok && strings.EqualFold(s, workingTag) {
			return true
		}
	}
	return false
}

type wireMessage struct {
	Parts    []wirePart     `json:"parts"`
	Metadata map[string]any `json:"metadata"`
}

type wireResult struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
	Final  bool   `json:"final"`
	Status struct {
		State   string       `json:"state"`
		Message *wireMessage `json:"message"`
	} `json:"status"`
}

type wireEnvelope struct {
	Result *wireResult `json:"result"`
	Error  *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

func Fold(events []Event) []Bubble {
	f := &folder{}
	for _, ev := range events {
		f.apply(ev)
	}
	return f.bubbles
}

type folder struct {
	bubbles []Bubble
}

func (f *folder) apply(ev Event) {
	var env wireEnvelope
	if err := json.Unmarshal(ev.Data, &env); err != nil {
		return
	}
	if env.Error != nil {
		f.applyError(env.Error.Code, env.Error.Message, env.Error.Data)
		return
	}
	if env.Result == nil {
		return
	}
	res := env.Result
	var (
		final  bool
		msg    *wireMessage
		taskID string
	)
	switch res.Kind {
	case "task":
		final = true
		taskID = res.ID
		if m := res.Status.Message; m != nil {
			if res.Status.State == "failed" {
				msg = m
			} else if files := filesOnly(m.Parts); len(files) > 0 {
				msg = &wireMessage{Parts: files}
			}
		}
	case "status-update":
		final = res.Final
		msg = res.Status.Message
		taskID = res.TaskID
	default:
		return
	}

	if msg != nil && f.applyDataParts(msg, taskID) {
		return
	}

	parts := renderableParts(msg)
	failed := res.Kind == "task" && res.Status.State == "failed"
	f.applyContent(parts, taskID, failed)

	if final {
		f.finish(res, taskID)
	}
}

func (f *folder) applyError(code int, message string, data map[string]any) {
	notice := code == taskTimeoutErrorCode || data["category"] == "budget_exhausted"
	if i := f.lastIndex(func(b Bubble) bool { return !b.Complete }); i >= 0 {
		f.bubbles[i].Complete = true
	}
	taskID := ""
	if i := f.lastIndex(func(b Bubble) bool { return b.TaskID != "" }); i >= 0 {
		taskID = f.bubbles[i].TaskID
	}
	f.bubbles = append(f.bubbles, Bubble{
		Role:      roleAgent,
		TaskID:    taskID,
		Parts:     []Part{{Kind: partText, Text: message}},
		IsError:   true,
		IsWarning: notice,
		Complete:  true,
	})
}

func (f *folder) applyDataParts(msg *wireMessage, taskID string) bool {
	fromPeer := msg.Metadata["forwarded_from_peer"] != nil
	for _, p := range msg.Parts {
		if p.Kind != partData || p.Data == nil {
			continue
		}
		switch str(p.Data["type"]) {
		case "artifact_creation_progress":
			if !fromPeer {
				f.artifactProgress(p.Data, taskID)
			}
			return true
		case "artifact_saved":
			if !fromPeer {
				f.setArtifactStatus(taskID, str(p.Data["filename"]), statusCompleted, false)
			}
			if !hasNonData(msg.Parts) {
				return true
			}
		case "user_input_request":
			id := str(p.Data["request_id"])
			if id != "" && p.Data["surface"] != nil && f.lastIndex(func(b Bubble) bool { return b.InputRequest == id }) < 0 {
				f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID, Parts: []Part{{Kind: partText}}, InputRequest: id})
			}
		case "authentication_required":
			f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID, Parts: []Part{{Kind: partText}}, AuthRequest: true})
		case "gateway_timeout_warning":
			f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID, Parts: []Part{{Kind: partText, Text: str(p.Data["message"])}}, IsWarning: true, Complete: true})
		}
	}
	return false
}

func (f *folder) artifactProgress(data map[string]any, taskID string) {
	name := str(data["filename"])
	status := str(data["status"])
	host := f.lastIndex(func(b Bubble) bool {
		return b.Role == roleAgent && b.InputRequest == "" && !b.AuthRequest && b.TaskID == taskID
	})
	switch status {
	case "cancelled":
		if host < 0 {
			return
		}
		kept := f.bubbles[host].Parts[:0:0]
		for _, p := range f.bubbles[host].Parts {
			if p.Kind != partArtifact || p.Name != name {
				kept = append(kept, p)
			}
		}
		if rolled := str(data["rolled_back_text"]); rolled != "" {
			kept = append(kept, Part{Kind: partText, Text: rolled})
		}
		f.bubbles[host].Parts = kept
	case statusInProgress:
		if f.setArtifactStatus(taskID, name, statusInProgress, true) {
			return
		}
		if host < 0 {
			f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID})
			host = len(f.bubbles) - 1
		}
		f.bubbles[host].Parts = append(f.bubbles[host].Parts, Part{Kind: partArtifact, Name: name, Status: statusInProgress})
	default:
		f.setArtifactStatus(taskID, name, status, true)
		if status != statusFailed && hasWorkingTag(data["tags"]) {
			f.hideArtifact(taskID, name)
		}
	}
}

func (f *folder) hideArtifact(taskID, name string) {
	for i := range f.bubbles {
		if f.bubbles[i].TaskID != taskID {
			continue
		}
		for j := range f.bubbles[i].Parts {
			if p := &f.bubbles[i].Parts[j]; p.Kind == partArtifact && p.Name == name {
				p.Hidden = true
			}
		}
	}
}

func (f *folder) setArtifactStatus(taskID, name, status string, anyStatus bool) bool {
	for i := len(f.bubbles) - 1; i >= 0; i-- {
		if f.bubbles[i].TaskID != taskID {
			continue
		}
		for j := range f.bubbles[i].Parts {
			p := &f.bubbles[i].Parts[j]
			if p.Kind == partArtifact && p.Name == name && (anyStatus || p.Status == statusInProgress) {
				p.Status = status
				return true
			}
		}
	}
	return false
}

func (f *folder) applyContent(parts []Part, taskID string, failed bool) {
	if n := len(f.bubbles); n > 0 && len(parts) > 0 {
		last := &f.bubbles[n-1]
		if last.Role == roleAgent && !last.IsWarning && last.InputRequest == "" && !last.AuthRequest && last.TaskID == taskID {
			last.Parts = append(last.Parts, parts...)
			last.IsError = last.IsError || failed
			return
		}
	}
	if failed || hasVisible(parts) {
		f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID, Parts: parts, IsError: failed})
	}
}

func (f *folder) finish(res *wireResult, taskID string) {
	for i := range f.bubbles {
		b := &f.bubbles[i]
		if b.TaskID != taskID {
			continue
		}
		for j := range b.Parts {
			if b.Parts[j].Kind == partArtifact && b.Parts[j].Status == statusInProgress {
				b.Parts[j].Status = statusFailed
				b.IsError = true
			}
		}
		b.Complete = true
	}
	if res.Kind != "task" || res.Status.State == "failed" || res.Status.Message == nil {
		return
	}
	represented := map[string]bool{}
	hasContent := false
	for _, b := range f.bubbles {
		if b.Role != roleAgent || b.TaskID != taskID {
			continue
		}
		for _, p := range b.Parts {
			if p.Name != "" {
				represented[p.Name] = true
			}
		}
		hasContent = hasContent || hasVisible(b.Parts)
	}
	if hasContent {
		return
	}
	var adopted []Part
	for _, p := range res.Status.Message.Parts {
		if p.Kind != partText {
			continue
		}
		text := artifactMarker.ReplaceAllStringFunc(p.Text, func(m string) string {
			if represented[stripVersion(artifactMarker.FindStringSubmatch(m)[1])] {
				return ""
			}
			return m
		})
		if strings.TrimSpace(text) != "" {
			adopted = append(adopted, Part{Kind: partText, Text: text})
		}
	}
	if len(adopted) == 0 {
		return
	}
	if i := f.lastIndex(func(b Bubble) bool { return b.Role == roleAgent && b.TaskID == taskID }); i >= 0 {
		f.bubbles[i].Parts = append(f.bubbles[i].Parts, adopted...)
		return
	}
	f.bubbles = append(f.bubbles, Bubble{Role: roleAgent, TaskID: taskID, Parts: adopted, Complete: true})
}

func (f *folder) lastIndex(match func(Bubble) bool) int {
	for i := len(f.bubbles) - 1; i >= 0; i-- {
		if match(f.bubbles[i]) {
			return i
		}
	}
	return -1
}

func renderableParts(msg *wireMessage) []Part {
	if msg == nil {
		return nil
	}
	research := false
	for _, p := range msg.Parts {
		if p.Kind == partData && str(p.Data["type"]) == "deep_research_progress" {
			research = true
		}
	}
	var parts []Part
	for _, p := range msg.Parts {
		switch p.Kind {
		case partText:
			if !research {
				parts = append(parts, Part{Kind: partText, Text: p.Text})
			}
		case partFile:
			name := ""
			if p.File != nil {
				name = p.File.Name
			}
			parts = append(parts, Part{Kind: partFile, Name: name, Hidden: hasWorkingTag(p.Metadata["tags"])})
		case partData:
			if t := str(p.Data["type"]); renderableDataTypes[t] {
				parts = append(parts, Part{Kind: partData, Name: t})
			}
		}
	}
	return parts
}

func filesOnly(parts []wirePart) []wirePart {
	var files []wirePart
	for _, p := range parts {
		if p.Kind == partFile {
			files = append(files, p)
		}
	}
	return files
}

func hasNonData(parts []wirePart) bool {
	for _, p := range parts {
		if p.Kind != partData {
			return true
		}
	}
	return false
}

func hasVisible(parts []Part) bool {
	for _, p := range parts {
		if (p.Kind == partText && strings.TrimSpace(p.Text) != "") || p.Kind == partFile || p.Kind == partArtifact || p.Kind == partData {
			return true
		}
	}
	return false
}

func stripVersion(name string) string {
	i := strings.LastIndex(name, ":")
	if i < 0 {
		return name
	}
	if strings.Trim(name[i+1:], "0123456789") == "" && i+1 < len(name) {
		return name[:i]
	}
	return name
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
