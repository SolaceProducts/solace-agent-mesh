// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	OutboxEvalFilesPrefix = "eval.files."
	eventEvalFilesDelete  = OutboxEvalFilesPrefix + "delete"
)

const uploadSettleDelay = 10 * time.Minute

type fileDeletes struct {
	Runs             []string                `json:"runs,omitempty"`
	Sessions         []string                `json:"sessions,omitempty"`
	Files            []store.EvalExampleFile `json:"files,omitempty"`
	UnlessReferenced bool                    `json:"unlessReferenced,omitempty"`
}

func (d fileDeletes) empty() bool {
	return len(d.Runs) == 0 && len(d.Sessions) == 0 && len(d.Files) == 0
}

func fileDeleteEvent(deleted store.EvalFileDeletes) (*store.OutboxEvent, error) {
	deletes := fileDeletes{Runs: deleted.RunIDs, Files: deleted.ExampleFiles}
	for _, trial := range deleted.TrialSnapshots {
		if session := legacySnapshotSession(trial); session != "" {
			deletes.Sessions = append(deletes.Sessions, session)
		}
	}
	return newFileDeleteEvent(deletes, 0)
}

func legacySnapshotSession(trial store.EvalTrialSnapshot) string {
	var metas []snapshotArtifactMeta
	if json.Unmarshal([]byte(trial.ArtifactsMetadata), &metas) != nil || len(metas) == 0 {
		return ""
	}
	for _, m := range metas {
		if m.SessionID != "" {
			return ""
		}
	}
	return evalArtifactSnapshotSessionID(trial.RunID, trial.ExampleID, trial.TrialIndex)
}

func newFileDeleteEvent(deletes fileDeletes, delay time.Duration) (*store.OutboxEvent, error) {
	if deletes.empty() {
		return nil, nil
	}
	body, err := json.Marshal(deletes)
	if err != nil {
		return nil, fmt.Errorf("marshal file deletes: %w", err)
	}
	event := &store.OutboxEvent{
		EventType:   eventEvalFilesDelete,
		AggregateID: idgen.New(),
		Payload:     string(body),
	}
	if delay > 0 {
		event.NextAttemptAt = time.Now().Add(delay).UnixMilli()
	}
	return event, nil
}

type fileDeleter struct {
	storage   *StorageService
	artifacts *artifact.ScopedService
	rows      *store.EvalExampleArtifactRepo
}

func (d *fileDeleter) delete(ctx context.Context, deletes fileDeletes) error {
	var errs []error
	for _, runID := range deletes.Runs {
		if d.storage.Available() {
			if _, err := d.storage.DeleteRunData(ctx, runID); err != nil {
				errs = append(errs, err)
			}
		}
		errs = append(errs, d.deleteSession(ctx, evalRunSnapshotSessionID(runID)))
	}
	for _, session := range deletes.Sessions {
		errs = append(errs, d.deleteSession(ctx, session))
	}
	for _, f := range deletes.Files {
		errs = append(errs, d.deleteFile(ctx, f, deletes.UnlessReferenced))
	}
	return errors.Join(errs...)
}

func (d *fileDeleter) deleteSession(ctx context.Context, session string) error {
	if d.artifacts == nil {
		return nil
	}
	loc := d.artifacts.Resolve("", EvalRunnerUserID, session, "")
	keys, err := d.artifacts.Inner.ListKeys(ctx, artifact.KeyScope{AppName: loc.AppName, UserID: loc.UserID, SessionID: loc.SessionID})
	if err != nil {
		return fmt.Errorf("list session %s: %w", session, err)
	}
	var errs []error
	for _, key := range keys {
		if _, userScoped := artifact.ResolveFilename(key); userScoped {
			continue
		}
		if err := d.artifacts.Inner.Delete(ctx, d.artifacts.Resolve("", EvalRunnerUserID, session, key)); err != nil {
			errs = append(errs, fmt.Errorf("delete %s in session %s: %w", key, session, err))
		}
	}
	return errors.Join(errs...)
}

func (d *fileDeleter) deleteFile(ctx context.Context, f store.EvalExampleFile, unlessReferenced bool) error {
	if d.artifacts == nil {
		return nil
	}
	if unlessReferenced && d.rows != nil {
		referenced, err := d.rows.SessionReferenced(ctx, f.SessionID)
		if err != nil {
			return err
		}
		if referenced {
			return nil
		}
	}
	var errs []error
	for _, name := range []string{f.Filename, f.Filename + artifact.MetadataSuffix} {
		if err := d.artifacts.Inner.Delete(ctx, d.artifacts.Resolve("", EvalRunnerUserID, f.SessionID, name)); err != nil {
			errs = append(errs, fmt.Errorf("delete %s in session %s: %w", name, f.SessionID, err))
		}
	}
	return errors.Join(errs...)
}

type FileDeletes struct {
	deleter       fileDeleter
	attemptBudget time.Duration
}

func NewFileDeletes(storage *StorageService, artifacts *artifact.ScopedService, rows *store.EvalExampleArtifactRepo) *FileDeletes {
	return &FileDeletes{deleter: fileDeleter{storage: storage, artifacts: artifacts, rows: rows}, attemptBudget: fileDeleteAttemptBudget}
}

func (f *FileDeletes) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	var deletes fileDeletes
	if err := json.Unmarshal([]byte(event.Payload), &deletes); err != nil {
		return fmt.Errorf("eval file deletes: invalid payload for event %s: %w", event.ID, err)
	}
	ctx, cancel := context.WithTimeout(ctx, f.attemptBudget)
	defer cancel()
	return f.deleter.delete(ctx, deletes)
}

const fileDeleteAttemptBudget = 30 * time.Second

var _ platpkg.OutboxPublisher = (*FileDeletes)(nil)

func (f *FileDeletes) event() store.EvalFileDeleteEvent {
	if f == nil {
		return nil
	}
	return fileDeleteEvent
}

func uploadEvent(location store.EvalExampleFile) store.EvalFileDeleteEvent {
	return func(store.EvalFileDeletes) (*store.OutboxEvent, error) {
		return newFileDeleteEvent(fileDeletes{Files: []store.EvalExampleFile{location}, UnlessReferenced: true}, uploadSettleDelay)
	}
}
