// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

type Operation interface {
	opTag() string
}

type TextUpdateOp struct{ Text string }

func (TextUpdateOp) opTag() string { return "text_update" }

type FileUploadOp struct {
	Filename       string
	Content        []byte
	InitialComment string
	MimeType       string
}

func (FileUploadOp) opTag() string { return "file_upload" }

type MessagePostOp struct {
	Text   string
	Blocks []goslack.Block
	Notice noticeKind
}

func (MessagePostOp) opTag() string { return "message_post" }

type MessageUpdateOp struct {
	TS     string
	Text   string
	Blocks []goslack.Block
}

func (MessageUpdateOp) opTag() string { return "message_update" }

type MessageDeleteOp struct{ TS string }

func (MessageDeleteOp) opTag() string { return "message_delete" }

type ActivitySignalOp struct {
	Type string
	Data map[string]any
}

func (ActivitySignalOp) opTag() string { return "activity_signal" }

type StreamStartOp struct {
	started chan struct{}
}

func (StreamStartOp) opTag() string { return "stream_start" }

type resumeOp struct{ r *resumedQueue }

func (resumeOp) opTag() string { return "resume" }

type answerDoneOp struct{}

func (answerDoneOp) opTag() string { return "answer-done" }

type StreamFinalizeOp struct {
	ErrorText string
	Notice    noticeKind
}

func (StreamFinalizeOp) opTag() string { return "stream_finalize" }

type FinalizeOp struct {
	StatusTS string
	Text     string
	Blocks   []goslack.Block
	Notice   noticeKind
}

func (FinalizeOp) opTag() string { return "finalize" }

type ArtifactProgressOp struct {
	Status   string
	Filename string
	Text     string
	Blocks   []goslack.Block
	Notice   noticeKind
}

func (ArtifactProgressOp) opTag() string { return "artifact_progress" }

type stopSignal struct{}

func (stopSignal) opTag() string { return "stop" }

type MessageQueue struct {
	taskID    string
	channelID string
	threadTS  string

	api             slackapi.SlackAPIAdapter
	logger          *slog.Logger
	correctMarkdown bool

	ops       chan Operation
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	textWasStreamed atomic.Bool

	textBuffer    strings.Builder
	currentMsgTS  string
	artifactMsgTS map[string]string

	answerCount int
	reuseMsgTS  []string
	rewriting   bool

	dropBlocks bool

	streaming       atomic.Bool
	timeline        *activityTimeline
	recipientUserID string
	recipientTeamID string
	displayMode     goslack.TaskDisplayMode
	initialTitle    string
	streamTS        string
	streamStarted   bool
	streamLate      atomic.Bool

	record func(item, value string)
	forget func(item string)
}

func (q *MessageQueue) resume(r *resumedQueue) {
	if r == nil {
		return
	}
	q.answerCount = max(q.answerCount, len(r.answerTS))
	if len(r.answerTS) > 0 && !q.TextWasStreamed() {
		q.currentMsgTS = r.answerTS[0]
		q.reuseMsgTS = r.answerTS[1:]
		q.answerCount = len(r.answerTS)
		q.rewriting = true
		for i, ts := range r.answerTS {
			q.recordItem(answerMsgItem(i+1), ts)
		}
		q.recordItem(itemAnswerMsgTS, q.currentMsgTS)
	}
	if r.streamTS != "" {
		q.streamTS = r.streamTS
		q.streamStarted = true
		if q.timeline != nil {
			q.streaming.Store(true)
		}
	}
	for filename, ts := range r.artifacts {
		q.artifactMsgTS[filename] = ts
	}
}

func (q *MessageQueue) recordItem(item, value string) {
	if q.record != nil {
		q.record(item, value)
	}
}

func (q *MessageQueue) forgetItem(item string) {
	if q.forget != nil {
		q.forget(item)
	}
}

func (q *MessageQueue) startNewMessage() {
	if q.currentMsgTS == "" || q.rewriting {
		return
	}
	q.currentMsgTS = ""
	q.forgetItem(itemAnswerMsgTS)
}

