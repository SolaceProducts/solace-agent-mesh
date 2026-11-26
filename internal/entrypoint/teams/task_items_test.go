// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const twoInstanceMessageID = "1792767298349"

func submitDM(t *testing.T, a *TeamsAdapter) string {
	t.Helper()
	if code := deliver(t, a, dmActivity(twoInstanceMessageID)); code != 200 {
		t.Fatalf("delivery answered %d, want 200", code)
	}
	ids := a.core.Tasks().AllByAgent(twoInstanceAgent)
	if len(ids) != 1 {
		t.Fatalf("submitted tasks = %v, want one", ids)
	}
	return ids[0]
}

func (inst *teamsInstances) agentSends(t *testing.T, topic, taskID string, result any) {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse(taskID, result))
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.brk.PublishGuaranteed(t.Context(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func (inst *teamsInstances) agentStreams(t *testing.T, taskID, text string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(text)},
		}},
	})
}

func (inst *teamsInstances) agentProgresses(t *testing.T, taskID, statusText string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type": "agent_progress_update", "status_text": statusText,
			})},
		}},
	})
}

func (inst *teamsInstances) agentCompletes(t *testing.T, taskID string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), taskID, &a2a.Task{
		ID: taskID, ContextID: "ctx", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	})
}

func (f *fakeBotConnector) waitPosted(t *testing.T, text string) []Activity {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		posts := f.postedActivities()
		if slices.ContainsFunc(posts, func(p Activity) bool { return p.Text == text }) {
			return posts
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q posted; posted = %q", text, f.posted())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fakeBotConnector) waitChanged(t *testing.T, change activityChange) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(f.changed(), change) {
		if time.Now().After(deadline) {
			t.Fatalf("no %+v; changes = %+v, posted = %q", change, f.changed(), f.posted())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireOneReply(t *testing.T, posts []Activity, text string) {
	t.Helper()
	var got []Activity
	for _, p := range posts {
		if p.Text == text {
			got = append(got, p)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%q posted %d times, want once", text, len(got))
	}
	if got[0].Conversation.ID != twoInstanceConversation || got[0].ReplyToID != twoInstanceMessageID {
		t.Errorf("%q posted to %q replying to %q, want %q replying to %q", text,
			got[0].Conversation.ID, got[0].ReplyToID, twoInstanceConversation, twoInstanceMessageID)
	}
}

func TestTwoInstances_AnswerPostedByTheInstanceThatDidNotSubmit(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	submitting := inst.adapters[1]
	taskID := submitDM(t, submitting)
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}
	_ = inst.clients[1].Disconnect(t.Context())

	inst.agentStreams(t, taskID, "Hello world")
	inst.agentCompletes(t, taskID)

	requireOneReply(t, inst.bot.waitPosted(t, "Hello world"), "Hello world")
}

func TestTwoInstances_TimeoutOnTheInstanceThatDidNotSubmitTellsTheUser(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	statusID := inst.bot.postedID(t, twoInstanceStatus)

	inst.adapters[0].OnError(t.Context(), taskID, &a2a.Error{Code: -32603, Message: "agent stopped responding"}, nil)

	const errorText = "❌ An error occurred while the agent was processing your request."
	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: statusID, Text: errorText})
	if slices.Contains(inst.bot.posted(), errorText) {
		t.Errorf("error posted as a new message as well; posted = %q", inst.bot.posted())
	}
}

func TestTwoInstances_TakeoverEditsAndDeletesTheSubmittersStatusMessage(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}
	_ = inst.clients[1].Disconnect(t.Context())

	inst.agentProgresses(t, taskID, "Looking it up")
	inst.agentStreams(t, taskID, "Hello world")
	inst.agentCompletes(t, taskID)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
	if !slices.Contains(inst.bot.changed(), activityChange{Method: http.MethodPut, ID: statusID, Text: "💭 Looking it up"}) {
		t.Errorf("progress not shown in the status message; changes = %+v", inst.bot.changed())
	}
	inst.bot.postedID(t, twoInstanceStatus)
	requireOneReply(t, inst.bot.postedActivities(), "Hello world")
}

