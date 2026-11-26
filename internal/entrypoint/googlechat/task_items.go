// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	itemDestination     = "destination"
	itemStatusMsg       = "status-msg"
	itemControlsMsg     = "controls-msg"
	itemSignInMsgPrefix = "signin-msg:"
	itemPrompted        = "prompted"
	itemSavedFilePrefix = "saved-file:"
)

const maxRecordedSaves = 64

type recordedSave struct {
	entrypoint.SavedArtifact
	Seq int
}

type savedFileValue struct {
	MIMEType string   `json:"mimeType,omitempty"`
	Version  *int     `json:"version,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Seq      int      `json:"seq"`
}

const taskItemTimeout = 5 * time.Second

type taskItemStore interface {
	Put(ctx context.Context, taskID, item, value string) error
	Delete(ctx context.Context, taskID, item string) error
	List(ctx context.Context, taskID string) (map[string]string, error)
}

var _ taskItemStore = (*store.EntrypointTaskItemRepo)(nil)

type taskDestination struct {
	SpaceName    string `json:"spaceName"`
	ThreadName   string `json:"threadName,omitempty"`
	SenderName   string `json:"senderName,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	AgentName    string `json:"agentName,omitempty"`
	IsDM         bool   `json:"isDm,omitempty"`
	RunAsSwapped bool   `json:"runAsSwapped,omitempty"`
	AddOn        bool   `json:"addOn,omitempty"`
}

var errNoDestinationSpace = errors.New("google chat task destination has no space")

