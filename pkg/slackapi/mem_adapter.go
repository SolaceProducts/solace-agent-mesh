// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
)

type RecordedCall struct {
	Method     string
	Channel    string
	ThreadTS   string
	Text       string
	Blocks     string
	Body       []byte
	Timestamp  time.Time
	ResponseTS string
	Params     map[string]string
}

type StoredMessage struct {
	Channel  string
	Text     string
	Blocks   string
	ThreadTS string
	TS       string
	User     string
	Username string
	BotID    string
	Files    []goslack.File
	Metadata goslack.SlackMetadata
	EditedTS string
}

type memState struct {
	calls          []RecordedCall
	tsCounter      int
	fileCounter    int
	userProfiles   map[string]*goslack.UserProfile
	usersByEmail   map[string]*goslack.User
	fileContents   map[string][]byte
	fileNames      map[string]string
	fileShares     map[string]string
	pendingUploads map[string]string
	messages       map[string]StoredMessage
	nextErrors     map[string]error
	botUserID      string
	botID          string
}

type InMemorySlackAPI struct {
	mu    sync.Mutex
	state memState
}

const (
	fakeToken  = "xoxb-fake"
	fakeAPIURL = "https://fake.slack.com/api/"
)

var _ SlackAPIAdapter = (*InMemorySlackAPI)(nil)

func NewInMemorySlackAPI() *InMemorySlackAPI {
	return &InMemorySlackAPI{
		state: memState{
			userProfiles:   make(map[string]*goslack.UserProfile),
			usersByEmail:   make(map[string]*goslack.User),
			fileContents:   make(map[string][]byte),
			fileNames:      make(map[string]string),
			fileShares:     make(map[string]string),
			pendingUploads: make(map[string]string),
			messages:       make(map[string]StoredMessage),
			nextErrors:     make(map[string]error),
		},
	}
}

func (m *InMemorySlackAPI) SetBotIdentity(userID, botID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.botUserID = userID
	m.state.botID = botID
}

func (m *InMemorySlackAPI) SeedStoredMessage(msg StoredMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg.TS == "" {
		msg.TS = m.nextTS()
	}
	m.state.messages[msg.Channel+":"+msg.TS] = msg
}

func (m *InMemorySlackAPI) SetNextError(method string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.nextErrors[method] = err
}

func (m *InMemorySlackAPI) SeedUserProfile(userID string, profile *goslack.UserProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.userProfiles[userID] = profile
}

func (m *InMemorySlackAPI) SeedUserByEmail(email string, user *goslack.User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.usersByEmail[email] = user
}

func (m *InMemorySlackAPI) SeedFile(fileID string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.fileContents[fileID] = append([]byte(nil), content...)
}

func (m *InMemorySlackAPI) Calls() []RecordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedCall, len(m.state.calls))
	copy(out, m.state.calls)
	return out
}

func (m *InMemorySlackAPI) SeedMessage(channelID, ts, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := channelID + ":" + ts
	m.state.messages[key] = StoredMessage{
		Channel: channelID,
		TS:      ts,
		Text:    text,
	}

	var seededCounter int
	if _, err := fmt.Sscanf(ts, "1700000000.%d", &seededCounter); err == nil {
		if seededCounter >= m.state.tsCounter {
			m.state.tsCounter = seededCounter
		}
	}
}

func (m *InMemorySlackAPI) SeedConversationMessage(channelID, threadTS, ts, user, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedConversationMessageLocked(channelID, threadTS, ts, user, text, nil)
}

func (m *InMemorySlackAPI) SeedConversationMessageWithFiles(channelID, threadTS, ts, user, text string, files []goslack.File) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedConversationMessageLocked(channelID, threadTS, ts, user, text, files)
}

func (m *InMemorySlackAPI) seedConversationMessageLocked(channelID, threadTS, ts, user, text string, files []goslack.File) {
	if ts == "" {
		ts = m.nextTS()
	}
	m.state.messages[channelID+":"+ts] = StoredMessage{
		Channel:  channelID,
		TS:       ts,
		ThreadTS: threadTS,
		User:     user,
		Text:     text,
		Files:    files,
	}
}