func TestSubmit_FailureIsShownInTheStatusMessage(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	_ = inst.clients[0].Disconnect(t.Context())

	if code := deliver(t, a, dmActivity(twoInstanceMessageID)); code != 200 {
		t.Fatalf("delivery answered %d, want 200", code)
	}

	statusID := inst.bot.postedID(t, twoInstanceStatus)
	changes := inst.bot.changed()
	if len(changes) != 1 || changes[0].Method != http.MethodPut || changes[0].ID != statusID ||
		changes[0].Text == "" || changes[0].Text == twoInstanceStatus {
		t.Fatalf("changes = %+v, want the status message edited to the failure", changes)
	}
	if replies := repliesOtherThanStatus(inst.bot.postedActivities()); len(replies) != 0 {
		t.Errorf("failure posted as a new message as well: %q", replies)
	}
	if ids := a.core.Tasks().AllByAgent(twoInstanceAgent); len(ids) != 0 {
		t.Errorf("tasks left after the failure: %v", ids)
	}
	key := inboundMessageKey(twoInstanceConversation, twoInstanceMessageID)
	if state := inboundRow(t, a, key); state != "done" {
		t.Errorf("inbound record = %q, want done: the user was told", state)
	}
}

func TestSubmit_FailureIsRepliedWhenTheStatusMessageCannotBeEdited(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	inst.bot.setFailEdits(true)
	_ = inst.clients[0].Disconnect(t.Context())

	if code := deliver(t, a, dmActivity(twoInstanceMessageID)); code != 200 {
		t.Fatalf("delivery answered %d, want 200", code)
	}

	replies := repliesOtherThanStatus(inst.bot.postedActivities())
	if len(replies) != 1 {
		t.Fatalf("replies = %q, want the failure once", replies)
	}
	requireOneReply(t, inst.bot.postedActivities(), replies[0])
	key := inboundMessageKey(twoInstanceConversation, twoInstanceMessageID)
	if state := inboundRow(t, a, key); state != "done" {
		t.Errorf("inbound record = %q, want done: the user was told", state)
	}
}

func repliesOtherThanStatus(posts []Activity) []string {
	var texts []string
	for _, p := range posts {
		if p.Text != "" && p.Text != twoInstanceStatus {
			texts = append(texts, p.Text)
		}
	}
	return texts
}

func TestSubmit_RefusedWhenTheDestinationCannotBeWritten(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	a.items = store.NewEntrypointTaskItemRepo(closedDB(t))

	if code := deliver(t, a, dmActivity(twoInstanceMessageID)); code != 200 {
		t.Fatalf("delivery answered %d, want 200", code)
	}

	if n := inst.published(t); n != 0 {
		t.Errorf("tasks published = %d, want none", n)
	}
	if ids := a.core.Tasks().AllByAgent(twoInstanceAgent); len(ids) != 0 {
		t.Errorf("tasks left after the refusal: %v", ids)
	}
	if got := inst.bot.posted(); len(got) != 1 || got[0] == twoInstanceStatus {
		t.Errorf("posted = %q, want only the refusal", got)
	}
	key := inboundMessageKey(twoInstanceConversation, twoInstanceMessageID)
	if state := inboundRow(t, a, key); state != "done" {
		t.Errorf("inbound record = %q, want done: the user was told", state)
	}
}

func TestLoadTask_UnreadableItemsAreRetried(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	other := inst.adapters[0]
	healthy := other.items

	other.items = store.NewEntrypointTaskItemRepo(closedDB(t))
	if err := other.LoadTask(t.Context(), taskID); err == nil {
		t.Fatal("LoadTask with the items unreadable succeeded, want an error to retry")
	}
	if meta := other.getTaskMeta(t.Context(), taskID); meta != nil {
		t.Fatal("an unreadable task must read as unknown")
	}

	other.items = healthy
	if err := other.LoadTask(t.Context(), "01a16908-74a6-726a-9192-efdb61e2d829"); err != nil {
		t.Fatalf("LoadTask of a task with no items = %v, want nil", err)
	}
	if err := other.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask = %v", err)
	}
	meta := other.getTaskMeta(t.Context(), taskID)
	if meta == nil || meta.ConversationID != twoInstanceConversation || meta.Activity.ID != twoInstanceMessageID ||
		meta.Activity.From.ID != "29:user" || meta.SessionID == "" {
		t.Fatalf("loaded meta = %+v, want the submitted task's destination", meta)
	}
}

