// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	defaultMaxDriveArtifactBytes = 25 << 20
	maxDriveArtifactsPerTask     = 5
	driveArtifactTimeout         = 60 * time.Second
	driveHandOffBudget           = 3 * time.Minute
	driveResultTimeout           = 30 * time.Second
	maxConcurrentDriveHandOffs   = 4
	driveRemoveAttempts          = 2
	driveRemoveAttemptTimeout    = 15 * time.Second
	driveRemoveBackoffDefault    = time.Second
	driveFileLinkPrefix          = "https://drive.google.com/file/d/"
	driveFileLinkSuffix          = "/view"
	maxDriveLabelRunes           = 80
	maxDriveFileNameRunes        = 255
	defaultDriveFileName         = "file"
	defaultDriveMIMEType         = "application/octet-stream"
	driveAppsMIMEPrefix          = "application/vnd.google-apps."
	msgDriveWorking              = "📎 Putting the files in Google Drive…"
)

var driveFileIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)

var driveReasonRE = regexp.MustCompile(`^[A-Za-z]{1,64}$`)

type driveHandOff struct {
	taskID, space, thread string
	email                 string
	files                 []entrypoint.SavedArtifact
	readFile              func(ctx context.Context, file entrypoint.SavedArtifact) ([]byte, error)
	held                  bool
	budget                time.Duration
}

func (w *driveHandOff) names() []string {
	if w == nil {
		return nil
	}
	return namesOf(w.files)
}

type driveOutcome struct {
	shared         []driveShared
	unshared       []string
	filesMayRemain bool
	leftFileIDs    []string
}

func (o *driveOutcome) noteLeftover(err error) {
	if fileMayRemain(err) {
		o.filesMayRemain = true
	}
	if staged, ok := errors.AsType[*driveStageError](err); ok && staged.fileRemains && staged.fileID != "" {
		o.leftFileIDs = append(o.leftFileIDs, staged.fileID)
	}
}

type driveShared struct{ name, link, fileID string }

func (o *driveOutcome) sharedFileIDs() []string {
	ids := make([]string, len(o.shared))
	for i, s := range o.shared {
		ids[i] = s.fileID
	}
	return ids
}

func (a *Adapter) planDriveHandOff(taskID string, st *taskState, q *replyQueue, saved []entrypoint.SavedArtifact, names []string) *driveHandOff {
	if a.drive == nil || a.core == nil || a.isStopping() {
		return nil
	}
	if st.snapshot().RunAsSwapped {
		a.logger.Debug("google chat artifacts not put in drive: the task ran as a service account", "taskID", taskID)
		return nil
	}
	info := a.taskRegistry.Get(taskID)
	store := a.core.Services().Artifacts()
	if info == nil || store == nil || !isEmailAddress(info.UserID) {
		a.logger.Debug("google chat artifacts not put in drive: there is no person to share them with", "taskID", taskID)
		return nil
	}
	files := driveCandidates(saved, names)
	if len(files) == 0 {
		return nil
	}
	agentName, sessionID, email, citations := info.AgentName, st.snapshot().SessionID, info.UserID, &st.citations
	return &driveHandOff{
		taskID: taskID, space: q.space, thread: q.thread, email: email, files: files,
		readFile: func(ctx context.Context, file entrypoint.SavedArtifact) ([]byte, error) {
			content, err := a.readArtifact(ctx, store, store.Resolve(agentName, email, sessionID, file.Filename), file.Version)
			if err != nil {
				return nil, err
			}
			return withResolvedCitations(citations, file, content), nil
		},
	}
}

func withResolvedCitations(citations *citationBook, file entrypoint.SavedArtifact, content []byte) []byte {
	kind := proseKindOf(file.Filename, file.MIMEType)
	if kind == notProse || !utf8.Valid(content) {
		return content
	}
	return []byte(citations.renderFile(string(content), kind == markdownProse))
}

func (a *Adapter) isStopping() bool {
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	return a.stopping
}

func driveCandidates(saved []entrypoint.SavedArtifact, names []string) []entrypoint.SavedArtifact {
	latest := make(map[string]entrypoint.SavedArtifact, len(saved))
	for _, s := range saved {
		latest[s.Filename] = s
	}
	var files []entrypoint.SavedArtifact
	seen := make(map[string]bool, len(saved))
	for _, s := range saved {
		if seen[s.Filename] {
			continue
		}
		seen[s.Filename] = true
		last := latest[s.Filename]
		if slices.Contains(last.Tags, a2a.ArtifactTagWorking) || !slices.Contains(names, last.Filename) {
			continue
		}
		files = append(files, last)
		if len(files) == maxDriveArtifactsPerTask {
			break
		}
	}
	return files
}

