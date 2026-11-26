// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"strings"
	"testing"
	"time"
)

func TestBuildExternalKey_Personal(t *testing.T) {
	activity := &Activity{
		From: Account{ID: "teams-user-123"},
		Conversation: Conversation{
			ID:               "conv-1",
			ConversationType: ConversationTypePersonal,
		},
	}

	key := buildExternalKey(activity)
	dateStr := time.Now().UTC().Format("20060102")

	want := "personal:conv-1:" + dateStr
	if key != want {
		t.Errorf("personal external key: got %q, want %q", key, want)
	}
}

func TestBuildExternalKey_PersonalKeyedByConvIDNotUserID(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{
			ID:               "conv-id-xyz",
			ConversationType: ConversationTypePersonal,
		},
	}

	key := buildExternalKey(activity)
	dateStr := time.Now().UTC().Format("20060102")
	want := "personal:conv-id-xyz:" + dateStr
	if key != want {
		t.Errorf("personal external key: got %q, want %q", key, want)
	}
}

func TestBuildExternalKey_EmptyType_TreatedAsPersonal(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{ID: "conv-2"},
	}

	key := buildExternalKey(activity)
	dateStr := time.Now().UTC().Format("20060102")
	want := "personal:conv-2:" + dateStr
	if key != want {
		t.Errorf("empty type: got %q, want %q", key, want)
	}
}

func TestBuildExternalKey_GroupChat(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{
			ID:               "19:abc123@thread.v2",
			ConversationType: ConversationTypeGroupChat,
		},
	}

	key := buildExternalKey(activity)
	dateStr := time.Now().UTC().Format("20060102")

	want := "groupChat:19:abc123@thread.v2:" + dateStr
	if key != want {
		t.Errorf("groupChat: got %q, want %q", key, want)
	}
}

func TestBuildExternalKey_Channel(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{
			ID:               "19:channel;id=5@thread.tacv2",
			ConversationType: ConversationTypeChannel,
		},
	}

	key := buildExternalKey(activity)
	dateStr := time.Now().UTC().Format("20060102")
	want := "channel:19:channel;id=5@thread.tacv2:" + dateStr
	if key != want {
		t.Errorf("channel: got %q, want %q", key, want)
	}
}

func TestBuildExternalKey_UnknownType_UsesHashFallback(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{
			ID:               "some:unknown:format",
			ConversationType: "some-future-type",
		},
	}
	key := buildExternalKey(activity)
	if !strings.HasPrefix(key, "h:") {
		t.Errorf("unknown convType key = %q, want it to start with %q", key, "h:")
	}
	if strings.Contains(key, ":unknown:format") {
		t.Errorf("unknown convType key %q leaked raw conv_id segments", key)
	}
}

func TestBuildExternalKey_OverlongFallsBackToHash(t *testing.T) {
	longConv := strings.Repeat("19:abc@thread.tacv2-", 20)
	activity := &Activity{
		Conversation: Conversation{
			ID:               longConv,
			ConversationType: ConversationTypeGroupChat,
		},
	}

	key := buildExternalKey(activity)
	if len(key) > maxExternalKeyLen {
		t.Errorf("external key (%d) exceeds max (%d): %s", len(key), maxExternalKeyLen, key)
	}
	if !strings.HasPrefix(key, "h:") {
		t.Errorf("expected hash fallback prefix h:, got %q", key)
	}
}

func TestBuildExternalKey_Deterministic(t *testing.T) {
	activity := &Activity{
		Conversation: Conversation{
			ID:               "19:abc@thread.tacv2",
			ConversationType: ConversationTypeChannel,
		},
	}
	k1 := buildExternalKey(activity)
	k2 := buildExternalKey(activity)
	if k1 != k2 {
		t.Errorf("same input produced different external keys: %q vs %q", k1, k2)
	}
}

func TestBuildExternalKey_DifferentConvIDs(t *testing.T) {
	a1 := &Activity{Conversation: Conversation{ID: "19:abc@thread.tacv2", ConversationType: ConversationTypeChannel}}
	a2 := &Activity{Conversation: Conversation{ID: "19:xyz@thread.tacv2", ConversationType: ConversationTypeChannel}}
	if buildExternalKey(a1) == buildExternalKey(a2) {
		t.Errorf("different conversation IDs produced same external key")
	}
}

func TestBuildExternalKey_GroupAndChannel_DistinctEvenForSameConvID(t *testing.T) {
	convID := "19:abc@thread.tacv2"
	group := &Activity{Conversation: Conversation{ID: convID, ConversationType: ConversationTypeGroupChat}}
	channel := &Activity{Conversation: Conversation{ID: convID, ConversationType: ConversationTypeChannel}}
	if buildExternalKey(group) == buildExternalKey(channel) {
		t.Errorf("group and channel keys should differ when convType is included")
	}
}
