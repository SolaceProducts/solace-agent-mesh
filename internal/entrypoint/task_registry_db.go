// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

const dbTaskRegistryTimeout = 5 * time.Second
const writeAttempts = 2

type dbTaskRegistry struct {
	repo   *store.TaskRecordRepo
	logger *slog.Logger

	mu             sync.RWMutex
	own            map[string]*TaskInfo
	metadata       map[string]any
	pendingDeletes map[string]struct{}
	pendingClears  map[string]string

	onEndedElsewhere func(taskIDs []string)
}

type heldTask struct {
	id      string
	waiting bool
	taken   bool
}

func NewDBTaskRegistry(repo *store.TaskRecordRepo, logger *slog.Logger) TaskRegistry {
	return &dbTaskRegistry{
		repo:           repo,
		logger:         logger.With("logger_name", "task-registry"),
		own:            make(map[string]*TaskInfo),
		metadata:       make(map[string]any),
		pendingDeletes: make(map[string]struct{}),
		pendingClears:  make(map[string]string),
	}
}

func (r *dbTaskRegistry) boundedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbTaskRegistryTimeout)
}

func (r *dbTaskRegistry) records() *store.TaskRecordRepo { return r.repo }

func (r *dbTaskRegistry) writeWithRetry(write func(context.Context) error) error {
	var err error
	for range writeAttempts {
		ctx, cancel := r.boundedContext()
		err = write(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
	}
	return err
}

func (r *dbTaskRegistry) sweepLive(ctx context.Context) {
	r.retryFailedWrites(ctx)
	snapshot := r.localSnapshot()
	var held, metadataOnly []string
	for id, info := range snapshot {
		if info != nil {
			held = append(held, id)
		} else {
			metadataOnly = append(metadataOnly, id)
		}
	}
	slices.Sort(held)
	slices.Sort(metadataOnly)
	if r.sweepBatches(ctx, held, snapshot, r.repo.StampSeen) {
		r.sweepBatches(ctx, metadataOnly, snapshot, r.repo.ExistingIDs)
	}
}

func (r *dbTaskRegistry) sweepBatches(ctx context.Context, ids []string, snapshot map[string]*TaskInfo,
	query func(context.Context, []string) ([]string, error)) bool {
	for batch := range slices.Chunk(ids, dbutil.InClauseCap) {
		queryCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
		found, err := query(queryCtx, batch)
		cancel()
		if err != nil {
			r.logger.Warn("task record liveness sweep failed; keeping local entries", "error", err)
			return false
		}
		present := make(map[string]struct{}, len(found))
		for _, id := range found {
			present[id] = struct{}{}
		}
		r.dropMissing(batch, present, snapshot)
	}
	return true
}

func (r *dbTaskRegistry) takeAll(ctx context.Context, instanceID string) ([]heldTask, bool) {
	r.retryFailedWrites(ctx)
	snapshot := r.localSnapshot()
	queryCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
	rows, err := r.repo.TakeAll(queryCtx, instanceID)
	cancel()
	if err != nil {
		r.logger.Warn("taking the app's task records failed; keeping local entries", "error", err)
		return nil, false
	}

	r.mu.Lock()
	held := make([]heldTask, 0, len(rows))
	present := make(map[string]struct{}, len(rows))
	for i := range rows {
		if task, ok := r.holdLocked(&rows[i]); ok {
			held = append(held, task)
			present[task.id] = struct{}{}
		}
	}
	r.mu.Unlock()

	ids := make([]string, 0, len(snapshot))
	for id := range snapshot {
		ids = append(ids, id)
	}
	r.dropMissing(ids, present, snapshot)
	return held, true
}

func (r *dbTaskRegistry) take(taskID, instanceID string) *TaskInfo {
	ctx, cancel := r.boundedContext()
	defer cancel()
	row, err := r.repo.Take(ctx, taskID, instanceID)
	if err != nil {
		r.logger.Warn("taking a task record failed", "taskID", taskID, "error", err)
		return nil
	}
	if row == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.holdLocked(row); !ok {
		return nil
	}
	cp := *r.own[taskID]
	cp.Metadata = r.metadata[taskID]
	return &cp
}

func (r *dbTaskRegistry) refreshWaiting(taskID string) {
	row, err := r.taskRecord(taskID)
	if err != nil || row == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.own[taskID]; ok {
		info.WaitingRequestID = r.waitingOnLocked(row)
	}
}

func (r *dbTaskRegistry) waitingRequest(taskID string) (requestID string, recorded bool) {
	row, err := r.taskRecord(taskID)
	if err != nil {
		return "", false
	}
	if row == nil {
		return "", true
	}
	if row.WaitingRequestID == nil {
		return "", false
	}
	return *row.WaitingRequestID, true
}

func (r *dbTaskRegistry) holdLocked(row *store.TaskRecord) (heldTask, bool) {
	if _, pending := r.pendingDeletes[row.TaskID]; pending {
		return heldTask{}, false
	}
	info, ok := r.own[row.TaskID]
	if ok {
		info.WaitingRequestID = r.waitingOnLocked(row)
	} else {
		info = taskInfoFromRecord(row)
		info.WaitingRequestID = r.waitingOnLocked(row)
		r.own[row.TaskID] = info
	}
	return heldTask{id: row.TaskID, waiting: info.WaitingRequestID != "", taken: !ok}, true
}

func (r *dbTaskRegistry) waitingOnLocked(row *store.TaskRecord) string {
	waiting := row.WaitingOn()
	if waiting != "" && r.pendingClears[row.TaskID] == waiting {
		return ""
	}
	return waiting
}

func (r *dbTaskRegistry) taskRecord(taskID string) (*store.TaskRecord, error) {
	ctx, cancel := r.boundedContext()
	defer cancel()
	row, err := r.repo.Get(ctx, taskID)
	if err != nil {
		r.logger.Warn("task record read failed", "taskID", taskID, "error", err)
		return nil, err
	}
	return row, nil
}

func (r *dbTaskRegistry) retryFailedWrites(ctx context.Context) {
	r.mu.RLock()
	deletes := make([]string, 0, len(r.pendingDeletes))
	for id := range r.pendingDeletes {
		deletes = append(deletes, id)
	}
	clears := make(map[string]string, len(r.pendingClears))
	for id, requestID := range r.pendingClears {
		clears[id] = requestID
	}
	r.mu.RUnlock()
	for _, id := range deletes {
		r.mu.RLock()
		_, pending := r.pendingDeletes[id]
		r.mu.RUnlock()
		if !pending {
			continue
		}
		deleteCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
		err := r.repo.Delete(deleteCtx, id)
		cancel()
		if err != nil {
			r.logger.Warn("task record delete failed again; retried on the next sweep", "taskID", id, "error", err)
			continue
		}
		r.mu.Lock()
		delete(r.pendingDeletes, id)
		r.mu.Unlock()
	}
	for id, requestID := range clears {
		clearCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
		_, err := r.repo.ClearWaiting(clearCtx, id, requestID)
		cancel()
		if err != nil {
			r.logger.Warn("task record waiting flag clear failed again; retried on the next sweep", "taskID", id, "error", err)
			continue
		}
		r.mu.Lock()
		if r.pendingClears[id] == requestID {
			delete(r.pendingClears, id)
		}
		r.mu.Unlock()
	}
}

func (r *dbTaskRegistry) localSnapshot() map[string]*TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := make(map[string]*TaskInfo, len(r.own)+len(r.metadata))
	for id, info := range r.own {
		snapshot[id] = info
	}
	for id := range r.metadata {
		if _, ok := snapshot[id]; !ok {
			snapshot[id] = nil
		}
	}
	return snapshot
}