const logFilesNotHandedOff = "google chat drive hand-off unfinished: the adapter stopped before it finished"

func (a *Adapter) abandoned() bool { return a.abandonCtx.Err() != nil }

func (a *Adapter) abandonableContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.abandonCtx, timeout)
}

func (a *Adapter) handOffToDrive(work *driveHandOff) (abandoned bool) {
	outcome := &driveOutcome{}
	defer func() {
		if r := recover(); r != nil {
			a.logHandOffPanic(work, "drive_hand_off_post", r, outcome)
		}
	}()
	if a.abandoned() {
		a.logHandOffAbandoned(work, outcome)
		return true
	}
	a.putFilesContained(work, outcome)
	if a.abandoned() {
		a.logHandOffAbandoned(work, outcome)
		return true
	}
	a.postHandOffResult(work, outcome)
	return false
}

func (a *Adapter) logHandOffAbandoned(work *driveHandOff, outcome *driveOutcome) {
	a.logUnfinished(work.held, logFilesNotHandedOff, "taskID", work.taskID, "files", len(work.files),
		"sharedFileIDs", outcome.sharedFileIDs(), "leftFileIDs", append([]string{}, outcome.leftFileIDs...), "filesMayRemain", outcome.filesMayRemain)
}

func (a *Adapter) logHandOffPanic(work *driveHandOff, callback string, r any, outcome *driveOutcome) {
	a.logger.Error("google chat drive hand-off panicked", "taskID", work.taskID, "callback", callback,
		"panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()), "sharedFileIDs", outcome.sharedFileIDs())
}

func (a *Adapter) putFilesContained(work *driveHandOff, outcome *driveOutcome) {
	defer func() {
		if r := recover(); r != nil {
			a.logHandOffPanic(work, "drive_hand_off_upload", r, outcome)
			outcome.unshared = append(outcome.unshared, unreported(work, outcome)...)
		}
	}()
	a.putFilesInDrive(work, outcome)
}

func unreported(work *driveHandOff, outcome *driveOutcome) []string {
	var left []string
	for _, name := range work.names() {
		reported := slices.Contains(outcome.unshared, name) || slices.ContainsFunc(outcome.shared, func(s driveShared) bool { return s.name == name })
		if !reported {
			left = append(left, name)
		}
	}
	return left
}

func (a *Adapter) putFilesInDrive(work *driveHandOff, outcome *driveOutcome) {
	ctx, release := a.abandonableContext(cmp.Or(work.budget, a.driveBudget))
	defer release()
	select {
	case a.driveSlots <- struct{}{}:
		defer func() { <-a.driveSlots }()
	case <-ctx.Done():
		if !a.abandoned() {
			a.logger.Warn("google chat drive hand-off gave up waiting for its turn", "taskID", work.taskID, "files", len(work.files))
		}
		outcome.unshared = append(outcome.unshared, work.names()...)
		return
	}
	for i, file := range work.files {
		if cmp.Or(a.abandonCtx.Err(), ctx.Err()) != nil {
			if !a.abandoned() {
				a.logger.Warn("google chat drive hand-off ran out of time", "taskID", work.taskID, "filesLeft", len(work.files)-i)
			}
			outcome.unshared = append(outcome.unshared, namesOf(work.files[i:])...)
			return
		}
		shared, err := a.putInDrive(ctx, work, file)
		if err != nil {
			outcome.noteLeftover(err)
			stopCaused := a.abandoned() && errors.Is(err, context.Canceled)
			if staged, ok := errors.AsType[*driveStageError](err); !stopCaused && (!ok || !staged.logged) {
				a.logger.Warn("google chat artifact could not be shared in drive", driveWarnFields(work.taskID, err)...)
			}
			outcome.unshared = append(outcome.unshared, file.Filename)
			continue
		}
		outcome.shared = append(outcome.shared, shared)
	}
}

func driveWarnFields(taskID string, err error) []any {
	fields := []any{"taskID", taskID, "fileMayRemain", fileMayRemain(err)}
	if staged, ok := errors.AsType[*driveStageError](err); ok && staged.fileID != "" {
		fields = append(fields, "fileID", staged.fileID)
	}
	return append(fields, driveErrorFields(err)...)
}

func fileMayRemain(err error) bool {
	staged, ok := errors.AsType[*driveStageError](err)
	return ok && (staged.fileRemains || uploadOutcomeUnknown(err))
}

func uploadOutcomeUnknown(err error) bool {
	staged, ok := errors.AsType[*driveStageError](err)
	if !ok || staged.stage != "upload" {
		return false
	}
	apiErr, answered := errors.AsType[*googleapi.Error](staged.err)
	return !answered || apiErr.Code >= http.StatusInternalServerError || apiErr.Code == http.StatusRequestTimeout
}

func (a *Adapter) postHandOffResult(work *driveHandOff, outcome *driveOutcome) {
	text := handOffResultText(outcome, a.signInURL())
	if text == "" {
		return
	}
	ctx, release := a.abandonableContext(driveResultTimeout)
	defer release()
	if _, err := a.writer.create(ctx, work.space, work.thread, messageContent{Text: text}); err != nil {
		a.logger.Error("google chat reply lost: the result of a drive hand-off could not be posted",
			append([]any{"taskID", work.taskID, "shared", len(outcome.shared), "sharedFileIDs", outcome.sharedFileIDs(), "unshared", len(outcome.unshared)}, driveErrorFields(&driveStageError{stage: "post", err: err})...)...)
	}
}

func handOffResultText(outcome *driveOutcome, signInURL string) string {
	lines := make([]string, 0, len(outcome.shared)+1)
	for _, s := range outcome.shared {
		lines = append(lines, driveNotice(s.name, s.link))
	}
	if len(outcome.unshared) > 0 {
		lines = append(lines, artifactNotice(outcome.unshared, signInURL))
	}
	return strings.Join(lines, "\n")
}

func namesOf(files []entrypoint.SavedArtifact) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Filename
	}
	return names
}

