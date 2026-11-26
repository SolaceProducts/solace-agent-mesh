// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

const testMaxAttempts = 5

func futureMillis() int64 { return time.Now().Add(time.Hour).UnixMilli() }

func nowMs() int64 { return time.Now().UnixMilli() }

func seedCandidate(t *testing.T, repo *UserChannelRepo, userID, channelType, address string) {
	t.Helper()
	if err := repo.Upsert(t.Context(), UserChannel{UserID: userID, ChannelType: channelType, Address: address}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
}

func mustStart(t *testing.T, repo *UserChannelRepo, userID, channelType, address, codeHash string) {
	t.Helper()
	if err := repo.StartConfirmation(t.Context(), userID, channelType, address, codeHash, futureMillis()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
}

func mustGet(t *testing.T, repo *UserChannelRepo, userID, channelType string) *UserChannel {
	t.Helper()
	uc, err := repo.GetByType(t.Context(), userID, channelType)
	if err != nil || uc == nil {
		t.Fatalf("GetByType(%s,%s) = %+v, %v", userID, channelType, uc, err)
	}
	return uc
}

func confirm(t *testing.T, repo *UserChannelRepo, userID, channelType, codeHash string) ConfirmOutcome {
	t.Helper()
	outcome, err := repo.ConfirmByCode(t.Context(), userID, channelType, codeHash, testMaxAttempts)
	if err != nil {
		t.Fatalf("ConfirmByCode: %v", err)
	}
	return outcome
}

func TestUserChannelRepo_UpsertGetList(t *testing.T) {
	ctx := t.Context()
	repo := NewUserChannelRepo(openTestDB(t))

	if err := repo.Upsert(ctx, UserChannel{
		UserID: "alice", ChannelType: "slack", Address: `{"channel_id":"C1"}`, Email: "alice@example.com", GatewayID: "gw-1",
	}); err != nil {
		t.Fatalf("Upsert slack: %v", err)
	}
	if err := repo.Upsert(ctx, UserChannel{
		UserID: "alice", ChannelType: "teams", Address: `{"conversation_id":"T1"}`, Email: "alice@example.com",
	}); err != nil {
		t.Fatalf("Upsert teams: %v", err)
	}

	got := mustGet(t, repo, "alice", "slack")
	if got.Address != `{"channel_id":"C1"}` || got.Email != "alice@example.com" || got.GatewayID != "gw-1" {
		t.Fatalf("GetByType slack = %+v, want the inserted row", got)
	}

	if err := repo.Upsert(ctx, UserChannel{
		UserID: "alice", ChannelType: "slack", Address: `{"channel_id":"C2"}`, Email: "alice@example.com", GatewayID: "gw-2",
	}); err != nil {
		t.Fatalf("Upsert slack (update): %v", err)
	}
	got = mustGet(t, repo, "alice", "slack")
	if got.Address != `{"channel_id":"C2"}` || got.GatewayID != "gw-2" {
		t.Fatalf("upsert should overwrite: got address=%q gateway=%q", got.Address, got.GatewayID)
	}

	all, err := repo.ListByUser(ctx, "alice")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListByUser len = %d, want 2 (slack + teams)", len(all))
	}
}

func TestUserChannelRepo_GetByTypeMiss(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	got, err := repo.GetByType(t.Context(), "nobody", "slack")
	if err != nil || got != nil {
		t.Fatalf("GetByType miss = %+v, %v; want nil, nil", got, err)
	}
}

func TestUserChannelRepo_UpsertRejectsEmptyKeys(t *testing.T) {
	ctx := t.Context()
	repo := NewUserChannelRepo(openTestDB(t))
	if err := repo.Upsert(ctx, UserChannel{ChannelType: "slack", Address: "{}"}); err == nil {
		t.Fatal("Upsert with empty user_id should error")
	}
	if err := repo.Upsert(ctx, UserChannel{UserID: "alice", Address: "{}"}); err == nil {
		t.Fatal("Upsert with empty channel_type should error")
	}
}

func TestUserChannelRepo_PresenceCandidateIsNotConfirmed(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", `{"channel_id":"C1"}`)
	if uc := mustGet(t, repo, "alice", "slack"); uc.Confirmed() || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("a presence candidate must be neither confirmed nor pending: %+v", uc)
	}
}