func (r *dbTaskRegistry) dropMissing(ids []string, present map[string]struct{}, snapshot map[string]*TaskInfo) {
	r.mu.Lock()
	dropped := 0
	var endedElsewhere []string
	for _, id := range ids {
		if _, ok := present[id]; ok {
			continue
		}
		if info, ok := r.own[id]; ok && info == snapshot[id] {
			delete(r.own, id)
		}
		if _, ok := r.own[id]; !ok {
			if _, had := r.metadata[id]; had {
				delete(r.metadata, id)
				endedElsewhere = append(endedElsewhere, id)
			}
			dropped++
		}
	}
	r.mu.Unlock()
	if dropped > 0 {
		r.logger.Debug("dropped local entries of tasks ended elsewhere", "count", dropped)
	}
	if len(endedElsewhere) > 0 && r.onEndedElsewhere != nil {
		r.onEndedElsewhere(endedElsewhere)
	}
}

func (r *dbTaskRegistry) Register(taskID, agentName, userID string, identity TaskIdentity) error {
	identity = identity.withoutPartialOrigin()
	rec := &store.TaskRecord{
		TaskID:    taskID,
		AgentName: agentName,
		UserID:    userID,
		TraceID:   identity.TraceID,
		SessionID: identity.SessionID,
	}
	applyOrigin(rec, identity.Origin)

	ctx, cancel := r.boundedContext()
	defer cancel()
	row, err := r.repo.Upsert(ctx, rec)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.own[taskID] = taskInfoFromRecord(row)
	delete(r.pendingDeletes, taskID)
	return nil
}