type driveStageError struct {
	stage       string
	err         error
	logged      bool
	fileID      string
	fileRemains bool
}

func (e *driveStageError) Error() string { return "drive hand-off failed at " + e.stage }
func (e *driveStageError) Unwrap() error { return e.err }

func (a *Adapter) putInDrive(ctx context.Context, work *driveHandOff, file entrypoint.SavedArtifact) (driveShared, error) {
	ctx, cancel := context.WithTimeout(ctx, driveArtifactTimeout)
	defer cancel()
	content, err := work.readFile(ctx, file)
	if err != nil {
		return driveShared{}, err
	}
	if ctxErr := cmp.Or(a.abandonCtx.Err(), ctx.Err()); ctxErr != nil {
		return driveShared{}, &driveStageError{stage: "before_upload", err: ctxErr}
	}
	fileID, err := a.drive.Upload(ctx, a.cfg.ArtifactDriveFolderID, driveFileName(file.Filename), driveMIMEType(file.MIMEType), content, map[string]string{AppPropertyTaskID: work.taskID})
	if err != nil {
		return driveShared{}, &driveStageError{stage: "upload", err: err}
	}
	if !driveFileIDRE.MatchString(fileID) {
		a.logger.Error("google chat artifact left in drive: it returned a file id that cannot be used to share or remove the file", "taskID", work.taskID, "fileIDLen", len(fileID))
		return driveShared{}, &driveStageError{stage: "file_id", err: errors.New("unusable file id"), logged: true, fileRemains: true}
	}
	expires := time.Now().Add(time.Duration(a.cfg.ArtifactDriveAccessDays) * 24 * time.Hour)
	stage := "before_share"
	shareErr := cmp.Or(a.abandonCtx.Err(), ctx.Err())
	if shareErr == nil {
		stage = "share"
		shareErr = a.drive.ShareWithUser(ctx, fileID, work.email, expires)
	}
	if err := shareErr; err != nil {
		removed := a.removeFromDrive(work.taskID, fileID)
		return driveShared{}, &driveStageError{stage: stage, err: err, fileID: fileID, fileRemains: !removed}
	}
	return driveShared{name: file.Filename, link: driveFileLinkPrefix + fileID + driveFileLinkSuffix, fileID: fileID}, nil
}

func (a *Adapter) readArtifact(ctx context.Context, store *artifact.ScopedService, loc artifact.Locator, version *int) ([]byte, error) {
	limit := a.cfg.DriveMaxArtifactBytes
	meta, err := store.Inner.GetVersionMetadata(ctx, loc, version)
	if err != nil {
		return nil, &driveStageError{stage: "read", err: err}
	}
	if meta == nil {
		return nil, &driveStageError{stage: "read", err: errors.New("no version to read")}
	}
	if meta.SizeBytes > limit {
		return nil, &driveStageError{stage: "size", err: errors.New("over the limit")}
	}
	content, _, err := store.Inner.Load(ctx, loc, version)
	if err != nil {
		return nil, &driveStageError{stage: "read", err: err}
	}
	if int64(len(content)) > limit {
		return nil, &driveStageError{stage: "size", err: errors.New("over the limit")}
	}
	return content, nil
}