func (a *Adapter) writeDestination(ctx context.Context, taskID string, dest taskDestination) error {
	if dest.SpaceName == "" {
		return errNoDestinationSpace
	}
	if a.items == nil {
		return nil
	}
	value, err := json.Marshal(dest)
	if err != nil {
		return fmt.Errorf("encode google chat task destination: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	return a.items.Put(ctx, taskID, itemDestination, string(value))
}

type destinationState int

const (
	destinationUnread destinationState = iota
	destinationMissing
	destinationFound
	destinationUndecodable
)

func (a *Adapter) readTaskItems(ctx context.Context, taskID string) (taskMeta, destinationState, error) {
	if a.items == nil {
		return taskMeta{}, destinationMissing, nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	items, err := a.items.List(ctx, taskID)
	if err != nil {
		return taskMeta{}, destinationUnread, err
	}
	raw, ok := items[itemDestination]
	if !ok {
		return taskMeta{}, destinationMissing, nil
	}
	dest, err := decodeDestination(raw)
	if err != nil {
		a.logger.Debug("google chat task destination cannot be decoded", "taskID", taskID, "error", err)
		return taskMeta{}, destinationUndecodable, nil
	}
	_, prompted := items[itemPrompted]
	return taskMeta{
		taskDestination: dest,
		StatusMessage:   items[itemStatusMsg],
		ControlsMessage: items[itemControlsMsg],
		SignInMessages:  signInMessagesIn(items),
		Prompted:        prompted,
		Saved:           a.savedFilesIn(taskID, items),
	}, destinationFound, nil
}

func (a *Adapter) savedFilesIn(taskID string, items map[string]string) []recordedSave {
	var files []recordedSave
	for item, raw := range items {
		name, ok := strings.CutPrefix(item, itemSavedFilePrefix)
		if !ok || name == "" {
			continue
		}
		var v savedFileValue
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			a.logger.Warn("google chat saved-file item cannot be decoded", "taskID", taskID, "error", err)
			continue
		}
		files = append(files, recordedSave{SavedArtifact: entrypoint.SavedArtifact{Filename: name, MIMEType: v.MIMEType, Version: v.Version, Tags: v.Tags}, Seq: v.Seq})
	}
	slices.SortFunc(files, func(x, y recordedSave) int {
		if x.Seq != y.Seq {
			return x.Seq - y.Seq
		}
		return strings.Compare(x.Filename, y.Filename)
	})
	return files
}

func (a *Adapter) mergeRecordedSaves(ctx context.Context, taskID string, st *taskState) {
	if a.items == nil || st.recovered {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	items, err := a.items.List(ctx, taskID)
	if err != nil {
		a.logger.Warn("google chat saved-file items could not be read; handing over the files this replica saw saved", "taskID", taskID, "error", err)
		return
	}
	st.mergeSaved(a.savedFilesIn(taskID, items))
}

func (a *Adapter) recordSaved(taskID string, st *taskState, saves []entrypoint.SavedArtifact) {
	for _, f := range st.recordSaved(saves) {
		if a.items == nil {
			continue
		}
		value, err := json.Marshal(savedFileValue{MIMEType: f.MIMEType, Version: f.Version, Tags: f.Tags, Seq: f.Seq})
		if err != nil {
			a.logger.Warn("google chat saved-file item cannot be encoded", "taskID", taskID, "error", err)
			continue
		}
		a.putItem(taskID, itemSavedFilePrefix+f.Filename, string(value))
	}
}

func signInMessagesIn(items map[string]string) []string {
	var names []string
	for item, name := range items {
		if strings.HasPrefix(item, itemSignInMsgPrefix) && name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (a *Adapter) readSignInMessages(ctx context.Context, taskID string) ([]string, bool) {
	if a.items == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	items, err := a.items.List(ctx, taskID)
	if err != nil {
		if ctx.Err() != nil {
			a.logger.Debug("google chat sign-in links could not be read from the task's items: stopping", "taskID", taskID)
		} else {
			a.logger.Warn("google chat sign-in links could not be read from the task's items", "taskID", taskID, "error", err)
		}
		return nil, false
	}
	if _, found := items[itemDestination]; !found {
		return nil, false
	}
	return signInMessagesIn(items), true
}

func (a *Adapter) outstandingSignInLinks(ctx context.Context, taskID string, known []string, recorded *queueAnchors) ([]string, bool) {
	fromItems, ok := a.readSignInMessages(ctx, taskID)
	if !ok {
		return nil, false
	}
	outstanding := slices.Clone(fromItems)
	for _, name := range known {
		if !slices.Contains(recorded.SignIn, name) && !slices.Contains(outstanding, name) {
			outstanding = append(outstanding, name)
		}
	}
	recorded.SignIn = fromItems
	return outstanding, true
}

func decodeDestination(raw string) (taskDestination, error) {
	var dest taskDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		return taskDestination{}, fmt.Errorf("decode google chat task destination: %w", err)
	}
	if dest.SpaceName == "" {
		return taskDestination{}, errNoDestinationSpace
	}
	return dest, nil
}

func (a *Adapter) writeAnchors(taskID string, recorded, want queueAnchors) queueAnchors {
	if a.items == nil {
		return want
	}
	if want.Controls != recorded.Controls && want.Controls != "" && a.putItem(taskID, itemControlsMsg, want.Controls) {
		recorded.Controls = want.Controls
	}
	for _, name := range want.SignIn {
		if !slices.Contains(recorded.SignIn, name) && a.putItem(taskID, itemSignInMsgPrefix+name, name) {
			recorded.SignIn = append(recorded.SignIn, name)
		}
	}
	for _, name := range slices.Clone(recorded.SignIn) {
		if !slices.Contains(want.SignIn, name) && a.deleteItem(taskID, itemSignInMsgPrefix+name) {
			recorded.SignIn = slices.DeleteFunc(recorded.SignIn, func(recordedName string) bool { return recordedName == name })
		}
	}
	if len(recorded.SignIn) == 0 {
		recorded.SignIn = nil
	}
	if want.Status != recorded.Status {
		var written bool
		if want.Status == "" {
			written = a.deleteItem(taskID, itemStatusMsg)
		} else {
			written = a.putItem(taskID, itemStatusMsg, want.Status)
		}
		if written {
			recorded.Status = want.Status
		}
	}
	if want.Prompted && !recorded.Prompted && recorded.Status == want.Status && a.putItem(taskID, itemPrompted, "") {
		recorded.Prompted = true
	}
	return recorded
}

func (a *Adapter) itemWriteContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(a.ctx), taskItemTimeout)
}

func (a *Adapter) putItem(taskID, item, value string) bool {
	ctx, cancel := a.itemWriteContext()
	defer cancel()
	if err := a.items.Put(ctx, taskID, item, value); err != nil {
		a.logger.Warn("google chat task item write failed", "taskID", taskID, "item", item, "error", err)
		return false
	}
	return true
}

func (a *Adapter) deleteItem(taskID, item string) bool {
	ctx, cancel := a.itemWriteContext()
	defer cancel()
	if err := a.items.Delete(ctx, taskID, item); err != nil {
		a.logger.Warn("google chat task item delete failed", "taskID", taskID, "item", item, "error", err)
		return false
	}
	return true
}