func (r *dbTaskRegistry) Holds(taskID string) bool {
	return r.owns(taskID)
}

func (r *dbTaskRegistry) SetWaiting(taskID, requestID string) {
	if err := r.writeWithRetry(func(ctx context.Context) error { return r.repo.SetWaiting(ctx, taskID, requestID) }); err != nil {
		r.logger.Warn("task record waiting flag write failed", "taskID", taskID, "error", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.own[taskID]; ok {
		info.WaitingRequestID = requestID
	}
}

func (r *dbTaskRegistry) ClearWaiting(taskID, requestID string) {
	err := r.writeWithRetry(func(ctx context.Context) error {
		_, err := r.repo.ClearWaiting(ctx, taskID, requestID)
		return err
	})
	if err != nil {
		r.logger.Warn("task record waiting flag clear failed; retried on the next sweep", "taskID", taskID, "error", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil && requestID != "" {
		r.pendingClears[taskID] = requestID
	}
	if info, ok := r.own[taskID]; ok && info.WaitingRequestID == requestID {
		info.WaitingRequestID = ""
	}
}

func (r *dbTaskRegistry) SetMetadata(taskID string, metadata any) {
	if !r.owns(taskID) && !r.IsRunning(taskID) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadata[taskID] = metadata
}

func (r *dbTaskRegistry) owns(taskID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.own[taskID]
	return ok
}

func (r *dbTaskRegistry) Metadata(taskID string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metadata[taskID]
}

func (r *dbTaskRegistry) SetRunModel(taskID string, model usagefacts.Model) {
	if model == (usagefacts.Model{}) {
		return
	}
	ctx, cancel := r.boundedContext()
	defer cancel()
	if err := r.repo.SettleModel(ctx, taskID, model); err != nil {
		r.logger.Warn("task record settle model failed", "taskID", taskID, "error", err)
	}
}

func (r *dbTaskRegistry) Get(taskID string) *TaskInfo {
	if info := r.ownCopy(taskID); info != nil {
		return info
	}
	rec, err := r.taskRecord(taskID)
	if err != nil {
		return nil
	}
	info := taskInfoFromRecord(rec)
	if info != nil {
		info.Metadata = r.Metadata(taskID)
	}
	return info
}

func (r *dbTaskRegistry) ownCopy(taskID string) *TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.own[taskID]
	if !ok {
		return nil
	}
	cp := *info
	cp.Metadata = r.metadata[taskID]
	return &cp
}

func (r *dbTaskRegistry) ClaimRunReport(taskID string) *TaskInfo {
	ctx, cancel := r.boundedContext()
	defer cancel()
	rec, err := r.repo.ClaimRunReport(ctx, taskID)
	if err != nil {
		r.logger.Warn("task record claim run report failed", "taskID", taskID, "error", err)
		return nil
	}
	info := taskInfoFromRecord(rec)
	if info == nil {
		return nil
	}
	if own := r.ownCopy(taskID); own != nil {
		if info.Trigger == "" && own.Trigger != "" {
			info.TaskOrigin = own.TaskOrigin
			r.logger.Warn("task record origin missing on the row; using this instance's copy", "taskID", taskID)
		}
		if info.TraceID == "" {
			info.TraceID = own.TraceID
		}
		if info.SessionID == "" {
			info.SessionID = own.SessionID
		}
	}
	return info
}

func (r *dbTaskRegistry) AgentName(taskID string) string {
	if info := r.Get(taskID); info != nil {
		return info.AgentName
	}
	return ""
}

func (r *dbTaskRegistry) RunState(taskID string) (running, known bool) {
	ctx, cancel := r.boundedContext()
	defer cancel()
	running, err := r.repo.Exists(ctx, taskID)
	if err != nil {
		r.logger.Warn("task record exists check failed", "taskID", taskID, "error", err)
		return false, false
	}
	return running, true
}

func (r *dbTaskRegistry) IsRunning(taskID string) bool {
	running, known := r.RunState(taskID)
	return running || !known
}

func (r *dbTaskRegistry) Remove(taskID string) {
	err := r.writeWithRetry(func(ctx context.Context) error { return r.repo.Delete(ctx, taskID) })
	if err != nil {
		r.logger.Warn("task record delete failed; retried on the next sweep",
			"taskID", taskID, "traceID", r.traceIDOf(taskID), "error", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.pendingDeletes[taskID] = struct{}{}
	}
	delete(r.pendingClears, taskID)
	delete(r.own, taskID)
	delete(r.metadata, taskID)
}

func (r *dbTaskRegistry) traceIDOf(taskID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if info, ok := r.own[taskID]; ok {
		return info.TraceID
	}
	return ""
}

func (r *dbTaskRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.own)
}

func (r *dbTaskRegistry) AllByAgent(agentName string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for id, info := range r.own {
		if info.AgentName == agentName {
			ids = append(ids, id)
		}
	}
	return ids
}

func applyOrigin(rec *store.TaskRecord, origin TaskOrigin) {
	rec.Trigger = origin.Trigger
	rec.SystemInitiated = origin.SystemInitiated
	rec.MachineIngress = origin.MachineIngress
	rec.Surface = origin.Surface
	rec.AgentType = origin.AgentType
	rec.AnalyticsSessionID = origin.AnalyticsSessionID
}

func taskInfoFromRecord(rec *store.TaskRecord) *TaskInfo {
	if rec == nil {
		return nil
	}
	return &TaskInfo{
		AgentName:        rec.AgentName,
		UserID:           rec.UserID,
		SessionID:        rec.SessionID,
		TraceID:          rec.TraceID,
		WaitingRequestID: rec.WaitingOn(),
		StartedAt:        rec.CreatedAt,
		TaskOrigin: TaskOrigin{
			Trigger:            rec.Trigger,
			SystemInitiated:    rec.SystemInitiated,
			MachineIngress:     rec.MachineIngress,
			Surface:            rec.Surface,
			AgentType:          rec.AgentType,
			AnalyticsSessionID: rec.AnalyticsSessionID,
		},
		Model: usagefacts.Model{
			Family:     rec.ModelFamily,
			Provider:   rec.ModelProvider,
			ServedFrom: rec.ModelServedFrom,
		},
		runReported: rec.RunReported,
	}
}