func TestDestination_HoldsNoMessageContent(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)

	value, ok, err := a.items.Get(t.Context(), taskID, itemDestination)
	if err != nil || !ok {
		t.Fatalf("destination = (%v, %v), want written", ok, err)
	}
	for _, private := range []string{"what is 2 + 2?", "Test User", "aad-user"} {
		if strings.Contains(value, private) {
			t.Errorf("destination %s holds %q", value, private)
		}
	}
}

func closedDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

func (inst *teamsInstances) agentCompletesWith(t *testing.T, taskID, text string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), taskID, &a2a.Task{
		ID: taskID, ContextID: "ctx", Kind: "task", Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(text)},
			},
		},
	})
}

func (inst *teamsInstances) waitItem(t *testing.T, taskID, item, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _, _ := inst.adapters[0].items.Get(t.Context(), taskID, item)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("item %s = %q, want %q", item, got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const (
	answerStart = "The answer is 4."
	answerWhole = "The answer is 4. Here's why: 2 + 2 = 4."
	answerFinal = answerWhole + "\n«artifact_return:report.pdf»"
)

func requireOnlyAnswerBubble(t *testing.T, bot *fakeBotConnector, text string) {
	t.Helper()
	if replies := repliesOtherThanStatus(bot.postedActivities()); len(replies) != 1 || replies[0] != text {
		t.Errorf("answer messages posted = %q, want only %q", replies, text)
	}
	for _, c := range bot.changed() {
		if strings.Contains(c.Text, "«") {
			t.Errorf("raw marker written to Teams: %q", c.Text)
		}
	}
}

func TestTwoInstances_TakeoverWritesTheWholeAnswerIntoTheFirstBubble(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	_ = inst.clients[2].Disconnect(t.Context())

	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	_ = inst.clients[inst.bot.poster(t, answerStart)].Disconnect(t.Context())

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	requireOnlyAnswerBubble(t, inst.bot, answerStart)
	requireNoPartialEdit(t, inst.bot, answerID)
}

func requireNoPartialEdit(t *testing.T, bot *fakeBotConnector, answerID string) {
	t.Helper()
	for _, c := range bot.changed() {
		if c.Method == http.MethodPut && c.ID == answerID && c.Text != answerWhole {
			t.Errorf("answer message replaced by %q, want only the whole answer", c.Text)
		}
	}
}

func TestTwoInstances_SubmitterTakingTheEventsBackWritesIntoTheSameBubble(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	const submitter = 1
	taskID := submitDM(t, inst.adapters[submitter])

	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	if p := inst.bot.poster(t, answerStart); p == submitter {
		t.Fatal("the submitter received the events; this test needs the other instance to")
	}
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	_ = inst.clients[0].Disconnect(t.Context())

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	requireOnlyAnswerBubble(t, inst.bot, answerStart)
	requireNoPartialEdit(t, inst.bot, answerID)
}

func TestTwoInstances_TakeoverAfterTextStartedButNotPostedPostsTheWholeAnswer(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	_ = inst.clients[1].Disconnect(t.Context())
	if err := inst.adapters[1].items.Put(t.Context(), taskID, itemTextStarted, ""); err != nil {
		t.Fatal(err)
	}

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitPosted(t, answerWhole)
	requireOnlyAnswerBubble(t, inst.bot, answerWhole)
}

func TestFinalResponse_TextShownWhenNothingStreamed(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])

	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitPosted(t, answerWhole)
	requireOnlyAnswerBubble(t, inst.bot, answerWhole)
}

func TestFinalResponse_TextShownWithTheNotesWhenNothingStreamed(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])
	const failure = "❌ Failed to create artifact: `report.pdf`"
	inst.agentReportsArtifact(t, taskID, "report.pdf", "failed")
	inst.bot.waitPosted(t, failure)
	noteID := inst.bot.postedID(t, failure)

	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: noteID, Text: answerWhole + "\n\n" + failure})
	requireOnlyAnswerBubble(t, inst.bot, failure)
}