func (m *InMemorySlackAPI) SeedBotConversationMessage(channelID, threadTS, ts, username, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts == "" {
		ts = m.nextTS()
	}
	m.state.messages[channelID+":"+ts] = StoredMessage{
		Channel:  channelID,
		TS:       ts,
		ThreadTS: threadTS,
		Username: username,
		Text:     text,
	}
}

func (m *InMemorySlackAPI) Messages() []StoredMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StoredMessage, 0, len(m.state.messages))
	for _, msg := range m.state.messages {
		out = append(out, msg)
	}
	return out
}

func (m *InMemorySlackAPI) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.calls = nil
	m.state.tsCounter = 0
	m.state.fileCounter = 0
	m.state.messages = make(map[string]StoredMessage)
	m.state.pendingUploads = make(map[string]string)
	m.state.fileContents = make(map[string][]byte)
	m.state.fileNames = make(map[string]string)
	m.state.fileShares = make(map[string]string)
	m.state.nextErrors = make(map[string]error)
	m.state.userProfiles = make(map[string]*goslack.UserProfile)
	m.state.usersByEmail = make(map[string]*goslack.User)
}

func (m *InMemorySlackAPI) nextTS() string {
	m.state.tsCounter++
	return fmt.Sprintf("1700000000.%06d", m.state.tsCounter)
}

func (m *InMemorySlackAPI) popError(method string) error {
	err, ok := m.state.nextErrors[method]
	if ok {
		delete(m.state.nextErrors, method)
	}
	return err
}

func (m *InMemorySlackAPI) record(call RecordedCall) {
	call.Timestamp = time.Now()
	m.state.calls = append(m.state.calls, call)
}

type captureTransport struct {
	values url.Values
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.ParseForm(); err != nil {
		return nil, err
	}
	c.values = req.PostForm
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Request:    req,
	}, nil
}

func extractMsgOptions(channelID string, opts []goslack.MsgOption) (text, blocks, threadTS string, params map[string]string) {
	rt := &captureTransport{}
	client := goslack.New(fakeToken,
		goslack.OptionAPIURL(fakeAPIURL),
		goslack.OptionHTTPClient(&http.Client{Transport: rt}))
	if _, _, err := client.PostMessage(channelID, opts...); err != nil || rt.values == nil {
		return "", "", "", nil
	}
	values := rt.values
	text = values.Get("text")
	blocks = values.Get("blocks")
	threadTS = values.Get("thread_ts")
	p := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) > 0 {
			p[k] = v[0]
		}
	}
	return text, blocks, threadTS, p
}

func (m *InMemorySlackAPI) PostMessage(_ context.Context, channelID string, opts ...goslack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("PostMessage"); err != nil {
		return "", "", err
	}

	text, blocks, threadTS, params := extractMsgOptions(channelID, opts)
	ts := m.nextTS()

	m.record(RecordedCall{
		Method:     "PostMessage",
		Channel:    channelID,
		Text:       text,
		Blocks:     blocks,
		ThreadTS:   threadTS,
		ResponseTS: ts,
		Params:     params,
	})

	key := channelID + ":" + ts
	m.state.messages[key] = StoredMessage{
		Channel:  channelID,
		Text:     text,
		Blocks:   blocks,
		ThreadTS: threadTS,
		TS:       ts,
		User:     m.state.botUserID,
		BotID:    m.state.botID,
		Metadata: postedMetadata(params),
	}

	return channelID, ts, nil
}

func (m *InMemorySlackAPI) UpdateMessage(_ context.Context, channelID string, ts string, opts ...goslack.MsgOption) (string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("UpdateMessage"); err != nil {
		return "", "", "", err
	}

	key := channelID + ":" + ts
	msg, ok := m.state.messages[key]
	if !ok {
		return "", "", "", fmt.Errorf("message_not_found")
	}

	text, blocks, _, params := extractMsgOptions(channelID, opts)

	msg.Text = text
	msg.Blocks = blocks
	msg.EditedTS = fmt.Sprintf("%d.000000", time.Now().Unix())
	if _, ok := params["metadata"]; ok {
		msg.Metadata = postedMetadata(params)
	}
	m.state.messages[key] = msg

	m.record(RecordedCall{
		Method:     "UpdateMessage",
		Channel:    channelID,
		Text:       text,
		Blocks:     blocks,
		ThreadTS:   ts,
		ResponseTS: ts,
		Params:     params,
	})
	return channelID, ts, text, nil
}