func TestUserChannelRepo_ConfirmationLifecycle(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	addr := `{"channel_id":"C1"}`
	seedCandidate(t, repo, "alice", "slack", addr)
	mustStart(t, repo, "alice", "slack", addr, "hash-1")

	uc := mustGet(t, repo, "alice", "slack")
	if uc.Confirmed() || !uc.CodeUsableAt(nowMs(), testMaxAttempts) {
		t.Fatalf("after start: want a usable code and no confirmation, got %+v", uc)
	}

	if got := confirm(t, repo, "alice", "slack", "hash-1"); got != ConfirmOK {
		t.Fatalf("ConfirmByCode = %v, want ConfirmOK", got)
	}
	uc = mustGet(t, repo, "alice", "slack")
	if uc.ConfirmedAddress != addr || uc.ConfirmedAt == 0 || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("after confirm: want confirmed %q with no outstanding code, got %+v", addr, uc)
	}

	if got := confirm(t, repo, "alice", "slack", "hash-1"); got != ConfirmAlreadyConfirmed {
		t.Fatalf("resubmitting after success = %v, want ConfirmAlreadyConfirmed", got)
	}
}

func TestUserChannelRepo_ConfirmWithNoRowIsExpiredNotAPanic(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if got := confirm(t, repo, "nobody", "slack", "h"); got != ConfirmExpired {
		t.Fatalf("confirm with no row = %v, want ConfirmExpired", got)
	}
	seedCandidate(t, repo, "alice", "slack", "{}")
	if got := confirm(t, repo, "alice", "slack", "h"); got != ConfirmExpired {
		t.Fatalf("confirm with no code issued = %v, want ConfirmExpired", got)
	}
}

func TestUserChannelRepo_ConfirmAddressConfirmsTheRowAndDropsTheCode(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", `{"user_id":"U1"}`)
	mustStart(t, repo, "alice", "slack", `{"user_id":"U1"}`, "h")
	if got := confirm(t, repo, "alice", "slack", "wrong"); got != ConfirmInvalid {
		t.Fatalf("a wrong guess = %v, want ConfirmInvalid", got)
	}

	before := nowMs()
	if err := repo.ConfirmAddress(t.Context(), "alice", "slack", `{"user_id":"U2"}`); err != nil {
		t.Fatalf("ConfirmAddress: %v", err)
	}
	uc := mustGet(t, repo, "alice", "slack")
	if uc.ConfirmedAddress != `{"user_id":"U2"}` || uc.ConfirmedAt < before || !uc.Confirmed() {
		t.Fatalf("row = %+v, want confirmed at the given address just now", uc)
	}
	if uc.PendingExpiresAt != 0 || uc.PendingAttempts != 0 || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("row = %+v, want the outstanding code and its attempts cleared", uc)
	}
	if got := confirm(t, repo, "alice", "slack", "h"); got != ConfirmAlreadyConfirmed {
		t.Fatalf("redeeming the superseded code = %v, want ConfirmAlreadyConfirmed", got)
	}
	var pendingAddress, pendingHash string
	if err := repo.db.QueryRowContext(t.Context(),
		`SELECT pending_address, pending_code_hash FROM notify_user_channels WHERE user_id = ? AND channel_type = ?`, "alice", "slack").
		Scan(&pendingAddress, &pendingHash); err != nil {
		t.Fatalf("read pending columns: %v", err)
	}
	if pendingAddress != "" || pendingHash != "" {
		t.Fatalf("pending address %q and code hash %q must both be cleared", pendingAddress, pendingHash)
	}
}