func (q *MessageQueue) takeReusedMessage() {
	if q.currentMsgTS != "" || len(q.reuseMsgTS) == 0 {
		return
	}
	q.currentMsgTS, q.reuseMsgTS = q.reuseMsgTS[0], q.reuseMsgTS[1:]
	q.dropBlocks = false
	q.recordItem(itemAnswerMsgTS, q.currentMsgTS)
}

func (q *MessageQueue) deleteUnusedAnswerMessages() {
	if q.rewriting {
		return
	}
	for len(q.reuseMsgTS) > 0 {
		last := len(q.reuseMsgTS) - 1
		ts := q.reuseMsgTS[last]
		if err := q.deleteChat(ts); err != nil && !isMessageNotFound(err) {
			q.logger.Warn("failed to delete unused answer message", "taskID", q.taskID, "ts", ts, "error", err)
			return
		}
		q.forgetItem(answerMsgItem(q.answerCount))
		q.answerCount--
		q.reuseMsgTS = q.reuseMsgTS[:last]
	}
}

func isMessageNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message_not_found")
}

func (q *MessageQueue) EnableStreaming(timeline *activityTimeline, recipientUserID, recipientTeamID string, displayMode goslack.TaskDisplayMode, initialTitle string) {
	q.streaming.Store(true)
	q.timeline = timeline
	q.recipientUserID = recipientUserID
	q.recipientTeamID = recipientTeamID
	q.displayMode = displayMode
	q.initialTitle = initialTitle
}

func (q *MessageQueue) Streaming() bool { return q.streaming.Load() }

func (q *MessageQueue) MarkTextStreamed() { q.textWasStreamed.Store(true) }

func (q *MessageQueue) TextWasStreamed() bool { return q.textWasStreamed.Load() }

func NewMessageQueue(taskID, channelID, threadTS string, api slackapi.SlackAPIAdapter, correctMarkdown bool, logger *slog.Logger) *MessageQueue {
	return &MessageQueue{
		taskID:          taskID,
		channelID:       channelID,
		threadTS:        threadTS,
		api:             api,
		logger:          logger,
		correctMarkdown: correctMarkdown,
		ops:             make(chan Operation, 256),
		done:            make(chan struct{}),
		artifactMsgTS:   make(map[string]string),
	}
}

func (q *MessageQueue) Start() {
	q.wg.Go(q.processLoop)
}

func (q *MessageQueue) Send(op Operation) {
	q.ops <- op
}

func (q *MessageQueue) trySend(op Operation) bool {
	select {
	case q.ops <- op:
		return true
	default:
		return false
	}
}

func (q *MessageQueue) Stop(timeout time.Duration) {
	select {
	case q.ops <- stopSignal{}:
	default:
		q.closeOnce.Do(func() { close(q.done) })
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-q.done:
	case <-timer.C:
		q.logger.Error("queue stop timeout", "taskID", q.taskID)
	}
}

var flushTimeout = time.Minute

type flushOp struct{ done chan struct{} }

func (flushOp) opTag() string { return "flush" }

func (q *MessageQueue) Flush() {
	done := make(chan struct{})
	select {
	case q.ops <- flushOp{done: done}:
	case <-q.done:
		return
	}
	select {
	case <-done:
	case <-q.done:
	case <-time.After(flushTimeout):
		q.logger.Warn("queue flush timed out", "taskID", q.taskID)
	}
}

func (q *MessageQueue) WaitDrain() {
	q.Flush()
}

func (q *MessageQueue) processLoop() {
	defer q.closeOnce.Do(func() { close(q.done) })

	for {
		op, ok := <-q.ops
		if !ok {
			return
		}

		if _, isStop := op.(stopSignal); isStop {
			q.logger.Debug("queue received stop signal", "taskID", q.taskID)
			return
		}

		q.handleOp(op)
	}
}