func (m *InMemorySlackAPI) OpenView(_ context.Context, triggerID string, view goslack.ModalViewRequest) (*goslack.ViewResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("OpenView"); err != nil {
		return nil, err
	}

	body, _ := json.Marshal(view)
	m.record(RecordedCall{
		Method: "OpenView",
		Body:   body,
		Params: map[string]string{"triggerID": triggerID, "callbackID": view.CallbackID},
	})
	return &goslack.ViewResponse{View: goslack.View{CallbackID: view.CallbackID}}, nil
}

func (m *InMemorySlackAPI) PostEphemeral(_ context.Context, channelID, userID string, opts ...goslack.MsgOption) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("PostEphemeral"); err != nil {
		return "", err
	}

	text, blocks, threadTS, params := extractMsgOptions(channelID, opts)
	ts := m.nextTS()
	params["user"] = userID

	m.record(RecordedCall{
		Method:     "PostEphemeral",
		Channel:    channelID,
		Text:       text,
		Blocks:     blocks,
		ThreadTS:   threadTS,
		ResponseTS: ts,
		Params:     params,
	})

	return ts, nil
}

func (m *InMemorySlackAPI) DeleteMessage(_ context.Context, channelID string, ts string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("DeleteMessage"); err != nil {
		return "", "", err
	}

	key := channelID + ":" + ts
	if _, ok := m.state.messages[key]; !ok {
		return "", "", fmt.Errorf("message_not_found")
	}
	delete(m.state.messages, key)

	m.record(RecordedCall{
		Method:   "DeleteMessage",
		Channel:  channelID,
		ThreadTS: ts,
	})
	return channelID, ts, nil
}

func (m *InMemorySlackAPI) StartStream(_ context.Context, channelID string, opts ...goslack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("StartStream"); err != nil {
		return "", "", err
	}

	_, _, threadTS, params := extractMsgOptions(channelID, opts)
	ts := m.nextTS()

	m.record(RecordedCall{
		Method:     "StartStream",
		Channel:    channelID,
		ThreadTS:   threadTS,
		ResponseTS: ts,
		Params:     params,
	})
	m.state.messages[channelID+":"+ts] = StoredMessage{Channel: channelID, ThreadTS: threadTS, TS: ts, User: m.state.botUserID, BotID: m.state.botID}
	return channelID, ts, nil
}

func (m *InMemorySlackAPI) AppendStream(_ context.Context, channelID, ts string, opts ...goslack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("AppendStream"); err != nil {
		return "", "", err
	}

	text, blocks, _, params := extractMsgOptions(channelID, opts)
	if msg, ok := m.state.messages[channelID+":"+ts]; ok {
		msg.Text += text
		if blocks != "" {
			msg.Blocks = blocks
		}
		m.state.messages[channelID+":"+ts] = msg
	}

	m.record(RecordedCall{
		Method:     "AppendStream",
		Channel:    channelID,
		ThreadTS:   ts,
		Text:       text,
		Blocks:     blocks,
		ResponseTS: ts,
		Params:     params,
	})
	return channelID, ts, nil
}

func (m *InMemorySlackAPI) StopStream(_ context.Context, channelID, ts string, opts ...goslack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("StopStream"); err != nil {
		return "", "", err
	}

	_, _, _, params := extractMsgOptions(channelID, opts)
	if msg, ok := m.state.messages[channelID+":"+ts]; ok {
		if _, stamped := params["metadata"]; stamped {
			msg.Metadata = postedMetadata(params)
			m.state.messages[channelID+":"+ts] = msg
		}
	}
	m.record(RecordedCall{
		Method:     "StopStream",
		Channel:    channelID,
		ThreadTS:   ts,
		ResponseTS: ts,
		Params:     params,
	})
	return channelID, ts, nil
}

