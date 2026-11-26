// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestNotificationRepo_AddListMarkRead(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	id, err := repo.Add(ctx, "trent", "Report ready", "Your report is ready.", map[string]any{"task_id": "t1"}, "notify_user", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatal("expected a generated id")
	}

	rows, err := repo.ListByUser(ctx, "trent", InboxFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id || rows[0].Status != "unread" || rows[0].Title != "Report ready" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].Payload == "" {
		t.Error("expected payload JSON to be stored")
	}

	if n, _ := repo.CountByUser(ctx, "trent", InboxFilter{}); n != 1 {
		t.Errorf("total count = %d, want 1", n)
	}
	if n, _ := repo.CountByUser(ctx, "trent", InboxFilter{UnreadOnly: true}); n != 1 {
		t.Errorf("unread count = %d, want 1", n)
	}

	if otherRows, _ := repo.ListByUser(ctx, "someone-else", InboxFilter{}, 10, 0); len(otherRows) != 0 {
		t.Errorf("expected empty inbox for other user, got %d", len(otherRows))
	}

	if err := repo.MarkRead(ctx, "trent", id); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if n, _ := repo.CountByUser(ctx, "trent", InboxFilter{UnreadOnly: true}); n != 0 {
		t.Errorf("unread count after read = %d, want 0", n)
	}
	rows, _ = repo.ListByUser(ctx, "trent", InboxFilter{}, 10, 0)
	if len(rows) != 1 || rows[0].Status != "read" || rows[0].ReadAt == nil {
		t.Fatalf("expected one read row with readAt, got %+v", rows)
	}
}

func TestNotificationRepo_DeleteOlderThan(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	oldRead, _ := repo.Add(ctx, "trent", "old-read", "b", nil, "notify_user", "")
	oldUnread, _ := repo.Add(ctx, "trent", "old-unread", "b", nil, "notify_user", "")
	_, _ = repo.Add(ctx, "trent", "new", "b", nil, "notify_user", "")

	if err := repo.MarkRead(ctx, "trent", oldRead); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	for _, id := range []string{oldRead, oldUnread} {
		if _, err := repo.db.ExecContext(ctx,
			`UPDATE notifications SET created_at = ? WHERE id = ?`, int64(1000), id); err != nil {
			t.Fatalf("age row: %v", err)
		}
	}

	deleted, err := repo.DeleteOlderThan(ctx, 100000)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted (old+read only), got %d", deleted)
	}
	rows, _ := repo.ListByUser(ctx, "trent", InboxFilter{}, 10, 0)
	titles := map[string]bool{}
	for _, r := range rows {
		titles[r.Title] = true
	}
	if titles["old-read"] {
		t.Error("old read notification should have been pruned")
	}
	if !titles["old-unread"] {
		t.Error("old UNREAD notification must be retained")
	}
	if !titles["new"] {
		t.Error("new notification must be retained")
	}
}

func TestNotificationRepo_MarkReadNotFound(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	id, _ := repo.Add(ctx, "trent", "T", "B", nil, "notify_user", "")

	if err := repo.MarkRead(ctx, "trent", absentID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("MarkRead(missing) = %v, want sql.ErrNoRows", err)
	}
	if err := repo.MarkRead(ctx, "intruder", id); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("MarkRead(wrong user) = %v, want sql.ErrNoRows", err)
	}
}