func (q *MessageQueue) handleOp(op Operation) {
	defer func() {
		if r := recover(); r != nil {
			q.logger.Error("panic in queue processor", "taskID", q.taskID, "op", op.opTag(), "panic", r)
		}
	}()

	switch o := op.(type) {
	case TextUpdateOp:
		q.handleTextUpdate(o)
	case FileUploadOp:
		q.handleFileUpload(o)
	case MessagePostOp:
		q.handleMessagePost(o)
	case MessageUpdateOp:
		q.handleMessageUpdate(o)
	case MessageDeleteOp:
		q.handleMessageDelete(o)
	case FinalizeOp:
		q.handleFinalize(o)
	case StreamStartOp:
		q.ensureStream()
		if o.started != nil {
			close(o.started)
		}
	case ActivitySignalOp:
		q.handleActivitySignal(o)
	case StreamFinalizeOp:
		q.handleStreamFinalize(o)
	case ArtifactProgressOp:
		q.handleArtifactProgress(o)
	case resumeOp:
		q.resume(o.r)
	case answerDoneOp:
		q.deleteUnusedAnswerMessages()
	case flushOp:
		close(o.done)
	default:
		q.logger.Warn("unknown queue operation", "taskID", q.taskID, "type", op.opTag())
	}
}

func (q *MessageQueue) handleTextUpdate(op TextUpdateOp) {
	combined := op.Text
	coalesced := 0
	for {
		select {
		case next := <-q.ops:
			if tu, ok := next.(TextUpdateOp); ok {
				combined += tu.Text
				coalesced++
				continue
			}
			q.flushCoalescedText(combined, coalesced)
			q.handleOp(next)
			return
		default:
			q.flushCoalescedText(combined, coalesced)
			return
		}
	}
}

func (q *MessageQueue) flushCoalescedText(combined string, coalesced int) {
	if coalesced > 0 {
		q.logger.Debug("coalesced text updates", "taskID", q.taskID, "count", coalesced+1)
	}
	q.textBuffer.WriteString(combined)
	if q.streaming.Load() {
		return
	}
	q.flushTextBuffer()
}

func msgOptions(text string, blocks []goslack.Block) []goslack.MsgOption {
	opts := []goslack.MsgOption{goslack.MsgOptionText(text, false)}
	if len(blocks) > 0 {
		opts = append(opts, goslack.MsgOptionBlocks(blocks...))
	}
	return opts
}

func (q *MessageQueue) postChat(text string, blocks []goslack.Block) (string, error) {
	return q.postChatNotice("", text, blocks)
}

func (q *MessageQueue) postChatNotice(kind noticeKind, text string, blocks []goslack.Block) (string, error) {
	opts := append(msgOptions(text, blocks), goslack.MsgOptionTS(q.threadTS), noticeMetadata(kind))
	_, ts, err := q.api.PostMessage(context.Background(), q.channelID, opts...)
	return ts, err
}

func (q *MessageQueue) updateChat(ts, text string, blocks []goslack.Block) error {
	return q.updateChatNotice(ts, "", text, blocks)
}

func (q *MessageQueue) updateChatNotice(ts string, kind noticeKind, text string, blocks []goslack.Block) error {
	_, _, _, err := q.api.UpdateMessage(context.Background(), q.channelID, ts, append(msgOptions(text, blocks), noticeMetadata(kind))...)
	return err
}

func (q *MessageQueue) deleteChat(ts string) error {
	_, _, err := q.api.DeleteMessage(context.Background(), q.channelID, ts)
	return err
}

const slackMaxMessageRunes = 3900

const slackMaxEditBytes = 3900

const slackEmojiBytes = 48

const splitToFitMinBudget = 50

const retryMinLimit = 1000

func slackEditSize(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r == '&':
			n += len("&amp;")
		case r == '<' || r == '>':
			n += len("&lt;")
		case isSlackEmoji(r):
			n += slackEmojiBytes
		default:
			n += utf8.RuneLen(r)
		}
	}
	return n
}

func isSlackEmoji(r rune) bool {
	if r >= 0x1F000 || r == 0xFE0F {
		return true
	}
	for _, e := range defaultEmojiBelowPictographs {
		if r >= e[0] && r <= e[1] {
			return true
		}
	}
	return false
}

