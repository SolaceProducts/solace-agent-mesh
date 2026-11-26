// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

var _ directMessages = (*restChatAPI)(nil)

type chatWithSpaces struct {
	*fakeChat
	dms *fakeDirectMessages
}

func (c chatWithSpaces) IsDirectMessage(ctx context.Context, space string) (bool, error) {
	return c.dms.IsDirectMessage(ctx, space)
}

func intPtr(v int) *int { return &v }

func TestInitClients_TakesTheSpaceReaderFromTheChatClient(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.dms = nil
	rig.a.chatOverride = chatWithSpaces{fakeChat: rig.chat, dms: rig.dms}
	rig.a.subOverride = &fakeSub{}

	if err := rig.a.initClients(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rig.a.dms == nil {
		t.Error("the adapter did not take the space reader from its Chat client, so proactive delivery would stay off")
	}
}

func TestStart_StartsProactiveDelivery(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.sub = &fakeSub{}
	rig.a.cfg.PubSubSubscription = "projects/p/subscriptions/s"

	if err := rig.a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rig.a.core.AdvertisedNotifyCapabilities() == nil {
		t.Error("Start did not start proactive delivery: the entrypoint advertises nothing to the notification service")
	}
}

func TestProcess_RecordsPresenceOnlyForAnAdmittedDirectMessage(t *testing.T) {
	noMatch := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	for name, tc := range map[string]struct {
		dm        bool
		domains   []string
		authorize auth.SenderAuthorizer
		want      bool
	}{
		"an admitted direct message":               {dm: true, authorize: matchedAuthorizer(), want: true},
		"a sender the identity gate does not know": {dm: true, authorize: noMatch},
		"a sender outside the allowed domains":     {dm: true, domains: []string{"corp.example.org"}, authorize: matchedAuthorizer()},
		"a shared space":                           {dm: false, authorize: matchedAuthorizer()},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			rig.a.core.SetIdentityAuthorizer(tc.authorize)
			rig.a.cfg.AllowedEmailDomains = tc.domains
			presence, err := rig.brk.CreateDirectSubscription()
			if err != nil {
				t.Fatal(err)
			}
			if err = rig.brk.Subscribe(presence, a2a.NotifyPresenceSubscription(notifyNamespace)); err != nil {
				t.Fatal(err)
			}
			space := notifySpace
			if !tc.dm {
				space = "spaces/TEAM"
			}
			ev := &inboundEvent{Kind: kindMessage, Addressed: true, IsDM: tc.dm, SpaceName: space, SenderName: "users/1", SenderEmail: notifyEmail}

			rig.a.process(t.Context(), ev)

			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			msg, err := rig.brk.Receive(ctx, presence)
			if !tc.want {
				if err == nil {
					t.Fatalf("presence = %s, want none", msg.Payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("no presence was published: %v", err)
			}
			var p a2a.NotifyPresence
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.UserID != notifyEmail || p.Email != notifyEmail || p.ChannelType != EntrypointType {
				t.Errorf("presence = %+v, want the person's own id and email on %s", p, EntrypointType)
			}
		})
	}
}

func TestDelivery_UnderWayWhenTheAdapterStopsIsStillMade(t *testing.T) {
	rig := newProactiveRig(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rig.chat.onWrite = func(ctx context.Context) error {
		once.Do(func() { close(started) })
		<-release
		return ctx.Err()
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d := a2a.NotifyDeliver{RequestID: "req-s", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, ChannelType: EntrypointType, Address: rig.address(t), Title: "Report"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rig.a.handleNotifyDeliver(d)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery never reached Chat")
	}

	rig.a.cancel()
	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery did not finish")
	}
	if writes := rig.posted(t); len(writes) != 1 || writes[0].text != "Report" {
		t.Errorf("writes = %+v, want the notification posted despite the stop", writes)
	}
}

func TestDeliverInput_ARefusedCardFallsBackToTheWebUI(t *testing.T) {
	rig := newProactiveRig(t)
	rig.chat.failures = []error{&permanentError{err: errors.New("rejected")}}

	rig.deliver(t, inputDeliver("req-9"))

	writes := rig.posted(t)
	if len(writes) != 1 || writes[0].cardButtons != 0 || !strings.Contains(writes[0].cardText, "web UI") {
		t.Errorf("writes = %+v, want one card with no buttons that points at the web UI", writes)
	}
}

func TestDeliverInput_ATooLargeSurfaceIsAPointerToTheWebUI(t *testing.T) {
	rig := newProactiveRig(t)
	args := map[string]any{}
	for i := 0; i < 20; i++ {
		args[strings.Repeat("k", i+1)] = strings.Repeat("x", 1500)
	}
	d := inputDeliver("req-9")
	d.Surface = chatApprovalSurface(t, args, "approve?", true)

	rig.deliver(t, d)

	writes := rig.posted(t)
	if len(writes) != 1 || writes[0].cardButtons != 0 || !strings.Contains(writes[0].cardText, "web UI") {
		t.Errorf("writes = %+v, want one card with no buttons that points at the web UI", writes)
	}
}

func TestDelivery_RetriesAReadOfTheSpaceThatMayPass(t *testing.T) {
	for name, tc := range map[string]struct {
		failFirst  []error
		wantWrites int
		wantReads  int
	}{
		"a rate limit":                {[]error{&rateLimitedError{err: errors.New("429")}}, 1, 2},
		"a server error":              {[]error{apiError(503)}, 1, 2},
		"a space the app cannot read": {[]error{&permanentError{err: wrapAs(errNoDirectMessage, apiError(403))}}, 0, 1},
		"credentials chat refused":    {[]error{&permanentError{err: wrapAs(errChatRefused, apiError(401))}}, 0, 1},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			rig.dms.failFirst = tc.failFirst

			rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-r", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Title: "Report"})

			if writes := rig.posted(t); len(writes) != tc.wantWrites {
				t.Errorf("writes = %d, want %d", len(writes), tc.wantWrites)
			}
			if reads := len(rig.dms.askedSpaces()); reads != tc.wantReads {
				t.Errorf("reads of the space = %d, want %d", reads, tc.wantReads)
			}
		})
	}
}