func TestNotificationRepo_Get(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	id, err := repo.Add(ctx, "trent", "T", "B", map[string]any{"request_id": "r1"}, "agent-1", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	row, err := repo.Get(ctx, "trent", id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.ID != id || row.Title != "T" || row.Payload == "" {
		t.Errorf("Get returned unexpected row (payload must be included): %+v", row)
	}

	if _, err := repo.Get(ctx, "trent", absentID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get(missing) = %v, want sql.ErrNoRows", err)
	}
	if _, err := repo.Get(ctx, "intruder", id); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get(wrong user) = %v, want sql.ErrNoRows", err)
	}
}

func TestNotificationRepo_MarkAllRead(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	for range 3 {
		if _, err := repo.Add(ctx, "trent", "n", "b", nil, "notify_user", ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if _, err := repo.Add(ctx, "other", "n", "b", nil, "notify_user", ""); err != nil {
		t.Fatalf("Add other: %v", err)
	}

	n, err := repo.MarkAllRead(ctx, "trent", InboxFilter{})
	if err != nil || n != 3 {
		t.Fatalf("MarkAllRead = %d (err=%v), want 3", n, err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{UnreadOnly: true}); c != 0 {
		t.Fatalf("trent should have 0 unread after mark-all, got %d", c)
	}
	if c, _ := repo.CountByUser(ctx, "other", InboxFilter{UnreadOnly: true}); c != 1 {
		t.Fatalf("other user must be unaffected, unread=%d", c)
	}
	if n2, _ := repo.MarkAllRead(ctx, "trent", InboxFilter{}); n2 != 0 {
		t.Fatalf("a second mark-all flips nothing, got %d", n2)
	}
}

func TestNotificationRepo_MarkUnread(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	id, err := repo.Add(ctx, "trent", "n", "b", nil, "notify_user", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.MarkRead(ctx, "trent", id); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{UnreadOnly: true}); c != 0 {
		t.Fatalf("want 0 unread after read, got %d", c)
	}

	if err := repo.MarkUnread(ctx, "trent", id); err != nil {
		t.Fatalf("MarkUnread: %v", err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{UnreadOnly: true}); c != 1 {
		t.Fatalf("want 1 unread after mark-unread, got %d", c)
	}
	if err := repo.MarkUnread(ctx, "trent", id); err != nil {
		t.Fatalf("re-mark-unread should be a no-op, got %v", err)
	}
	if err := repo.MarkUnread(ctx, "trent", "00000000-0000-0000-0000-000000000000"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mark-unread missing = %v, want sql.ErrNoRows", err)
	}
}

func TestNotificationRepo_Delete(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	id, err := repo.Add(ctx, "trent", "n", "b", nil, "notify_user", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := repo.Delete(ctx, "trent", id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{}); c != 0 {
		t.Fatalf("after delete, count = %d, want 0", c)
	}
	if err := repo.Delete(ctx, "trent", id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete missing = %v, want sql.ErrNoRows", err)
	}
	id2, _ := repo.Add(ctx, "trent", "n", "b", nil, "notify_user", "")
	if err := repo.Delete(ctx, "other", id2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-user delete = %v, want sql.ErrNoRows", err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{}); c != 1 {
		t.Fatalf("trent's notification must survive a cross-user delete, count=%d", c)
	}
}

func TestNotificationRepo_DeleteAllByUser(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	for range 3 {
		if _, err := repo.Add(ctx, "trent", "n", "b", nil, "notify_user", ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if _, err := repo.Add(ctx, "other", "n", "b", nil, "notify_user", ""); err != nil {
		t.Fatalf("Add other: %v", err)
	}

	n, err := repo.DeleteAllByUser(ctx, "trent")
	if err != nil || n != 3 {
		t.Fatalf("DeleteAllByUser = %d (err=%v), want 3", n, err)
	}
	if c, _ := repo.CountByUser(ctx, "trent", InboxFilter{}); c != 0 {
		t.Fatalf("trent inbox should be empty, got %d", c)
	}
	if c, _ := repo.CountByUser(ctx, "other", InboxFilter{}); c != 1 {
		t.Fatalf("other user must be unaffected, count=%d", c)
	}
	if n2, _ := repo.DeleteAllByUser(ctx, "trent"); n2 != 0 {
		t.Fatalf("clearing an empty inbox deletes nothing, got %d", n2)
	}
}

func TestNotificationRepo_Category(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	categorized, err := repo.Add(ctx, "trent", "T", "B", nil, "scheduler", "scheduled_task")
	if err != nil {
		t.Fatalf("Add categorized: %v", err)
	}
	uncategorized, err := repo.Add(ctx, "trent", "T", "B", nil, "notify_user", "")
	if err != nil {
		t.Fatalf("Add uncategorized: %v", err)
	}
	want := map[string]string{categorized: "scheduled_task", uncategorized: ""}

	for id, category := range want {
		n, getErr := repo.Get(ctx, "trent", id)
		if getErr != nil {
			t.Fatalf("Get %s: %v", id, getErr)
		}
		if n.Category != category {
			t.Errorf("Get %s: category = %q, want %q", id, n.Category, category)
		}
	}
	rows, err := repo.ListByUser(ctx, "trent", InboxFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("ListByUser returned %d rows, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		if r.Category != want[r.ID] {
			t.Errorf("listed %s: category = %q, want %q", r.ID, r.Category, want[r.ID])
		}
	}
}

func TestNotificationRepo_FilterByCategory(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	read, err := repo.Add(ctx, "alice", "T", "B", nil, "scheduler", "scheduled_task")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, category := range []string{"scheduled_task", "chat_share", ""} {
		if _, err = repo.Add(ctx, "alice", "T", "B", nil, "src", category); err != nil {
			t.Fatalf("Add %q: %v", category, err)
		}
	}
	if _, err = repo.Add(ctx, "bob", "T", "B", nil, "scheduler", "scheduled_task"); err != nil {
		t.Fatalf("Add bob: %v", err)
	}
	if err = repo.MarkRead(ctx, "alice", read); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	for _, tc := range []struct {
		name   string
		filter InboxFilter
		want   int
	}{
		{"one category", InboxFilter{Category: "scheduled_task"}, 2},
		{"one category, unread only", InboxFilter{Category: "scheduled_task", UnreadOnly: true}, 1},
		{"another category", InboxFilter{Category: "chat_share"}, 1},
		{"no filter", InboxFilter{}, 4},
	} {
		rows, listErr := repo.ListByUser(ctx, "alice", tc.filter, 10, 0)
		if listErr != nil {
			t.Fatalf("%s: ListByUser: %v", tc.name, listErr)
		}
		count, countErr := repo.CountByUser(ctx, "alice", tc.filter)
		if countErr != nil {
			t.Fatalf("%s: CountByUser: %v", tc.name, countErr)
		}
		if len(rows) != tc.want || count != tc.want {
			t.Errorf("%s: listed %d, counted %d, want %d", tc.name, len(rows), count, tc.want)
		}
		for _, r := range rows {
			if tc.filter.Category != "" && r.Category != tc.filter.Category {
				t.Errorf("%s: listed a %q notification", tc.name, r.Category)
			}
		}
	}

	scheduled := InboxFilter{Category: "scheduled_task"}
	first, err := repo.ListByUser(ctx, "alice", scheduled, 1, 0)
	if err != nil {
		t.Fatalf("ListByUser page 1: %v", err)
	}
	second, err := repo.ListByUser(ctx, "alice", scheduled, 1, 1)
	if err != nil {
		t.Fatalf("ListByUser page 2: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID == second[0].ID || second[0].Category != "scheduled_task" {
		t.Errorf("paging one category: page 1 = %+v, page 2 = %+v, want two different scheduled_task rows", first, second)
	}
}

func TestNotificationRepo_MarkAllReadByCategory(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))
	for _, category := range []string{"scheduled_task", "scheduled_task", "chat_share", ""} {
		if _, err := repo.Add(ctx, "alice", "T", "B", nil, "src", category); err != nil {
			t.Fatalf("Add %q: %v", category, err)
		}
	}
	if _, err := repo.Add(ctx, "bob", "T", "B", nil, "src", "scheduled_task"); err != nil {
		t.Fatalf("Add bob: %v", err)
	}

	n, err := repo.MarkAllRead(ctx, "alice", InboxFilter{Category: "scheduled_task"})
	if err != nil || n != 2 {
		t.Fatalf("MarkAllRead(scheduled_task) = %d (err=%v), want 2", n, err)
	}
	unread := func(user, category string) int {
		c, err := repo.CountByUser(ctx, user, InboxFilter{UnreadOnly: true, Category: category})
		if err != nil {
			t.Fatalf("CountByUser: %v", err)
		}
		return c
	}
	if got := unread("alice", "scheduled_task"); got != 0 {
		t.Errorf("alice scheduled_task unread = %d, want 0", got)
	}
	if got := unread("alice", ""); got != 2 {
		t.Errorf("alice unread in other categories = %d, want 2 (chat_share and uncategorized untouched)", got)
	}
	if got := unread("bob", "scheduled_task"); got != 1 {
		t.Errorf("bob unread = %d, want 1 (another user untouched)", got)
	}
}