var defaultEmojiBelowPictographs = [][2]rune{
	{0x231A, 0x231B}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0}, {0x23F3, 0x23F3},
	{0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267F, 0x267F},
	{0x2693, 0x2693}, {0x26A1, 0x26A1}, {0x26AA, 0x26AB}, {0x26BD, 0x26BE},
	{0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4}, {0x26EA, 0x26EA},
	{0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA}, {0x26FD, 0x26FD},
	{0x2705, 0x2705}, {0x270A, 0x270B}, {0x2728, 0x2728}, {0x274C, 0x274C},
	{0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
}

func (q *MessageQueue) flushTextBuffer() {
	text := q.textBuffer.String()
	if strings.TrimSpace(text) == "" {
		return
	}
	for limit := slackMaxEditBytes; ; limit /= 2 {
		head, tail := q.splitToFit(text, limit)
		formatted, blocks := q.renderText(head)
		err := q.writeCurrentMessage(formatted, blocks)
		if isMsgTooLong(err) && limit > retryMinLimit {
			continue
		}
		if tail == "" {
			if err != nil {
				q.logger.Error("failed to write message; keeping buffer", "taskID", q.taskID, "error", err)
			}
			return
		}
		if err != nil {
			q.rewriting = false
			q.notifyDeliveryGap()
		}
		q.startNewMessage()
		q.textBuffer.Reset()
		q.textBuffer.WriteString(tail)
		q.flushTextBuffer()
		return
	}
}

func (q *MessageQueue) renderText(text string) (string, []goslack.Block) {
	if !q.correctMarkdown {
		return text, nil
	}
	formatted := ConvertMarkdown(text)
	var blocks []goslack.Block
	if tableBlocks, ok := BuildMessageBlocks(text); ok {
		blocks = tableBlocks
	}
	return formatted, blocks
}

func (q *MessageQueue) splitToFit(text string, limit int) (head, tail string) {
	budget := limit
	for {
		head, tail = splitForSlack(text, budget)
		formatted, _ := q.renderText(head)
		if slackEditSize(formatted) <= limit || budget <= splitToFitMinBudget {
			return head, tail
		}
		budget = budget * 3 / 4
	}
}

func (q *MessageQueue) writeCurrentMessage(formatted string, blocks []goslack.Block) error {
	q.takeReusedMessage()
	if q.currentMsgTS == "" {
		q.dropBlocks = false
		ts, err := q.postChat(formatted, blocks)
		if err != nil && len(blocks) > 0 && isMsgTooLong(err) {
			q.logger.Warn("slack rejected block payload as too large; dropping blocks and retrying text-only",
				"taskID", q.taskID)
			q.dropBlocks = true
			ts, err = q.postChat(formatted, nil)
		}
		if err != nil {
			q.logWriteError("failed to post text message", err)
			return err
		}
		q.currentMsgTS = ts
		q.answerCount++
		q.recordItem(answerMsgItem(q.answerCount), ts)
		q.recordItem(itemAnswerMsgTS, ts)
		return nil
	}
	if q.dropBlocks {
		blocks = nil
	}
	err := q.updateChat(q.currentMsgTS, formatted, blocks)
	if err != nil && len(blocks) > 0 && isMsgTooLong(err) {
		q.logger.Warn("slack rejected block payload as too large; dropping blocks and retrying text-only",
			"taskID", q.taskID, "ts", q.currentMsgTS)
		q.dropBlocks = true
		err = q.updateChat(q.currentMsgTS, formatted, nil)
	}
	if isMessageNotFound(err) {
		q.currentMsgTS = ""
		q.forgetItem(itemAnswerMsgTS)
		return q.writeCurrentMessage(formatted, blocks)
	}
	if err != nil {
		q.logWriteError("failed to update text message", err)
		return err
	}
	q.rewriting = false
	return nil
}