func (m *InMemorySlackAPI) GetUserProfile(_ context.Context, params *goslack.GetUserProfileParameters) (*goslack.UserProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetUserProfile"); err != nil {
		return nil, err
	}

	m.record(RecordedCall{
		Method: "GetUserProfile",
		Params: map[string]string{"user": params.UserID},
	})

	profile, ok := m.state.userProfiles[params.UserID]
	if !ok {
		return nil, fmt.Errorf("user_not_found")
	}
	return profile, nil
}

func (m *InMemorySlackAPI) OpenDMChannel(_ context.Context, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("OpenDMChannel"); err != nil {
		return "", err
	}

	m.record(RecordedCall{
		Method: "OpenDMChannel",
		Params: map[string]string{"user": userID},
	})

	return "D_" + userID, nil
}

func (m *InMemorySlackAPI) GetUserByEmail(_ context.Context, email string) (*goslack.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetUserByEmail"); err != nil {
		return nil, err
	}

	m.record(RecordedCall{
		Method: "GetUserByEmail",
		Params: map[string]string{"email": email},
	})

	user, ok := m.state.usersByEmail[email]
	if !ok {
		return nil, fmt.Errorf("users_not_found")
	}
	return user, nil
}

func (m *InMemorySlackAPI) GetUploadURLExternal(_ context.Context, params goslack.GetUploadURLExternalParameters) (*goslack.GetUploadURLExternalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetUploadURLExternal"); err != nil {
		return nil, err
	}

	m.state.fileCounter++
	fileID := fmt.Sprintf("F-mem-%d", m.state.fileCounter)
	uploadURL := fmt.Sprintf("https://fake-upload.slack.com/%d", m.state.fileCounter)

	m.state.pendingUploads[uploadURL] = fileID
	m.state.fileNames[fileID] = params.FileName

	m.record(RecordedCall{
		Method: "GetUploadURLExternal",
		Params: map[string]string{
			"filename":  params.FileName,
			"file_size": fmt.Sprintf("%d", params.FileSize),
		},
	})

	return &goslack.GetUploadURLExternalResponse{
		UploadURL: uploadURL,
		FileID:    fileID,
	}, nil
}

func (m *InMemorySlackAPI) Upload(_ context.Context, uploadURL, filename string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("Upload"); err != nil {
		return err
	}

	if filename == "" {
		return fmt.Errorf("filename is required for Slack file upload")
	}

	fileID, ok := m.state.pendingUploads[uploadURL]
	if !ok {
		return fmt.Errorf("unknown upload URL: %s", uploadURL)
	}
	delete(m.state.pendingUploads, uploadURL)

	m.state.fileContents[fileID] = append([]byte(nil), content...)

	m.record(RecordedCall{
		Method: "Upload",
		Body:   append([]byte(nil), content...),
		Params: map[string]string{"url": uploadURL, "filename": filename},
	})
	return nil
}

func (m *InMemorySlackAPI) CompleteUploadExternal(_ context.Context, params goslack.CompleteUploadExternalParameters) (*goslack.CompleteUploadExternalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("CompleteUploadExternal"); err != nil {
		return nil, err
	}

	for _, f := range params.Files {
		if _, exists := m.state.fileContents[f.ID]; !exists {
			return nil, fmt.Errorf("file_not_found: %s", f.ID)
		}
		m.state.fileShares[f.ID] = params.Channel
	}

	var ts string
	if params.Channel != "" && params.InitialComment != "" {
		ts = m.nextTS()
		key := params.Channel + ":" + ts
		m.state.messages[key] = StoredMessage{
			Channel:  params.Channel,
			Text:     params.InitialComment,
			ThreadTS: params.ThreadTimestamp,
			TS:       ts,
		}
	}

	filesJSON, _ := json.Marshal(params.Files)
	m.record(RecordedCall{
		Method:     "CompleteUploadExternal",
		Channel:    params.Channel,
		ThreadTS:   params.ThreadTimestamp,
		ResponseTS: ts,
		Params: map[string]string{
			"files":     string(filesJSON),
			"channel":   params.Channel,
			"thread_ts": params.ThreadTimestamp,
			"comment":   params.InitialComment,
		},
	})

	return &goslack.CompleteUploadExternalResponse{}, nil
}