func TestUserChannelRepo_ConfirmAddressNeedsARowAndAnAddress(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if err := repo.ConfirmAddress(t.Context(), "alice", "slack", "{}"); !errors.Is(err, ErrNoCandidateAddress) {
		t.Fatalf("confirm with no row = %v, want ErrNoCandidateAddress", err)
	}
	seedCandidate(t, repo, "alice", "slack", "{}")
	for name, args := range map[string][3]string{
		"no user":    {"", "slack", "{}"},
		"no channel": {"alice", "", "{}"},
		"no address": {"alice", "slack", ""},
	} {
		if err := repo.ConfirmAddress(t.Context(), args[0], args[1], args[2]); err == nil {
			t.Errorf("%s: ConfirmAddress succeeded, want an error", name)
		}
	}
	if uc := mustGet(t, repo, "alice", "slack"); uc.Confirmed() {
		t.Fatalf("a rejected call confirmed the row: %+v", uc)
	}
}

func TestUserChannelRepo_ConfirmAddressLeavesOtherRowsAlone(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	seedCandidate(t, repo, "bob", "slack", "{}")
	seedCandidate(t, repo, "alice", "teams", "{}")
	if err := repo.ConfirmAddress(t.Context(), "alice", "slack", "{}"); err != nil {
		t.Fatalf("ConfirmAddress: %v", err)
	}
	for _, other := range [][2]string{{"bob", "slack"}, {"alice", "teams"}} {
		if uc := mustGet(t, repo, other[0], other[1]); uc.Confirmed() {
			t.Errorf("%s on %s was confirmed too: %+v", other[0], other[1], uc)
		}
	}
}

func TestUserChannelRepo_StartWithoutRow(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if err := repo.StartConfirmation(t.Context(), "alice", "slack", "{}", "h", futureMillis()); !errors.Is(err, ErrNoCandidateAddress) {
		t.Fatalf("start with no row = %v, want ErrNoCandidateAddress", err)
	}
}

func TestUserChannelRepo_StartPreservesProvenance(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if err := repo.Upsert(t.Context(), UserChannel{
		UserID: "alice", ChannelType: "slack", Address: "{}", Email: "alice@example.com", GatewayID: "gw-1",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	mustStart(t, repo, "alice", "slack", "{}", "h")
	if uc := mustGet(t, repo, "alice", "slack"); uc.Email != "alice@example.com" || uc.GatewayID != "gw-1" {
		t.Fatalf("start must keep email/gateway_id, got %+v", uc)
	}
}

func TestUserChannelRepo_RowsArePerUserAndChannel(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	aliceSlack, aliceTeams, bobSlack := `{"u":"alice-slack"}`, `{"u":"alice-teams"}`, `{"u":"bob-slack"}`
	seedCandidate(t, repo, "alice", "slack", aliceSlack)
	seedCandidate(t, repo, "alice", "teams", aliceTeams)
	seedCandidate(t, repo, "bob", "slack", bobSlack)

	mustStart(t, repo, "alice", "slack", aliceSlack, "alice-slack-hash")
	if uc := mustGet(t, repo, "bob", "slack"); uc.Address != bobSlack || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("starting alice/slack must not touch bob/slack, got %+v", uc)
	}
	if uc := mustGet(t, repo, "alice", "teams"); uc.Address != aliceTeams || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("starting alice/slack must not touch alice/teams, got %+v", uc)
	}

	if got := confirm(t, repo, "bob", "slack", "alice-slack-hash"); got == ConfirmOK {
		t.Fatal("another user must not redeem the code")
	}
	if got := confirm(t, repo, "alice", "teams", "alice-slack-hash"); got == ConfirmOK {
		t.Fatal("the code must not confirm a different channel")
	}
	if got := confirm(t, repo, "alice", "slack", "alice-slack-hash"); got != ConfirmOK {
		t.Fatalf("owner redeem = %v, want ConfirmOK", got)
	}
	if uc := mustGet(t, repo, "bob", "slack"); uc.Confirmed() {
		t.Fatalf("bob/slack must stay unconfirmed, got %+v", uc)
	}

	aliceRows, err := repo.ListByUser(t.Context(), "alice")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	for _, uc := range aliceRows {
		if uc.UserID != "alice" {
			t.Fatalf("ListByUser(alice) returned another user's row: %+v", uc)
		}
	}
	if len(aliceRows) != 2 {
		t.Fatalf("ListByUser(alice) = %d rows, want 2", len(aliceRows))
	}
}

