// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func chatTaskOf(user, bubbles string, metadata *string) *store.ChatTask {
	return &store.ChatTask{ID: "t-" + user, UserID: user, MessageBubbles: bubbles, TaskMetadata: metadata}
}

func referencedBy(t *testing.T, bubbles string) ReferencedUserFiles {
	t.Helper()
	refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{chatTaskOf("alice", bubbles, nil)}, "alice")
	if err != nil {
		t.Fatalf("UserScopedFilesReferencedBy: %v", err)
	}
	return refs
}

func TestUserScopedFilesReferencedByReadsEveryReferenceFormThatNamesAVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bubbles string
		want    ReferencedUserFiles
	}{
		{"return marker", `[{"text":"ok«artifact_return:user:a.md:2»"}]`, ReferencedUserFiles{"user:a.md": 2}},
		{"plain marker", `[{"text":"«artifact:user:a.md:0»"}]`, ReferencedUserFiles{"user:a.md": 0}},
		{"marker in triple guillemets", `[{"text":"«««artifact_return:user:a.md:4»»»"}]`, ReferencedUserFiles{"user:a.md": 4}},
		{"marker in a part's text", `[{"parts":[{"kind":"text","text":"«artifact:user:a.md:1»"}]}]`, ReferencedUserFiles{"user:a.md": 1}},
		{"a marker for a name that ends in a colon and digits", `[{"text":"«artifact_return:user:data:2024:1»"}]`, ReferencedUserFiles{"user:data:2024": 1}},
		{"a file URI for a name that ends in a colon and digits", `[{"parts":[{"kind":"file","file":{"uri":"artifact://s/user:data:2024?version=1"}}]}]`, ReferencedUserFiles{"user:data:2024": 1}},
		{"an attachment named like a time", `[{"attachedArtifacts":[{"filename":"user:meeting 10:30","uri":"artifact://s/user:meeting%2010:30?version=2"}]}]`, ReferencedUserFiles{"user:meeting 10:30": 2}},
		{"artifact part file URI", `[{"parts":[{"kind":"artifact","name":"user:a.md","file":{"name":"user:a.md","uri":"artifact://sess/user:a.md?version=5"}}]}]`, ReferencedUserFiles{"user:a.md": 5}},
		{"file part full URI", `[{"parts":[{"kind":"file","file":{"uri":"artifact://app/u/s/user:a.md?version=3"}}]}]`, ReferencedUserFiles{"user:a.md": 3}},
		{"percent-encoded URI", `[{"parts":[{"kind":"file","file":{"uri":"artifact://app/u/s/user%3Aa%20b.md?version=1"}}]}]`, ReferencedUserFiles{"user:a b.md": 1}},
		{"attached files", `[{"files":[{"name":"user:a.md","uri":"artifact://s/user:a.md?version=2"}]}]`, ReferencedUserFiles{"user:a.md": 2}},
		{"attached artifacts", `[{"attachedArtifacts":[{"filename":"user:a.md","uri":"artifact://s/user:a.md?version=7","mimeType":"m"}]}]`, ReferencedUserFiles{"user:a.md": 7}},
		{"artifact notification", `[{"artifactNotification":{"name":"user:a.md","version":1}}]`, ReferencedUserFiles{"user:a.md": 1}},
		{"the highest version a chat names", `[{"text":"«artifact:user:a.md:1»"},{"text":"«artifact:user:a.md:3»«artifact:user:a.md:2»"}]`, ReferencedUserFiles{"user:a.md": 3}},
		{"several files", `[{"text":"«artifact:user:a.md:0»«artifact:user:b.md:1»"}]`, ReferencedUserFiles{"user:a.md": 0, "user:b.md": 1}},
		{"empty", ``, ReferencedUserFiles{}},
		{"no bubbles", `[]`, ReferencedUserFiles{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := referencedBy(t, tc.bubbles); !maps.Equal(got, tc.want) {
				t.Errorf("references %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUserScopedFilesReferencedByNamesNoVersionForAReferenceWithoutOne(t *testing.T) {
	for name, bubbles := range map[string]string{
		"a bare marker":         `[{"text":"«artifact_return:user:a.md»"}]`,
		"a latest marker":       `[{"text":"«artifact:user:a.md:latest»"}]`,
		"an artifact part name": `[{"parts":[{"kind":"artifact","name":"user:a.md"}]}]`,
		"a structured name that ends in a colon and digits is not a version": `[{"parts":[{"kind":"artifact","name":"user:data:2024","file":{"name":"user:data:2024"}}],"attachedArtifacts":[{"filename":"user:meeting 10:30"}],"files":[{"name":"user:a.md:3"}]}]`,
		"a file part name":                          `[{"parts":[{"kind":"file","file":{"name":"user:a.md"}}]}]`,
		"an unversioned URI":                        `[{"parts":[{"kind":"file","file":{"uri":"artifact://sess/user:a.md"}}]}]`,
		"a URI with a version that is not a number": `[{"parts":[{"kind":"file","file":{"uri":"artifact://sess/user:a.md?version=x"}}]}]`,
		"an uploaded file":                          `[{"uploadedFiles":[{"name":"user:a.md","type":"text/plain"}]}]`,
		"a notification without one":                `[{"artifactNotification":{"name":"user:a.md"}}]`,
		"a notification with a fraction":            `[{"artifactNotification":{"name":"user:a.md","version":1.5}}]`,
		"a notification with a negative":            `[{"artifactNotification":{"name":"user:a.md","version":-1}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := referencedBy(t, bubbles); len(got) != 0 {
				t.Errorf("references %v, want none", got)
			}
		})
	}
}

func TestUserScopedFilesReferencedByIgnoresWhatDoesNotNameAUserScopedFile(t *testing.T) {
	for name, bubbles := range map[string]string{
		"a mention in prose":                     `[{"text":"I saved user:a.md:1 earlier"}]`,
		"a name on an object that is not a file": `[{"parts":[{"kind":"text","name":"user:a.md:1"}]}]`,
		"a session-scoped name":                  `[{"text":"«artifact_return:a.md:1»"}]`,
		"a name on a data part":                  `[{"parts":[{"kind":"data","name":"user:a.md:1"}]}]`,
		"the prefix in another case":             `[{"text":"«artifact_return:User:a.md:1»"}]`,
		"the prefix inside a name":               `[{"text":"«artifact_return:my-user:a.md:1»"}]`,
		"the bare prefix":                        `[{"text":"«artifact_return:user::1»"}]`,
		"a URI that is not an artifact":          `[{"parts":[{"kind":"file","file":{"uri":"https://h/user:a.md?version=1"}}]}]`,
		"a URI after other text":                 `[{"parts":[{"kind":"text","text":"see artifact://app/u/s/user:a.md?version=1"}]}]`,
		"a name whose value is not text":         `[{"parts":[{"kind":"artifact","name":["user:a.md:1"]}]}]`,
		"a marker with no closing":               `[{"text":"«artifact_return:user:a.md:1"}]`,
		"the model's thinking":                   `[{"thinkingContent":"«artifact:user:a.md:1»"}]`,
		"a progress note":                        `[{"progressUpdates":[{"text":"«artifact:user:a.md:1»"}]}]`,
		"rendered HTML":                          `[{"displayHtml":"«artifact:user:a.md:1»"}]`,
		"a quoted context":                       `[{"contextQuote":"«artifact:user:a.md:1»"}]`,
		"a data part":                            `[{"parts":[{"kind":"data","data":{"uri":"artifact://s/user:a.md?version=1","name":"user:a.md"}}]}]`,
		"retrieved text on a bubble":             `[{"ragData":[{"text":"«artifact:user:a.md:1»"}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := referencedBy(t, bubbles); len(got) != 0 {
				t.Errorf("references %v, want none", got)
			}
		})
	}
}

func TestUserScopedFilesReferencedByDoesNotReadTaskMetadata(t *testing.T) {
	metadata := `{"ragData":[{"text":"«artifact:user:a.md:1»","uri":"artifact://s/user:a.md?version=1"}],"note":"«artifact:user:a.md:1»"}`
	refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{chatTaskOf("alice", `[]`, &metadata)}, "alice")
	if err != nil || len(refs) != 0 {
		t.Errorf("references = %v (err %v), want none from metadata", refs, err)
	}
}

func TestUserScopedFilesReferencedByCountsOnlyTheOwnersTasks(t *testing.T) {
	bubbles := `[{"text":"«artifact:user:a.md:1»"}]`
	tasks := []*store.ChatTask{chatTaskOf("bob", bubbles, nil)}
	if refs, err := UserScopedFilesReferencedBy(tasks, "alice"); err != nil || len(refs) != 0 {
		t.Errorf("another user's task vouched for %v (err %v)", refs, err)
	}
	for _, owner := range []string{"bob", "BOB"} {
		if refs, err := UserScopedFilesReferencedBy(tasks, owner); err != nil || !refs.Has("user:a.md", 1) {
			t.Errorf("owner %q: references %v (err %v), want the owner's own task counted", owner, refs, err)
		}
	}
	kates := []*store.ChatTask{chatTaskOf("kate@x.com", bubbles, nil)}
	if refs, err := UserScopedFilesReferencedBy(kates, "Kate@x.com"); err != nil || len(refs) != 0 {
		t.Errorf("an owner id that Unicode folding merges with kate's counted kate's task: %v (err %v)", refs, err)
	}
}

func TestUserScopedFilesReferencedByDoesNotLetAForkCopyVouch(t *testing.T) {
	bubbles := `[{"text":"«artifact:user:a.md:1»"}]`
	for name, metadata := range map[string]string{
		"a copy of another user's chat":                     `{"forkedFromOwnerId":"mallory","forkedFrom":"t1"}`,
		"a copy of the owner's own fork":                    `{"forkedFromOwnerId":"alice"}`,
		"a copy a fork made before the keys were camelCase": `{"forked_from_owner_id":"mallory","forked_from":"t1","forked_from_session_id":"s1"}`,
		"a copy that has only one snake_case key":           `{"forked_from":"t1"}`,
		"a copy that has only the session key":              `{"forkedFromSessionId":"s1"}`,
		"a copy whose source is not text":                   `{"forkedFromOwnerId":["alice"]}`,
		"a copy with an empty source":                       `{"forkedFromOwnerId":""}`,
		"a copy that also carries other keys":               `{"schemaVersion":1,"forkedFromOwnerId":"mallory","status":"completed"}`,
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{chatTaskOf("alice", bubbles, &metadata)}, "alice")
			if err != nil || len(refs) != 0 {
				t.Errorf("references = %v (err %v), want none from a task a fork created", refs, err)
			}
		})
	}
	t.Run("a task of the owner without the fork key still vouches", func(t *testing.T) {
		metadata := `{"schemaVersion":1,"status":"completed"}`
		refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{chatTaskOf("alice", bubbles, &metadata)}, "alice")
		if err != nil || !refs.Has("user:a.md", 1) {
			t.Errorf("references = %v (err %v), want user:a.md at version 1", refs, err)
		}
	})
}

