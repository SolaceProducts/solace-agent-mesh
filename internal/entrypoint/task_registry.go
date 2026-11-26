// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

type TaskInfo struct {
	AgentName string
	UserID    string
	SessionID string
	TraceID   string
	Metadata  any

	WaitingRequestID string

	StartedAt int64

	TaskOrigin

	Model usagefacts.Model

	runReported bool
}

type TaskOrigin struct {
	Trigger string

	SystemInitiated bool

	MachineIngress bool

	Surface string

	AgentType string

	AnalyticsSessionID string
}

type TaskIdentity struct {
	TraceID   string
	SessionID string
	Origin    TaskOrigin
}

func (t TaskIdentity) withoutPartialOrigin() TaskIdentity {
	if t.Origin.Trigger == "" {
		t.Origin = TaskOrigin{}
	}
	return t
}

type TaskRegistry interface {
	Register(taskID, agentName, userID string, identity TaskIdentity) error

	SetMetadata(taskID string, metadata any)

	Metadata(taskID string) any

	SetRunModel(taskID string, model usagefacts.Model)

	Get(taskID string) *TaskInfo

	Holds(taskID string) bool

	SetWaiting(taskID, requestID string)
	ClearWaiting(taskID, requestID string)

	ClaimRunReport(taskID string) *TaskInfo

	AgentName(taskID string) string

	RunState(taskID string) (running, known bool)

	IsRunning(taskID string) bool

	Remove(taskID string)

	AllByAgent(agentName string) []string
}

type memoryTaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*TaskInfo
}

func NewMemoryTaskRegistry() TaskRegistry {
	return &memoryTaskRegistry{tasks: make(map[string]*TaskInfo)}
}

func (r *memoryTaskRegistry) Register(taskID, agentName, userID string, identity TaskIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.tasks[taskID]
	if !ok {
		info = &TaskInfo{AgentName: agentName, UserID: userID, StartedAt: time.Now().UnixMilli()}
		r.tasks[taskID] = info
	}
	applyIdentityWhileEmpty(info, identity.withoutPartialOrigin())
	return nil
}

func (r *memoryTaskRegistry) SetMetadata(taskID string, metadata any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.tasks[taskID]; ok {
		info.Metadata = metadata
	}
}

func (r *memoryTaskRegistry) Metadata(taskID string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if info, ok := r.tasks[taskID]; ok {
		return info.Metadata
	}
	return nil
}

func applyIdentityWhileEmpty(info *TaskInfo, identity TaskIdentity) {
	if info.TraceID == "" && identity.TraceID != "" {
		info.TraceID = identity.TraceID
	}
	if info.SessionID == "" && identity.SessionID != "" {
		info.SessionID = identity.SessionID
	}
	if info.Trigger == "" && identity.Origin.Trigger != "" {
		info.TaskOrigin = identity.Origin
	}
}

func (r *memoryTaskRegistry) SetRunModel(taskID string, model usagefacts.Model) {
	if model == (usagefacts.Model{}) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.tasks[taskID]
	if !ok {
		return
	}
	info.Model = usagefacts.SettleModel(info.Model, model)
}

func (r *memoryTaskRegistry) Get(taskID string) *TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.tasks[taskID]
	if !ok {
		return nil
	}
	cp := *info
	return &cp
}

func (r *memoryTaskRegistry) Holds(taskID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tasks[taskID]
	return ok
}

func (r *memoryTaskRegistry) SetWaiting(taskID, requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.tasks[taskID]; ok {
		info.WaitingRequestID = requestID
	}
}

func (r *memoryTaskRegistry) ClearWaiting(taskID, requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.tasks[taskID]; ok && info.WaitingRequestID == requestID {
		info.WaitingRequestID = ""
	}
}

func (r *memoryTaskRegistry) AgentName(taskID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if info, ok := r.tasks[taskID]; ok {
		return info.AgentName
	}
	return ""
}

func (r *memoryTaskRegistry) RunState(taskID string) (running, known bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tasks[taskID]
	return ok, true
}

func (r *memoryTaskRegistry) IsRunning(taskID string) bool {
	running, _ := r.RunState(taskID)
	return running
}

func (r *memoryTaskRegistry) ClaimRunReport(taskID string) *TaskInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.tasks[taskID]
	if !ok || info.runReported {
		return nil
	}
	info.runReported = true
	cp := *info
	return &cp
}

func (r *memoryTaskRegistry) Remove(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, taskID)
}

func (r *memoryTaskRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tasks)
}

func (r *memoryTaskRegistry) AllByAgent(agentName string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for id, info := range r.tasks {
		if info.AgentName == agentName {
			ids = append(ids, id)
		}
	}
	return ids
}