func TestUserChannelRepo_WrongGuessesOnlyCountAgainstTheirOwnRow(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	for _, k := range []struct{ user, channel string }{{"alice", "slack"}, {"alice", "teams"}, {"bob", "slack"}} {
		seedCandidate(t, repo, k.user, k.channel, "{}")
		mustStart(t, repo, k.user, k.channel, "{}", k.user+"-"+k.channel)
	}
	for range testMaxAttempts + 1 {
		confirm(t, repo, "alice", "slack", "wrong")
	}
	if got := confirm(t, repo, "alice", "teams", "alice-teams"); got != ConfirmOK {
		t.Fatalf("alice/teams after guesses on alice/slack = %v, want ConfirmOK", got)
	}
	if got := confirm(t, repo, "bob", "slack", "bob-slack"); got != ConfirmOK {
		t.Fatalf("bob/slack after guesses on alice/slack = %v, want ConfirmOK", got)
	}
}

func TestUserChannelRepo_WrongCodesLockTheConfirmation(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	mustStart(t, repo, "alice", "slack", "{}", "right")

	for i := range testMaxAttempts {
		if got := confirm(t, repo, "alice", "slack", "wrong"); got != ConfirmInvalid {
			t.Fatalf("wrong attempt %d = %v, want ConfirmInvalid", i+1, got)
		}
	}
	uc := mustGet(t, repo, "alice", "slack")
	if !uc.CodeLockedAt(nowMs(), testMaxAttempts) || uc.CodeUsableAt(nowMs(), testMaxAttempts) {
		t.Fatalf("after %d wrong attempts the code must read as locked, got %+v", testMaxAttempts, uc)
	}
	if got := confirm(t, repo, "alice", "slack", "right"); got != ConfirmLocked {
		t.Fatalf("right code after lockout = %v, want ConfirmLocked", got)
	}

	mustStart(t, repo, "alice", "slack", "{}", "fresh")
	if got := confirm(t, repo, "alice", "slack", "fresh"); got != ConfirmOK {
		t.Fatalf("a new code after lockout = %v, want ConfirmOK", got)
	}
}