func TestUserScopedFilesReferencedByKeepsWhatReadableTasksReferenceAndReportsTheRest(t *testing.T) {
	good := &store.ChatTask{ID: "good", UserID: "alice", MessageBubbles: `[{"text":"«artifact:user:a.md:1»"}]`}
	badMetadata := `{"unterminated`
	for name, bad := range map[string]*store.ChatTask{
		"bubbles that are not JSON":      {ID: "bad", UserID: "alice", MessageBubbles: `[{"text":`},
		"bubbles that are not a list":    {ID: "bad", UserID: "alice", MessageBubbles: `{"text":"x"}`},
		"bubbles that hold a non-object": {ID: "bad", UserID: "alice", MessageBubbles: `["x"]`},
		"metadata that is not JSON":      {ID: "bad", UserID: "alice", MessageBubbles: `[{"text":"«artifact:user:b.md:1»"}]`, TaskMetadata: &badMetadata},
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{good, bad}, "alice")
			if !errors.Is(err, ErrUnreadableTasks) || !strings.Contains(err.Error(), "bad") || strings.Contains(err.Error(), "good") {
				t.Errorf("err = %v, want ErrUnreadableTasks naming only the bad task", err)
			}
			if !maps.Equal(refs, ReferencedUserFiles{"user:a.md": 1}) {
				t.Errorf("references = %v, want what the readable task references and nothing the unreadable one holds", refs)
			}
		})
	}
	t.Run("another user's unreadable task is not read", func(t *testing.T) {
		if _, err := UserScopedFilesReferencedBy([]*store.ChatTask{good, chatTaskOf("bob", `[{`, nil)}, "alice"); err != nil {
			t.Errorf("err = %v, want none for a task of another user", err)
		}
	})
}

func TestReferencedUserFilesHasNeedsTheVersion(t *testing.T) {
	refs := ReferencedUserFiles{"user:a.md": 2}
	for version, want := range map[int]bool{2: true, 1: false, 3: false, -1: false} {
		if got := refs.Has("user:a.md", version); got != want {
			t.Errorf("Has(user:a.md, %d) = %v, want %v", version, got, want)
		}
	}
	if refs.Has("user:b.md", 2) {
		t.Error("an unreferenced name is vouched for")
	}
	var none ReferencedUserFiles
	if none.Has("user:a.md", 0) {
		t.Error("a nil set vouches for a name")
	}
}