func TestDelivery_AMalformedSpaceNeverReachesChat(t *testing.T) {
	for _, space := range []string{"x/spaces/DMADA/y", "spaces/DMADA/messages/m1", "spaces/", "spaces/a b", "../spaces/DMADA", "spaces/DMADA\n", ""} {
		t.Run(space, func(t *testing.T) {
			rig := newProactiveRig(t)
			addr, err := json.Marshal(proactiveAddress{Space: space, Email: notifyEmail})
			if err != nil {
				t.Fatal(err)
			}

			rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-m", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Address: addr, Title: "Report"})

			if asked := rig.dms.askedSpaces(); len(asked) != 0 {
				t.Errorf("Chat was asked about %q, want nothing", asked)
			}
			if writes := rig.posted(t); len(writes) != 0 {
				t.Errorf("writes = %+v, want nothing", writes)
			}
		})
	}
}

func TestDelivery_ADomainNoLongerAllowedIsLoggedAsPolicy(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.AllowedEmailDomains = []string{"corp.example.org"}

	rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-p", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Title: "Report"})

	if rig.logs.count(slog.LevelWarn, "google chat notify deliver: the recipient's email domain") != 1 || rig.logs.count(slog.LevelError, "google chat notify deliver: unusable address") != 0 {
		t.Error("a domain an operator excluded was not logged once at WARN as policy")
	}
	if writes := rig.posted(t); len(writes) != 0 {
		t.Errorf("writes = %+v, want nothing", writes)
	}
}

func TestHandOverFiles_OneFileForEachNameInItsLastVersion(t *testing.T) {
	rig := newProactiveRig(t)
	d := a2a.NotifyDeliver{UserID: notifyEmail, Artifacts: []a2a.NotifyArtifact{
		{AgentName: "a1", UserID: notifyEmail, SessionID: "s1", Filename: "r.csv", Version: intPtr(0)},
		{AgentName: "a2", UserID: notifyEmail, SessionID: "s2", Filename: "r.csv", Version: intPtr(1)},
		{AgentName: "a1", UserID: notifyEmail, SessionID: "s1", Filename: "other.csv"},
	}}

	files, owned := rig.a.handOverFiles(d, proactiveAddress{Space: notifySpace, Email: notifyEmail})

	if len(files) != 2 || files[0].Filename != "r.csv" || files[1].Filename != "other.csv" {
		t.Fatalf("files = %+v, want r.csv then other.csv", files)
	}
	if files[0].Version == nil || *files[0].Version != 1 || owned["r.csv"].SessionID != "s2" || owned["r.csv"].AgentName != "a2" {
		t.Errorf("r.csv is version %v read as %+v, want its last version read as the scope that named it", files[0].Version, owned["r.csv"])
	}
}

func TestHandOverFiles_OnlyToTheOwnerWhenTheOwnerIsAnEmail(t *testing.T) {
	rig := newProactiveRig(t)
	d := a2a.NotifyDeliver{UserID: "bob@example.com", Artifacts: []a2a.NotifyArtifact{
		{AgentName: "a1", UserID: "bob@example.com", SessionID: "s1", Filename: "r.csv"},
	}}

	files, _ := rig.a.handOverFiles(d, proactiveAddress{Space: notifySpace, Email: notifyEmail})

	if len(files) != 0 {
		t.Errorf("files = %+v, want none: the address names someone other than the file's owner", files)
	}
}

func TestNotifyPromptClick_TheRecipientsEmailMatchesInAnyCase(t *testing.T) {
	rig := newProactiveRig(t)
	replies := rig.subscribeReplies(t, "req-9")
	click := rig.promptClick(t, "req-9", hilSubmitLabel, "Ada@Example.COM")

	if res := rig.a.process(t.Context(), click); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	if _, ok := rig.nextReply(t, replies, 5*time.Second); !ok {
		t.Error("no answer reached the notification service")
	}
}