func TestUserChannelRepo_ExpiredCodeDoesNotConfirm(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	if err := repo.StartConfirmation(t.Context(), "alice", "slack", "{}", "h", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
	if got := confirm(t, repo, "alice", "slack", "h"); got != ConfirmExpired {
		t.Fatalf("expired code = %v, want ConfirmExpired", got)
	}
	if uc := mustGet(t, repo, "alice", "slack"); uc.Confirmed() || uc.CodeOutstandingAt(nowMs()) {
		t.Fatalf("an expired code must neither confirm nor read as outstanding, got %+v", uc)
	}
}

func TestUserChannelRepo_RestartReplacesOutstandingCode(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	mustStart(t, repo, "alice", "slack", "{}", "first")
	mustStart(t, repo, "alice", "slack", "{}", "second")

	if got := confirm(t, repo, "alice", "slack", "first"); got != ConfirmInvalid {
		t.Fatalf("superseded code = %v, want ConfirmInvalid", got)
	}
	if got := confirm(t, repo, "alice", "slack", "second"); got != ConfirmOK {
		t.Fatalf("current code = %v, want ConfirmOK", got)
	}
}

func TestUserChannelRepo_PresenceDuringPendingDoesNotChangeWhatIsConfirmed(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	sentTo := `{"channel_id":"SENT"}`
	seedCandidate(t, repo, "alice", "slack", sentTo)
	mustStart(t, repo, "alice", "slack", sentTo, "h")

	seedCandidate(t, repo, "alice", "slack", `{"channel_id":"LATER"}`)
	if got := confirm(t, repo, "alice", "slack", "h"); got != ConfirmOK {
		t.Fatalf("ConfirmByCode = %v, want ConfirmOK", got)
	}
	if uc := mustGet(t, repo, "alice", "slack"); uc.ConfirmedAddress != sentTo {
		t.Fatalf("confirmed %q, want the address the code was sent to (%q)", uc.ConfirmedAddress, sentTo)
	}
}

func TestUserChannelRepo_ReverifyKeepsWorkingChannelUntilRedeemed(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	first := `{"channel_id":"FIRST"}`
	second := `{"channel_id":"SECOND"}`
	seedCandidate(t, repo, "alice", "slack", first)
	mustStart(t, repo, "alice", "slack", first, "h1")
	confirm(t, repo, "alice", "slack", "h1")

	seedCandidate(t, repo, "alice", "slack", second)
	if uc := mustGet(t, repo, "alice", "slack"); uc.ConfirmedAddress != first {
		t.Fatalf("presence must not move a confirmed address, got %+v", uc)
	}
	mustStart(t, repo, "alice", "slack", second, "h2")
	if uc := mustGet(t, repo, "alice", "slack"); uc.ConfirmedAddress != first {
		t.Fatalf("starting a re-verification must not demote the working channel, got %+v", uc)
	}
	confirm(t, repo, "alice", "slack", "h2")
	if uc := mustGet(t, repo, "alice", "slack"); uc.ConfirmedAddress != second {
		t.Fatalf("redeeming the new code must move delivery to %q, got %+v", second, uc)
	}
}

func TestUserChannelRepo_RequiresArguments(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if _, err := repo.ConfirmByCode(context.Background(), "", "slack", "h", testMaxAttempts); err == nil {
		t.Fatal("ConfirmByCode with empty user must error")
	}
	if err := repo.StartConfirmation(context.Background(), "alice", "slack", "{}", "", futureMillis()); err == nil {
		t.Fatal("StartConfirmation with empty hash must error")
	}
	if err := repo.StartConfirmation(context.Background(), "alice", "slack", "", "h", futureMillis()); err == nil {
		t.Fatal("StartConfirmation with empty address must error")
	}
}

func TestUserChannelRepo_ReverifyWithExpiredCodeOnConfirmedChannel(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	mustStart(t, repo, "alice", "slack", "{}", "old")
	if got := confirm(t, repo, "alice", "slack", "old"); got != ConfirmOK {
		t.Fatalf("setup confirm = %v", got)
	}
	if err := repo.StartConfirmation(t.Context(), "alice", "slack", "{}", "new", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
	if got := confirm(t, repo, "alice", "slack", "new"); got != ConfirmExpired {
		t.Fatalf("expired re-verify code on a confirmed channel = %v, want ConfirmExpired (not AlreadyConfirmed)", got)
	}
}

func TestUserChannelRepo_ReverifyWithLockedCodeOnConfirmedChannel(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	mustStart(t, repo, "alice", "slack", "{}", "old")
	confirm(t, repo, "alice", "slack", "old")
	mustStart(t, repo, "alice", "slack", "{}", "new")
	for range testMaxAttempts {
		confirm(t, repo, "alice", "slack", "bad")
	}
	if got := confirm(t, repo, "alice", "slack", "new"); got != ConfirmLocked {
		t.Fatalf("locked re-verify code on a confirmed channel = %v, want ConfirmLocked", got)
	}
}

func TestUserChannelRepo_NoRowIgnoresOtherUsersConfirmedRows(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "bob", "slack", "{}")
	mustStart(t, repo, "bob", "slack", "{}", "b")
	confirm(t, repo, "bob", "slack", "b")
	if got := confirm(t, repo, "alice", "slack", "x"); got != ConfirmExpired {
		t.Fatalf("alice with no row = %v, want ConfirmExpired", got)
	}
	if got := confirm(t, repo, "bob", "teams", "x"); got != ConfirmExpired {
		t.Fatalf("bob on a channel with no row = %v, want ConfirmExpired", got)
	}
}

func TestUserChannelRepo_WrongGuessAgainstExpiredCode(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", "{}")
	if err := repo.StartConfirmation(t.Context(), "alice", "slack", "{}", "h", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
	if got := confirm(t, repo, "alice", "slack", "wrong"); got != ConfirmExpired {
		t.Fatalf("wrong guess against an expired code = %v, want ConfirmExpired", got)
	}
}

type pendingCodeColumns struct{ address, codeHash string }

func pendingColumns(t *testing.T, repo *UserChannelRepo, userID, channelType string) pendingCodeColumns {
	t.Helper()
	var p pendingCodeColumns
	err := repo.db.QueryRowContext(t.Context(),
		`SELECT pending_address, pending_code_hash FROM notify_user_channels WHERE user_id = ? AND channel_type = ?`,
		userID, channelType).Scan(&p.address, &p.codeHash)
	if err != nil {
		t.Fatalf("read pending columns: %v", err)
	}
	return p
}

func TestUserChannelRepo_ClearConfirmationTurnsTheChannelOffAndKeepsTheRow(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	seedCandidate(t, repo, "alice", "slack", `{"user_id":"U1"}`)
	if err := repo.ConfirmAddress(t.Context(), "alice", "slack", `{"user_id":"U1"}`); err != nil {
		t.Fatalf("ConfirmAddress: %v", err)
	}
	mustStart(t, repo, "alice", "slack", `{"user_id":"U1"}`, "h")
	if got := confirm(t, repo, "alice", "slack", "wrong"); got != ConfirmInvalid {
		t.Fatalf("a wrong guess = %v, want ConfirmInvalid", got)
	}
	pendingBefore := pendingColumns(t, repo, "alice", "slack")
	if pendingBefore.address == "" || pendingBefore.codeHash == "" || mustGet(t, repo, "alice", "slack").PendingAttempts != 1 {
		t.Fatalf("before clearing: pending = %+v, attempts = %d, want an outstanding code with one wrong guess", pendingBefore, mustGet(t, repo, "alice", "slack").PendingAttempts)
	}

	if err := repo.ClearConfirmation(t.Context(), "alice", "slack"); err != nil {
		t.Fatalf("ClearConfirmation: %v", err)
	}

	if got := pendingColumns(t, repo, "alice", "slack"); got.address != "" || got.codeHash != "" {
		t.Fatalf("pending columns after clearing = %+v, want both empty", got)
	}
	uc := mustGet(t, repo, "alice", "slack")
	if uc.Confirmed() || uc.ConfirmedAt != 0 {
		t.Fatalf("row = %+v, want the channel off", uc)
	}
	if uc.CodeOutstandingAt(nowMs()) || uc.PendingAttempts != 0 {
		t.Fatalf("row = %+v, want the outstanding code cleared", uc)
	}
	if uc.Address != `{"user_id":"U1"}` {
		t.Fatalf("address = %q, want the learned address kept so the channel can be turned on again", uc.Address)
	}
}

func TestUserChannelRepo_ClearConfirmationIsIdempotentAndScoped(t *testing.T) {
	repo := NewUserChannelRepo(openTestDB(t))
	if err := repo.ClearConfirmation(t.Context(), "alice", "slack"); err != nil {
		t.Fatalf("clearing a channel with no row = %v, want no error", err)
	}
	seedCandidate(t, repo, "alice", "slack", "{}")
	seedCandidate(t, repo, "bob", "slack", "{}")
	seedCandidate(t, repo, "alice", "teams", "{}")
	for _, row := range [][2]string{{"alice", "slack"}, {"bob", "slack"}, {"alice", "teams"}} {
		if err := repo.ConfirmAddress(t.Context(), row[0], row[1], "{}"); err != nil {
			t.Fatalf("ConfirmAddress(%v): %v", row, err)
		}
	}

	for range 2 {
		if err := repo.ClearConfirmation(t.Context(), "alice", "slack"); err != nil {
			t.Fatalf("ClearConfirmation: %v", err)
		}
	}

	if mustGet(t, repo, "alice", "slack").Confirmed() {
		t.Fatal("alice's slack channel is still on")
	}
	if !mustGet(t, repo, "bob", "slack").Confirmed() || !mustGet(t, repo, "alice", "teams").Confirmed() {
		t.Fatal("clearing one channel turned off another user's or another channel")
	}
	if err := repo.ClearConfirmation(t.Context(), "", "slack"); err == nil {
		t.Error("ClearConfirmation without a user succeeded, want an error")
	}
	if err := repo.ClearConfirmation(t.Context(), "alice", ""); err == nil {
		t.Error("ClearConfirmation without a channel succeeded, want an error")
	}
}