func (a *Adapter) removeFromDrive(taskID, fileID string) bool {
	var err error
	attempts := 0
	for attempt := 1; attempt <= driveRemoveAttempts && !a.abandoned(); attempt++ {
		attempts++
		ctx, release := a.abandonableContext(driveRemoveAttemptTimeout)
		var trashed bool
		trashed, err = a.drive.Delete(ctx, fileID)
		release()
		if driveFileGone(err) {
			a.logger.Debug("google chat drive file to remove was not found", "taskID", taskID, "fileID", fileID)
			return true
		}
		if err == nil {
			if trashed {
				a.logger.Warn("google chat drive file moved to the trash because the app could not delete it", "taskID", taskID, "fileID", fileID)
			}
			return true
		}
		if attempt < driveRemoveAttempts && !a.pauseUnlessAbandoned(a.driveRemoveBackoff) {
			break
		}
	}
	fields := []any{"taskID", taskID, "fileID", fileID, "attempts", attempts}
	if err != nil {
		fields = append(fields, driveErrorFields(&driveStageError{stage: "remove", err: err})...)
	}
	if a.abandoned() {
		a.logger.Debug("google chat drive file removal abandoned: the adapter stopped", fields...)
		return false
	}
	a.logger.Error("google chat artifact left in drive: it could not be shared or removed", fields...)
	return false
}

func driveFileGone(err error) bool {
	if failed, ok := errors.AsType[*trashFailedError](err); ok {
		return driveFileGone(failed.trashErr)
	}
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	return ok && apiErr.Code == http.StatusNotFound
}

func (a *Adapter) pauseUnlessAbandoned(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-a.abandonCtx.Done():
		return false
	}
}

func driveErrorFields(err error) []any {
	stage := "unknown"
	if staged, ok := errors.AsType[*driveStageError](err); ok {
		stage = staged.stage
		err = staged.err
	}
	fields := []any{"stage", stage}
	if failed, ok := errors.AsType[*trashFailedError](err); ok {
		fields = append(fields, causeFields(primaryKeys, failed.deleteErr)...)
		return append(fields, causeFields(trashKeys, failed.trashErr)...)
	}
	return append(fields, causeFields(primaryKeys, err)...)
}

type causeKeys struct{ cause, status, reason, op, errType string }

var (
	primaryKeys = causeKeys{cause: "cause", status: "httpStatus", reason: "reason", op: "op", errType: "errorType"}
	trashKeys   = causeKeys{cause: "trashCause", status: "trashHTTPStatus", reason: "trashReason", op: "trashOp", errType: "trashErrorType"}
)

func causeFields(keys causeKeys, err error) []any {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return []any{keys.cause, "timed out"}
	case errors.Is(err, context.Canceled):
		return []any{keys.cause, "canceled"}
	}
	if apiErr, ok := errors.AsType[*googleapi.Error](err); ok {
		reasons := make([]string, 0, len(apiErr.Errors))
		for _, item := range apiErr.Errors {
			if driveReasonRE.MatchString(item.Reason) {
				reasons = append(reasons, item.Reason)
			}
		}
		return []any{keys.status, apiErr.Code, keys.reason, strings.Join(reasons, ",")}
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return []any{keys.cause, "request failed", keys.op, urlErr.Op, keys.errType, leafTypeName(urlErr.Err)}
	}
	return []any{keys.cause, "failed", keys.errType, leafTypeName(err)}
}

func leafTypeName(err error) string {
	for inner := errors.Unwrap(err); inner != nil; inner = errors.Unwrap(err) {
		err = inner
	}
	return fmt.Sprintf("%T", err)
}

func driveNotice(name, link string) string {
	return fmt.Sprintf("📎 <%s|%s> is shared with you in Google Drive.", link, driveLabel(name))
}

func driveLabel(name string) string {
	label := strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune("<>|*_~`", r), hiddenRune(r):
			return -1
		}
		return r
	}, name)
	label = strings.TrimSpace(label)
	if runes := []rune(label); len(runes) > maxDriveLabelRunes {
		label = strings.TrimSpace(string(runes[:maxDriveLabelRunes])) + "…"
	}
	if label == "" {
		return defaultDriveFileName
	}
	return label
}

func driveFileName(name string) string {
	clean := strings.TrimSpace(strings.Map(func(r rune) rune {
		if hiddenRune(r) {
			return -1
		}
		return r
	}, name))
	if runes := []rune(clean); len(runes) > maxDriveFileNameRunes {
		clean = string(runes[:maxDriveFileNameRunes])
	}
	if clean == "" {
		return defaultDriveFileName
	}
	return clean
}

func hiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

func driveMIMEType(mimeType string) string {
	trimmed := strings.TrimSpace(mimeType)
	if trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), driveAppsMIMEPrefix) {
		return defaultDriveMIMEType
	}
	return trimmed
}

func isEmailAddress(id string) bool {
	addr, err := mail.ParseAddress(id)
	return err == nil && addr.Address == id
}