func TestFinalResponse_StreamedAnswerNotRewritten(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])
	statusID := inst.bot.postedID(t, twoInstanceStatus)

	inst.agentStreams(t, taskID, answerStart)
	inst.agentCompletesWith(t, taskID, answerStart+" (final)")

	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
	requireOnlyAnswerBubble(t, inst.bot, answerStart)
	for _, c := range inst.bot.changed() {
		if strings.Contains(c.Text, "(final)") {
			t.Errorf("final text written over the streamed answer: %+v", c)
		}
	}
}

func TestStreamText_RecordsThatTheAnswerStarted(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)
	arrived, release := inst.bot.hold(t, http.MethodPost)

	inst.agentStreams(t, taskID, answerStart)
	<-arrived

	if _, ok, err := a.items.Get(t.Context(), taskID, itemTextStarted); err != nil || !ok {
		t.Fatalf("text-started = (%v, %v) while the answer's post is held, want recorded", ok, err)
	}
	release()
	inst.bot.waitPosted(t, answerStart)
}

func TestRefresh_UnreadableItemsDoNotHoldTheSubmittersEvents(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)
	healthy := a.items

	a.items = store.NewEntrypointTaskItemRepo(closedDB(t))
	if err := a.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask with the items unreadable = %v, want nil: the submitter has its own copy", err)
	}
	meta := a.getTaskMeta(t.Context(), taskID)
	if meta == nil {
		t.Fatal("the submitter's own copy must be used while the items are unreadable")
	}
	if meta.refreshDue() {
		t.Fatal("read again at once after a failure, want after refreshRetryDelay")
	}

	a.items = healthy
	if err := a.items.Put(t.Context(), taskID, itemTextStarted, ""); err != nil {
		t.Fatal(err)
	}
	meta.mu.Lock()
	meta.nextReadAt = time.Now().Add(-time.Millisecond)
	meta.mu.Unlock()
	if err := a.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask = %v", err)
	}
	meta.mu.Lock()
	read := meta.itemsRead
	meta.mu.Unlock()
	if !read {
		t.Fatal("items not read once the retry was due")
	}
}

func TestRefresh_SubmitterReadsAgainUntilTheAnswerStarts(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	inst.agentProgresses(t, taskID, "Thinking")
	waitUntil(t, "the progress line is shown", func() bool { return inst.bot.edits(statusID) > 0 })

	const otherAnswer = "posted-by-another-instance"
	for item, value := range map[string]string{itemTextStarted: "", itemAnswerMsg: otherAnswer} {
		if err := a.items.Put(t.Context(), taskID, item, value); err != nil {
			t.Fatal(err)
		}
	}
	readLongAgo(t, a, taskID)

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: otherAnswer, Text: answerWhole})
	if replies := repliesOtherThanStatus(inst.bot.postedActivities()); len(replies) != 0 {
		t.Errorf("answer messages posted = %q, want none: the answer another instance started is edited", replies)
	}
	requireNoPartialEdit(t, inst.bot, otherAnswer)
}

func TestRefresh_InstanceHoldingAnotherInstancesTaskReadsAgainUntilTheAnswerStarts(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	holder := inst.adapters[0]
	inst.agentProgresses(t, taskID, "Thinking")
	waitUntil(t, "instance 0 handles the task's events (this test needs it to)", func() bool { return holder.hasQueue(taskID) })

	const otherAnswer = "posted-by-the-submitter"
	for item, value := range map[string]string{itemTextStarted: "", itemAnswerMsg: otherAnswer} {
		if err := holder.items.Put(t.Context(), taskID, item, value); err != nil {
			t.Fatal(err)
		}
	}
	readLongAgo(t, holder, taskID)

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: otherAnswer, Text: answerWhole})
	if replies := repliesOtherThanStatus(inst.bot.postedActivities()); len(replies) != 0 {
		t.Errorf("answer messages posted = %q, want none: the answer the submitter started is edited", replies)
	}
}

