// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func sPtr(s string) *string { return &s }
func bPtr(b bool) *bool     { return &b }

func TestPlatformWebuiSettingsRepo_GetAbsentReturnsNilNil(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformWebuiSettingsRepo(db)

	row, err := repo.Get(t.Context())
	if err != nil {
		t.Fatalf("Get on absent row: %v", err)
	}
	if row != nil {
		t.Errorf("row = %+v, want nil on an unconfigured instance", row)
	}
}

func TestPlatformWebuiSettingsRepo_RoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformWebuiSettingsRepo(db)
	ctx := t.Context()

	in := &PlatformWebuiSettings{
		AppName:         sPtr("Acme Assistant"),
		WelcomeMessage:  sPtr(""),
		CollectFeedback: bPtr(false),
		PublishFeedback: bPtr(true),
		DisclaimerText:  sPtr("May be inaccurate."),
		LogoURL:         sPtr("https://cdn.acme.com/logo.svg"),
		SystemPurpose:   sPtr("You are a support assistant"),
	}
	if err := repo.Set(ctx, in, "admin@acme.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil {
		t.Fatal("row absent after Set")
	}
	if row.AppName == nil || *row.AppName != "Acme Assistant" {
		t.Errorf("app_name = %v", row.AppName)
	}
	if row.WelcomeMessage == nil || *row.WelcomeMessage != "" {
		t.Errorf("welcome_message = %v, want explicit empty string", row.WelcomeMessage)
	}
	if row.CollectFeedback == nil || *row.CollectFeedback != false {
		t.Errorf("collect_feedback = %v, want explicit false", row.CollectFeedback)
	}
	if row.PublishFeedback == nil || *row.PublishFeedback != true {
		t.Errorf("publish_feedback = %v, want true", row.PublishFeedback)
	}
	if row.SmallLogoURL != nil || row.ResponseFormat != nil {
		t.Errorf("unset columns must scan as nil, got small=%v respFmt=%v", row.SmallLogoURL, row.ResponseFormat)
	}
	if row.CreatedBy != "admin@acme.com" || row.CreatedTime == 0 {
		t.Errorf("audit columns = %q/%d", row.CreatedBy, row.CreatedTime)
	}

	createdTime := row.CreatedTime
	in2 := &PlatformWebuiSettings{
		CollectFeedback: bPtr(true),
		SmallLogoURL:    sPtr("/static/logo-sm.svg"),
	}
	if setErr := repo.Set(ctx, in2, "other@acme.com"); setErr != nil {
		t.Fatalf("second Set: %v", setErr)
	}
	row, err = repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.AppName != nil {
		t.Errorf("app_name = %v, want NULL after replacement", row.AppName)
	}
	if row.CollectFeedback == nil || *row.CollectFeedback != true {
		t.Errorf("collect_feedback = %v, want true", row.CollectFeedback)
	}
	if row.PublishFeedback != nil {
		t.Errorf("publish_feedback = %v, want NULL after replacement", row.PublishFeedback)
	}
	if row.SmallLogoURL == nil || *row.SmallLogoURL != "/static/logo-sm.svg" {
		t.Errorf("small_logo_url = %v", row.SmallLogoURL)
	}
	if row.CreatedBy != "admin@acme.com" || row.CreatedTime != createdTime {
		t.Errorf("created_by/created_time changed on update: %q/%d", row.CreatedBy, row.CreatedTime)
	}
	if row.UpdatedBy != "other@acme.com" {
		t.Errorf("updated_by = %q", row.UpdatedBy)
	}
}

func TestPlatformWebuiSettingsRepo_DeleteDropsRow(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformWebuiSettingsRepo(db)
	ctx := t.Context()

	if err := repo.Delete(ctx); err != nil {
		t.Fatalf("Delete on absent row must be a no-op, got %v", err)
	}

	if err := repo.Set(ctx, &PlatformWebuiSettings{AppName: sPtr("Acme")}, "t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := repo.Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row != nil {
		t.Errorf("row survived Delete: %+v", row)
	}
}