func (m *InMemorySlackAPI) GetFileInfo(_ context.Context, fileID string) (*goslack.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetFileInfo"); err != nil {
		return nil, err
	}

	m.record(RecordedCall{
		Method: "GetFileInfo",
		Params: map[string]string{"file": fileID},
	})

	content, ok := m.state.fileContents[fileID]
	if !ok {
		return nil, fmt.Errorf("file_not_found")
	}

	shares := goslack.Share{}
	if ch, hasCh := m.state.fileShares[fileID]; hasCh {
		shares.Public = map[string][]goslack.ShareFileInfo{ch: {}}
	}

	name := m.state.fileNames[fileID]
	if name == "" {
		name = fileID + ".txt"
	}

	return &goslack.File{
		ID:     fileID,
		Name:   name,
		Size:   len(content),
		Shares: shares,
	}, nil
}

func (m *InMemorySlackAPI) GetFile(_ context.Context, downloadURL string, writer io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetFile"); err != nil {
		return err
	}

	fileID := downloadURL

	m.record(RecordedCall{
		Method: "GetFile",
		Params: map[string]string{"url": downloadURL},
	})

	content, ok := m.state.fileContents[fileID]
	if !ok {
		return fmt.Errorf("file_not_found")
	}
	_, err := writer.Write(content)
	return err
}

func (m *InMemorySlackAPI) GetConversationReplies(_ context.Context, channelID, threadTS string, limit int) ([]goslack.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetConversationReplies"); err != nil {
		return nil, err
	}
	m.record(RecordedCall{Method: "GetConversationReplies", Channel: channelID, ThreadTS: threadTS})

	var stored []StoredMessage
	for _, sm := range m.state.messages {
		if sm.Channel == channelID && (sm.ThreadTS == threadTS || sm.TS == threadTS) {
			stored = append(stored, sm)
		}
	}
	slices.SortFunc(stored, func(a, b StoredMessage) int { return cmp.Compare(a.TS, b.TS) })
	return storedToMessages(stored, limit, false), nil
}

func (m *InMemorySlackAPI) GetConversationHistory(_ context.Context, channelID string, limit int) ([]goslack.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("GetConversationHistory"); err != nil {
		return nil, err
	}
	m.record(RecordedCall{Method: "GetConversationHistory", Channel: channelID})

	var stored []StoredMessage
	for _, sm := range m.state.messages {
		if sm.Channel == channelID {
			stored = append(stored, sm)
		}
	}
	slices.SortFunc(stored, func(a, b StoredMessage) int { return cmp.Compare(a.TS, b.TS) })
	return storedToMessages(stored, limit, true), nil
}

func (m *InMemorySlackAPI) SetSessionStatus(_ context.Context, channelID, threadTS, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.popError("SetSessionStatus"); err != nil {
		return err
	}

	m.record(RecordedCall{
		Method:   "SetSessionStatus",
		Channel:  channelID,
		ThreadTS: threadTS,
		Params:   map[string]string{"status": status},
	})
	return nil
}

func storedToMessages(stored []StoredMessage, limit int, newestFirst bool) []goslack.Message {
	if limit > 0 && len(stored) > limit {
		stored = stored[len(stored)-limit:]
	}
	if newestFirst {
		slices.Reverse(stored)
	}
	out := make([]goslack.Message, 0, len(stored))
	for _, sm := range stored {
		var edited *goslack.Edited
		if sm.EditedTS != "" {
			edited = &goslack.Edited{Timestamp: sm.EditedTS}
		}
		out = append(out, goslack.Message{Msg: goslack.Msg{
			User:            sm.User,
			Username:        sm.Username,
			BotID:           sm.BotID,
			Text:            sm.Text,
			Timestamp:       sm.TS,
			ThreadTimestamp: sm.ThreadTS,
			Files:           sm.Files,
			Metadata:        sm.Metadata,
			Edited:          edited,
		}})
	}
	return out
}

func postedMetadata(params map[string]string) goslack.SlackMetadata {
	var md goslack.SlackMetadata
	if raw := params["metadata"]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &md)
	}
	return md
}