func TestRestChatAPI_IsDirectMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		status       int
		body         string
		want         bool
		wantErr      func(error) bool
		wantTerminal bool
	}{
		"a direct message with one person": {status: 200, body: `{"name":"spaces/DM1","spaceType":"DIRECT_MESSAGE","singleUserBotDm":true}`, want: true},
		"a direct message of the old kind": {status: 200, body: `{"name":"spaces/DM1","type":"DM"}`, want: true},
		"a direct message with several":    {status: 200, body: `{"name":"spaces/DM1","spaceType":"DIRECT_MESSAGE","singleUserBotDm":false}`},
		"a group chat":                     {status: 200, body: `{"name":"spaces/G1","spaceType":"GROUP_CHAT"}`},
		"a room":                           {status: 200, body: `{"name":"spaces/R1","spaceType":"SPACE","type":"ROOM"}`},
		"a space the app cannot read":      {status: 403, body: `{"error":{"code":403,"message":"denied"}}`, wantErr: func(e error) bool { return errors.Is(e, errNoDirectMessage) }, wantTerminal: true},
		"a space that is gone":             {status: 404, body: `{"error":{"code":404,"message":"gone"}}`, wantErr: func(e error) bool { return errors.Is(e, errNoDirectMessage) }, wantTerminal: true},
		"credentials chat refused":         {status: 401, body: `{"error":{"code":401,"message":"no"}}`, wantErr: func(e error) bool { return errors.Is(e, errChatRefused) }, wantTerminal: true},
		"a rate limit":                     {status: 429, body: `{"error":{"code":429,"message":"slow"}}`, wantErr: func(e error) bool { _, ok := errors.AsType[*rateLimitedError](e); return ok }},
		"a server error": {status: 503, body: `{"error":{"code":503,"message":"down"}}`, wantErr: func(e error) bool {
			return e != nil && !errors.Is(e, errNoDirectMessage) && !errors.Is(e, errChatRefused) && !isTerminalWriteError(e)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			api, err := newRestChatAPI(t.Context(), &Config{ChatAPIBaseURL: srv.URL + "/", TestSkipGoogleAuth: true})
			if err != nil {
				t.Fatal(err)
			}

			direct, err := api.IsDirectMessage(t.Context(), "spaces/DM1")

			if path != "/v1/spaces/DM1" {
				t.Errorf("the request went to %q, want the space's own resource", path)
			}
			if tc.wantErr != nil {
				if !tc.wantErr(err) || direct {
					t.Errorf("IsDirectMessage = (%v, %v), want a failure of the kind the status maps to", direct, err)
				}
				if isTerminalWriteError(err) != tc.wantTerminal {
					t.Errorf("a %d is terminal = %v, want %v: a space the app cannot read is not retried, a rate limit and a server error are", tc.status, isTerminalWriteError(err), tc.wantTerminal)
				}
				return
			}
			if err != nil || direct != tc.want {
				t.Errorf("IsDirectMessage = (%v, %v), want (%v, nil)", direct, err, tc.want)
			}
		})
	}
}

func TestHandOverFiles_OwnerCases(t *testing.T) {
	rig := newProactiveRig(t)
	addr := proactiveAddress{Space: notifySpace, Email: notifyEmail}
	for name, tc := range map[string]struct {
		deliverTo string
		owner     string
		want      int
	}{
		"an email owner in another case":         {deliverTo: "Ada@Example.com", owner: "Ada@Example.com", want: 1},
		"a non-email owner who is the recipient": {deliverTo: "u-123", owner: "u-123", want: 1},
		"a non-email owner who is someone else":  {deliverTo: "u-123", owner: "u-456", want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			d := a2a.NotifyDeliver{UserID: tc.deliverTo, Artifacts: []a2a.NotifyArtifact{{AgentName: "a", UserID: tc.owner, SessionID: "s", Filename: "r.csv"}}}
			if files, _ := rig.a.handOverFiles(d, addr); len(files) != tc.want {
				t.Errorf("files = %+v, want %d", files, tc.want)
			}
		})
	}
}

func TestDelivery_AnUnusableAddressIsLoggedAsADefect(t *testing.T) {
	rig := newProactiveRig(t)

	rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-u", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Address: json.RawMessage(`{"space":"nope","email":"ada@example.com"}`), Title: "Report"})

	if rig.logs.count(slog.LevelError, "google chat notify deliver: unusable address") != 1 {
		t.Error("an unusable address was not logged once at ERROR")
	}
}

func TestNotifyPromptClick_AnUndecodableFormIsDropped(t *testing.T) {
	rig := newProactiveRig(t)
	replies := rig.subscribeReplies(t, "req-9")
	click := rig.promptClick(t, "req-9", hilSubmitLabel, notifyEmail)
	click.Action[paramFields] = "not json"

	if res := rig.a.process(t.Context(), click); res.outcome != outcomeIgnored {
		t.Errorf("outcome = %v, want ignored", res.outcome)
	}
	if resp, ok := rig.nextReply(t, replies, 300*time.Millisecond); ok {
		t.Errorf("reply = %+v, want none", resp)
	}
}
