// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func resetUsersCache(t *testing.T) {
	t.Helper()
	usersCacheMu.Lock()
	usersCache = map[string][]devUser{}
	usersCacheMu.Unlock()
}

func newToolContext(usersFile string) *sdk.ToolContext {
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(map[string]any{"users_file": usersFile})
	return tc
}

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadUsers_HappyPath(t *testing.T) {
	resetUsersCache(t)
	path := writeFixture(t, `[
		{"id":"a@dev.local","email":"a@dev.local","name":"A","title":"E","department":"D"},
		{"id":"b@dev.local","email":"b@dev.local","name":"B","title":"PM","department":"D"}
	]`)
	users, errMsg := loadUsers(newToolContext(path))
	if errMsg != "" {
		t.Fatalf("loadUsers: %s", errMsg)
	}
	if len(users) != 2 || users[0].ID != "a@dev.local" || users[1].Name != "B" {
		t.Errorf("loadUsers returned %+v", users)
	}
}

func TestLoadUsers_MissingPath(t *testing.T) {
	resetUsersCache(t)
	_, errMsg := loadUsers(newToolContext(""))
	if !strings.Contains(errMsg, "users_file is required") {
		t.Errorf("errMsg = %q, want mention of users_file", errMsg)
	}
}

func TestLoadUsers_BadJSON(t *testing.T) {
	resetUsersCache(t)
	path := writeFixture(t, `not json {`)
	_, errMsg := loadUsers(newToolContext(path))
	if !strings.Contains(errMsg, "failed to parse") {
		t.Errorf("errMsg = %q, want parse-failure", errMsg)
	}
}

func TestLoadUsers_EmptyArray(t *testing.T) {
	resetUsersCache(t)
	path := writeFixture(t, `[]`)
	_, errMsg := loadUsers(newToolContext(path))
	if !strings.Contains(errMsg, "no users") {
		t.Errorf("errMsg = %q, want no-users message", errMsg)
	}
}

func TestFindUser_MatchesByID(t *testing.T) {
	users := []devUser{
		{ID: "alice@dev.local", Email: "alice@dev.local", Name: "Alice"},
		{ID: "bob@dev.local", Email: "bob@dev.local", Name: "Bob"},
	}
	if got := findUser(users, "alice@dev.local"); got == nil || got.Name != "Alice" {
		t.Errorf("findUser exact = %v, want Alice", got)
	}
	if got := findUser(users, "  ALICE@dev.local  "); got == nil || got.Name != "Alice" {
		t.Errorf("findUser case+whitespace-insensitive = %v, want Alice", got)
	}
	if got := findUser(users, "missing@dev.local"); got != nil {
		t.Errorf("findUser miss = %v, want nil", got)
	}
	if got := findUser(users, ""); got != nil {
		t.Errorf("findUser empty = %v, want nil", got)
	}
}

func TestToProfile_LowercasesIDAndEmail(t *testing.T) {
	u := &devUser{
		ID:         "ALICE@dev.local",
		Email:      "ALICE@dev.local",
		Name:       "Alice Anderson",
		Title:      "Engineer",
		Department: "Eng",
	}
	p := toProfile(u)
	if p.ID != "alice@dev.local" {
		t.Errorf("Profile.ID = %q, want lowercased", p.ID)
	}
	if p.WorkEmail != "alice@dev.local" {
		t.Errorf("Profile.WorkEmail = %q, want lowercased", p.WorkEmail)
	}
	if p.DisplayName != "Alice Anderson" || p.JobTitle != "Engineer" || p.Department != "Eng" {
		t.Errorf("Profile fields not propagated: %+v", p)
	}
}