func readLongAgo(t *testing.T, a *TeamsAdapter, taskID string) {
	t.Helper()
	meta, _ := a.core.Tasks().Metadata(taskID).(*TeamsTaskMeta)
	if meta == nil {
		t.Fatal("the instance holds no copy of the task")
	}
	meta.mu.Lock()
	meta.nextReadAt = time.Time{}
	meta.mu.Unlock()
}

func TestRefresh_OneReadServesTheRestOfTheEvent(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)

	if err := a.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask = %v", err)
	}
	meta := a.getTaskMeta(t.Context(), taskID)
	if meta.refreshDue() {
		t.Fatal("read again right after a read, want the read to serve the rest of the event")
	}
	readLongAgo(t, a, taskID)
	if !meta.refreshDue() {
		t.Fatal("not read again once the last read is stale and the answer has not started")
	}
}

func TestTwoInstances_QueueStoppedWhenAnotherInstanceEndsTheTask(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	statusID := inst.bot.postedID(t, twoInstanceStatus)

	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	if p := inst.bot.poster(t, answerStart); p != 0 {
		t.Fatalf("instance %d received the events; this test needs instance 0 to", p)
	}
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	_ = inst.clients[0].Disconnect(t.Context())
	inst.agentCompletesWith(t, taskID, answerFinal)
	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
	ending := inst.adapters[1]
	waitUntil(t, "instance 1 deletes the task's record", func() bool {
		return !slices.Contains(ending.core.Tasks().AllByAgent(twoInstanceAgent), taskID)
	})

	first := inst.adapters[0]
	if !first.hasQueue(taskID) {
		t.Fatal("instance 0 has no queue for the task before the sweep")
	}
	before := inst.bot.edits(answerID)
	first.core.RunTaskSweepNow()
	waitUntil(t, "instance 0 stops the task's queue", func() bool { return !first.hasQueue(taskID) })
	first.server.Wait()

	requireNoPartialEdit(t, inst.bot, answerID)
	if n := inst.bot.edits(answerID); n != before {
		t.Errorf("the answer was edited %d more times by the stop, want 0", n-before)
	}
}

func (inst *teamsInstances) agentReportsArtifact(t *testing.T, taskID, filename, status string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type": "artifact_creation_progress", "filename": filename, "status": status,
			})},
		}},
	})
}

func TestTwoInstances_ArtifactNoteOnATakenOverTaskIsPostedOnItsOwn(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])

	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	_ = inst.clients[inst.bot.poster(t, answerStart)].Disconnect(t.Context())

	const failure = "❌ Failed to create artifact: `report.pdf`"
	inst.agentReportsArtifact(t, taskID, "report.pdf", "failed")
	inst.bot.waitPosted(t, failure)
	if n := inst.bot.edits(answerID); n != 0 {
		t.Errorf("the answer was edited %d times by the note, want 0", n)
	}

	inst.agentCompletesWith(t, taskID, answerFinal)
	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	requireNoPartialEdit(t, inst.bot, answerID)
}

func TestTwoInstances_AnswerPostLandingAfterTheTakeoverIsWrittenInto(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	_ = inst.clients[2].Disconnect(t.Context())
	arrived, release := inst.bot.hold(t, http.MethodPost)

	inst.agentStreams(t, taskID, answerStart)
	<-arrived
	streaming, other := 0, 1
	if inst.adapters[1].hasQueue(taskID) {
		streaming, other = 1, 0
	}
	_ = inst.clients[streaming].Disconnect(t.Context())
	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	waitUntil(t, "the other instance takes the task over", func() bool { return inst.adapters[other].hasQueue(taskID) })

	release()
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	requireOnlyAnswerBubble(t, inst.bot, answerStart)
}

const artifactFailureNote = "❌ Failed to create artifact: `report.pdf`"

func (inst *teamsInstances) streamAnswerWithNote(t *testing.T, taskID string) (answerID string, poster int) {
	t.Helper()
	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID = inst.bot.postedID(t, answerStart)
	inst.agentReportsArtifact(t, taskID, "report.pdf", "failed")
	inst.waitItem(t, taskID, itemNotes, artifactFailureNote)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	return answerID, inst.bot.poster(t, answerStart)
}