func (q *MessageQueue) logWriteError(msg string, err error) {
	if isMsgTooLong(err) {
		q.logger.Warn(msg, "taskID", q.taskID, "ts", q.currentMsgTS, "error", err)
		return
	}
	q.logger.Error(msg, "taskID", q.taskID, "ts", q.currentMsgTS, "error", err)
}

func isMsgTooLong(err error) bool {
	return err != nil && strings.Contains(err.Error(), "msg_too_long")
}

func (q *MessageQueue) notifyDeliveryGap() {
	if _, _, err := q.api.PostMessage(context.Background(), q.channelID,
		goslack.MsgOptionText("⚠️ Part of this response could not be delivered.", false),
		goslack.MsgOptionTS(q.threadTS),
		noticeMetadata(noticeError),
	); err != nil {
		q.logger.Debug("failed to post delivery-gap notice", "taskID", q.taskID, "error", err)
	}
}

func splitForSlack(s string, maxRunes int) (head, tail string) {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s, ""
	}
	minBreak := maxRunes - maxRunes/6
	cut := maxRunes
	for i := maxRunes - 1; i >= minBreak; i-- {
		if runes[i] == '\n' {
			cut = i + 1
			break
		}
	}
	if cut == maxRunes {
		for i := maxRunes - 1; i >= minBreak; i-- {
			if runes[i] == ' ' {
				cut = i + 1
				break
			}
		}
	}
	return string(runes[:cut]), string(runes[cut:])
}

func (q *MessageQueue) ensureStream() bool {
	if q.streamStarted {
		return q.streamTS != ""
	}
	q.streamStarted = true

	opts := []goslack.MsgOption{goslack.MsgOptionTaskDisplayMode(q.displayMode)}
	if q.threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(q.threadTS))
	}
	if q.recipientUserID != "" {
		opts = append(opts, goslack.MsgOptionRecipientUserID(q.recipientUserID))
	}
	if q.recipientTeamID != "" {
		opts = append(opts, goslack.MsgOptionRecipientTeamID(q.recipientTeamID))
	}

	_, ts, err := q.api.StartStream(context.Background(), q.channelID, opts...)
	if err != nil {
		q.logger.Warn("failed to start stream; falling back to non-streaming", "taskID", q.taskID, "error", err)
		q.streaming.Store(false)
		return false
	}
	q.streamTS = ts
	if q.streamLate.Load() {
		q.logger.Warn("activity stream opened after the submit stopped waiting; stopping it", "taskID", q.taskID)
		q.stopStreamBestEffort()
		q.streamTS = ""
		q.streaming.Store(false)
		return false
	}
	q.recordItem(itemStreamTS, ts)

	title := planTitle(q.initialTitle)
	if title == "" {
		title = "Working on your request…"
	}
	if _, _, err := q.api.AppendStream(context.Background(), q.channelID, q.streamTS,
		goslack.MsgOptionChunks(goslack.NewPlanUpdateChunk(title))); err != nil {
		q.logger.Warn("failed to seed plan title", "taskID", q.taskID, "error", err)
	}
	return true
}

func (q *MessageQueue) handleActivitySignal(op ActivitySignalOp) {
	if !q.streaming.Load() || q.timeline == nil {
		return
	}
	chunks := q.timeline.applySignal(op.Type, op.Data)
	if len(chunks) == 0 {
		return
	}
	if !q.ensureStream() {
		return
	}
	if _, _, err := q.api.AppendStream(context.Background(), q.channelID, q.streamTS, goslack.MsgOptionChunks(chunks...)); err != nil {
		q.logger.Warn("failed to append activity chunks", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) handleStreamFinalize(op StreamFinalizeOp) {
	answer := q.textBuffer.String()
	if op.ErrorText == "" && answer == "" && !q.streamStarted {
		return
	}
	if !q.ensureStream() {
		q.deliverAnswerNonStreaming(op)
		return
	}

	var chunks []goslack.StreamChunk
	if q.timeline != nil {
		chunks = append(chunks, q.timeline.finalize()...)
	}
	answerCh, oversized := q.answerChunks(answer, op.ErrorText)
	chunks = append(chunks, answerCh...)
	if len(chunks) > 0 {
		if _, _, err := q.api.AppendStream(context.Background(), q.channelID, q.streamTS, goslack.MsgOptionChunks(chunks...)); err != nil {
			q.logger.Warn("failed to append final stream chunks; delivering answer via non-streaming path", "taskID", q.taskID, "error", err)
			q.stopStreamBestEffort(streamNotice(op)...)
			q.deliverAnswerNonStreaming(op)
			return
		}
	}
	q.stopStreamBestEffort(streamNotice(op)...)
	if oversized {
		q.deliverAnswerNonStreaming(op)
	}
}

func (q *MessageQueue) deliverAnswerNonStreaming(op StreamFinalizeOp) {
	q.streaming.Store(false)
	if op.ErrorText != "" {
		q.textBuffer.Reset()
		q.writeTerminalNotice(cmp.Or(op.Notice, noticeError), op.ErrorText)
		return
	}
	q.flushTextBuffer()
}

func (q *MessageQueue) writeTerminalNotice(kind noticeKind, text string) {
	head, tail := q.splitToFit(text, slackMaxEditBytes)
	if tail != "" {
		head += "…"
	}
	formatted, blocks := q.renderText(head)
	err := q.writeNotice(kind, formatted, blocks)
	if err != nil && len(blocks) > 0 && isMsgTooLong(err) {
		q.logger.Warn("slack rejected block payload as too large; retrying terminal notice text-only", "taskID", q.taskID)
		err = q.writeNotice(kind, formatted, nil)
	}
	if err != nil {
		q.logger.Error("failed to write terminal error message", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) writeNotice(kind noticeKind, formatted string, blocks []goslack.Block) error {
	ts := q.currentMsgTS
	if q.rewriting && len(q.reuseMsgTS) > 0 {
		ts = q.reuseMsgTS[len(q.reuseMsgTS)-1]
	}
	if ts != "" {
		return q.updateChatNotice(ts, kind, formatted, blocks)
	}
	_, err := q.postChatNotice(kind, formatted, blocks)
	return err
}

func streamNotice(op StreamFinalizeOp) []goslack.MsgOption {
	if op.ErrorText == "" {
		return nil
	}
	return []goslack.MsgOption{noticeMetadata(cmp.Or(op.Notice, noticeError))}
}

func (q *MessageQueue) stopStreamBestEffort(opts ...goslack.MsgOption) {
	const attempts = 2
	for attempt := range attempts {
		_, _, err := q.api.StopStream(context.Background(), q.channelID, q.streamTS, opts...)
		if err == nil {
			return
		}
		if attempt < attempts-1 {
			q.logger.Warn("failed to stop stream; retrying", "taskID", q.taskID, "attempt", attempt, "error", err)
		} else {
			q.logger.Error("failed to stop stream", "taskID", q.taskID, "attempt", attempt, "error", err)
		}
	}
}

func (q *MessageQueue) answerChunks(answer, errorText string) (chunks []goslack.StreamChunk, oversized bool) {
	if errorText != "" {
		return []goslack.StreamChunk{goslack.NewMarkdownTextChunk(errorText)}, false
	}
	if answer == "" {
		return nil, false
	}
	if !containsTable(answer) && utf8.RuneCountInString(answer) <= sectionTextLimit {
		return []goslack.StreamChunk{goslack.NewMarkdownTextChunk(answer)}, false
	}
	blocks := q.answerBlocks(answer)
	if len(blocks) == 0 || len(blocks) > maxMessageBlocks {
		return nil, true
	}
	return []goslack.StreamChunk{goslack.NewBlocksChunk(blocks...)}, false
}

func (q *MessageQueue) answerBlocks(answer string) []goslack.Block {
	if blocks, ok := BuildMessageBlocks(answer); ok {
		return blocks
	}
	text := answer
	if q.correctMarkdown {
		text = ConvertMarkdown(answer)
	}
	return proseSections(text)
}

func (q *MessageQueue) handleFileUpload(op FileUploadOp) {
	if !q.streaming.Load() {
		q.flushTextBuffer()
		q.textBuffer.Reset()
		q.startNewMessage()
	}

	err := slackapi.UploadFile(context.Background(), q.api, q.logger, q.channelID, q.threadTS,
		op.Filename, op.Content, op.InitialComment)
	if err != nil {
		q.logger.Error("file upload failed", "taskID", q.taskID, "filename", op.Filename, "error", err)
		if _, _, notifyErr := q.api.PostMessage(context.Background(), q.channelID,
			goslack.MsgOptionText("❌ Failed to upload file: "+op.Filename, false),
			goslack.MsgOptionTS(q.threadTS),
			noticeMetadata(noticeError),
		); notifyErr != nil {
			q.logger.Debug("failed to notify user of upload error", "error", notifyErr)
		}
	}
}

func (q *MessageQueue) handleMessagePost(op MessagePostOp) {
	if _, err := q.postChatNotice(op.Notice, op.Text, op.Blocks); err != nil {
		q.logger.Error("failed to post message", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) handleMessageUpdate(op MessageUpdateOp) {
	if err := q.updateChat(op.TS, op.Text, op.Blocks); err != nil {
		q.logger.Error("failed to update message", "taskID", q.taskID, "ts", op.TS, "error", err)
	}
}

func (q *MessageQueue) handleMessageDelete(op MessageDeleteOp) {
	if err := q.deleteChat(op.TS); err != nil {
		q.logger.Error("failed to delete message", "taskID", q.taskID, "ts", op.TS, "error", err)
	}
}

func (q *MessageQueue) updateArtifactPlaceholder(ts string, op ArtifactProgressOp) {
	if err := q.updateChatNotice(ts, op.Notice, op.Text, op.Blocks); err != nil {
		q.logger.Error("failed to update artifact placeholder",
			"taskID", q.taskID, "filename", op.Filename, "artifactStatus", op.Status, "ts", ts, "error", err)
	}
}

func (q *MessageQueue) handleArtifactProgress(op ArtifactProgressOp) {
	existingTS := q.artifactMsgTS[op.Filename]

	switch op.Status {
	case "in-progress":
		if existingTS != "" {
			q.updateArtifactPlaceholder(existingTS, op)
			return
		}
		q.flushTextBuffer()
		q.textBuffer.Reset()
		q.startNewMessage()

		ts, err := q.postChatNotice(op.Notice, op.Text, op.Blocks)
		if err != nil {
			q.logger.Error("failed to post artifact placeholder",
				"taskID", q.taskID, "filename", op.Filename, "error", err)
			return
		}
		q.artifactMsgTS[op.Filename] = ts
		q.recordItem(itemArtifactMsgTSPrefix+op.Filename, ts)

	case "completed":
		if existingTS == "" {
			return
		}
		if err := q.deleteChat(existingTS); err != nil {
			q.logger.Error("failed to delete artifact placeholder",
				"taskID", q.taskID, "filename", op.Filename, "ts", existingTS, "error", err)
		}
		delete(q.artifactMsgTS, op.Filename)
		q.forgetItem(itemArtifactMsgTSPrefix + op.Filename)

	case "failed":
		if existingTS != "" {
			q.updateArtifactPlaceholder(existingTS, op)
			delete(q.artifactMsgTS, op.Filename)
			q.forgetItem(itemArtifactMsgTSPrefix + op.Filename)
			return
		}
		q.handleMessagePost(MessagePostOp{Text: op.Text, Blocks: op.Blocks, Notice: op.Notice})
	}
}

func (q *MessageQueue) handleFinalize(op FinalizeOp) {
	if op.StatusTS == "" {
		if op.Text != "" {
			q.handleMessagePost(MessagePostOp{Text: op.Text, Blocks: op.Blocks, Notice: op.Notice})
		}
		return
	}
	if err := q.updateChatNotice(op.StatusTS, op.Notice, op.Text, op.Blocks); err != nil {
		q.logger.Error("failed to finalize status message", "taskID", q.taskID, "error", err)
	}
}