func TestTwoInstances_TakeoverKeepsTheNotesInTheAnswer(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	_ = inst.clients[2].Disconnect(t.Context())
	answerID, poster := inst.streamAnswerWithNote(t, taskID)
	_ = inst.clients[poster].Disconnect(t.Context())

	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole + "\n\n" + artifactFailureNote})
}

func TestTwoInstances_NoteRecordedAfterTheTakeoverIsKept(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	_ = inst.clients[2].Disconnect(t.Context())
	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	poster := inst.bot.poster(t, answerStart)
	_ = inst.clients[poster].Disconnect(t.Context())
	other := 1 - poster
	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	waitUntil(t, "the other instance takes the task over", func() bool { return inst.adapters[other].hasQueue(taskID) })

	if err := inst.adapters[poster].items.Put(t.Context(), taskID, itemNotes, artifactFailureNote); err != nil {
		t.Fatal(err)
	}
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole + "\n\n" + artifactFailureNote})
}

func TestTwoInstances_SubmitterTakingTheEventsBackKeepsTheNotesInTheAnswer(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	const submitter = 1
	taskID := submitDM(t, inst.adapters[submitter])
	answerID, poster := inst.streamAnswerWithNote(t, taskID)
	if poster == submitter {
		t.Fatal("the submitter received the events; this test needs the other instance to")
	}
	_ = inst.clients[poster].Disconnect(t.Context())

	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole + "\n\n" + artifactFailureNote})
}

func TestAnswerClaim_UnreachableDatabase(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	logs := &lockedBuffer{}
	a.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a.items = store.NewEntrypointTaskItemRepo(closedDB(t))
	const item = itemHILAnsweredPrefix + "r1"

	if claim, value := a.claimAnswer(t.Context(), "t1", item); claim != answerClaimWon || value != "" {
		t.Errorf("claimAnswer = (%v, %q), want won with no claim to release", claim, value)
	}
	if a.answerWasForwarded(t.Context(), "t1", item) {
		t.Error("an unreadable claim must read as not forwarded")
	}
	a.releaseAnswer(t.Context(), "t1", item, "1")
	a.markAnswerForwarded(t.Context(), "t1", item, "1")

	for _, want := range []string{
		"teams answer claim failed",
		"teams answer claim read failed",
		"teams answer claim release failed",
		"teams answer claim forwarded write failed",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("no %q WARN; logs:\n%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "r1") {
		t.Errorf("a claim WARN names the prompt's request id; logs:\n%s", logs.String())
	}
}

func (a *TeamsAdapter) hasQueue(taskID string) bool {
	a.queuesMu.Lock()
	defer a.queuesMu.Unlock()
	_, ok := a.queues[taskID]
	return ok
}

func TestSubmit_HangingInitialStatusPostDoesNotHoldTheSubmit(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	a.statusPostTimeout = 300 * time.Millisecond
	_, release := inst.bot.hold(t, http.MethodPost)

	taskID := submitDM(t, a)

	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1 while the status post hangs", n)
	}
	if _, ok, _ := a.items.Get(t.Context(), taskID, itemStatusMsg); ok {
		t.Error("status message recorded although its post did not return in time")
	}
	release()
}

func TestSubmit_FailedInitialStatusPostStillSubmitsTheTask(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	inst.bot.failNextPost()

	taskID := submitDM(t, a)
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}
	inst.agentStreams(t, taskID, answerStart)
	inst.agentCompletes(t, taskID)
	waitUntil(t, "the task ends", func() bool {
		return !slices.Contains(a.core.Tasks().AllByAgent(twoInstanceAgent), taskID)
	})

	posted := inst.bot.posted()
	if slices.Contains(posted, twoInstanceStatus) {
		t.Errorf("posted = %q, want no status message: Bot Connector refused it", posted)
	}
	if !slices.Contains(posted, answerStart) {
		t.Errorf("posted = %q, want the answer", posted)
	}
	for _, c := range inst.bot.changed() {
		if c.Method == http.MethodDelete {
			t.Errorf("deleted %q although no status message was posted", c.ID)
		}
	}
}
