// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newWebuiSettingsSvc(t *testing.T) (*WebuiSettingsService, *store.PlatformWebuiSettingsRepo) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewPlatformWebuiSettingsRepo(db)
	return NewWebuiSettingsService(repo, slog.Default()), repo
}

func override(s string) *string { return &s }
func overrideBool(b bool) *bool { return &b }

func TestWebuiSettingsService_GetUnset(t *testing.T) {
	t.Parallel()
	svc, _ := newWebuiSettingsSvc(t)
	resp, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.AppName != nil || resp.WelcomeMessage != nil || resp.CollectFeedback != nil ||
		resp.PublishFeedback != nil || resp.DisclaimerText != nil || resp.LogoURL != nil ||
		resp.SmallLogoURL != nil || resp.SystemPurpose != nil || resp.ResponseFormat != nil {
		t.Errorf("unset instance must return all-null overrides, got %+v", resp)
	}
	if resp.CreatedBy != "" || resp.CreatedAt != 0 {
		t.Errorf("unset instance must return zero audit fields, got createdBy=%q createdAt=%d", resp.CreatedBy, resp.CreatedAt)
	}
}

func TestWebuiSettingsService_RoundTripFalseAndEmpty(t *testing.T) {
	t.Parallel()
	svc, _ := newWebuiSettingsSvc(t)

	_, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{
		AppName:         override("Acme Assistant"),
		WelcomeMessage:  override(""),
		CollectFeedback: overrideBool(false),
		DisclaimerText:  override(""),
		LogoURL:         override("https://cdn.acme.com/logo.svg"),
	}, "admin@acme.com")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	resp, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.AppName == nil || *resp.AppName != "Acme Assistant" {
		t.Errorf("appName = %v, want Acme Assistant", resp.AppName)
	}
	if resp.WelcomeMessage == nil || *resp.WelcomeMessage != "" {
		t.Errorf("welcomeMessage = %v, want explicit empty-string override", resp.WelcomeMessage)
	}
	if resp.CollectFeedback == nil || *resp.CollectFeedback != false {
		t.Errorf("collectFeedback = %v, want explicit false override", resp.CollectFeedback)
	}
	if resp.DisclaimerText == nil || *resp.DisclaimerText != "" {
		t.Errorf("disclaimerText = %v, want explicit empty-string override", resp.DisclaimerText)
	}
	if resp.SmallLogoURL != nil || resp.SystemPurpose != nil || resp.ResponseFormat != nil {
		t.Errorf("unset fields must stay nil, got %+v", resp)
	}
	if resp.CreatedBy != "admin@acme.com" || resp.UpdatedBy != "admin@acme.com" {
		t.Errorf("audit fields = %q/%q", resp.CreatedBy, resp.UpdatedBy)
	}
	if resp.CreatedAt == 0 || resp.UpdatedAt == 0 {
		t.Error("audit timestamps must be set after a write")
	}
}

func TestWebuiSettingsService_NullVersusEmptyPerField(t *testing.T) {
	t.Parallel()

	fields := []struct {
		name string
		set  func(*UpdateWebuiSettingsRequest, *string)
		get  func(*WebuiSettingsResponse) *string
	}{
		{"appName", func(r *UpdateWebuiSettingsRequest, v *string) { r.AppName = v }, func(r *WebuiSettingsResponse) *string { return r.AppName }},
		{"welcomeMessage", func(r *UpdateWebuiSettingsRequest, v *string) { r.WelcomeMessage = v }, func(r *WebuiSettingsResponse) *string { return r.WelcomeMessage }},
		{"disclaimerText", func(r *UpdateWebuiSettingsRequest, v *string) { r.DisclaimerText = v }, func(r *WebuiSettingsResponse) *string { return r.DisclaimerText }},
		{"logoUrl", func(r *UpdateWebuiSettingsRequest, v *string) { r.LogoURL = v }, func(r *WebuiSettingsResponse) *string { return r.LogoURL }},
		{"smallLogoUrl", func(r *UpdateWebuiSettingsRequest, v *string) { r.SmallLogoURL = v }, func(r *WebuiSettingsResponse) *string { return r.SmallLogoURL }},
		{"systemPurpose", func(r *UpdateWebuiSettingsRequest, v *string) { r.SystemPurpose = v }, func(r *WebuiSettingsResponse) *string { return r.SystemPurpose }},
		{"responseFormat", func(r *UpdateWebuiSettingsRequest, v *string) { r.ResponseFormat = v }, func(r *WebuiSettingsResponse) *string { return r.ResponseFormat }},
	}

	for _, f := range fields {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			svc, _ := newWebuiSettingsSvc(t)

			req := UpdateWebuiSettingsRequest{AppName: override("keep")}
			f.set(&req, override(""))
			if _, err := svc.Set(context.Background(), req, "t"); err != nil {
				t.Fatalf("Set(%s=\"\"): %v", f.name, err)
			}
			resp, err := svc.Get(context.Background())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := f.get(resp); got == nil || *got != "" {
				t.Errorf("%s after empty-string override = %v, want \"\"", f.name, got)
			}

			req = UpdateWebuiSettingsRequest{AppName: override("keep")}
			f.set(&req, nil)
			if _, setErr := svc.Set(context.Background(), req, "t"); setErr != nil {
				t.Fatalf("Set(%s=nil): %v", f.name, setErr)
			}
			resp, err = svc.Get(context.Background())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := f.get(resp); got != nil {
				t.Errorf("%s after nil = %q, want nil (inherited)", f.name, *got)
			}
		})
	}
}

func TestWebuiSettingsService_AllNullDeletesRow(t *testing.T) {
	t.Parallel()
	svc, repo := newWebuiSettingsSvc(t)

	if _, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{AppName: override("Acme")}, "t"); err != nil {
		t.Fatalf("seed Set: %v", err)
	}
	if row, err := repo.Get(context.Background()); err != nil || row == nil {
		t.Fatalf("row after Set = (%v, %v), want present", row, err)
	}

	if _, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{}, "t"); err != nil {
		t.Fatalf("all-null Set: %v", err)
	}
	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if row != nil {
		t.Errorf("row after all-null PUT = %+v, want deleted", row)
	}
}

func TestWebuiSettingsService_PublishFeedbackOnlyIsPersisted(t *testing.T) {
	t.Parallel()
	svc, repo := newWebuiSettingsSvc(t)

	if _, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{
		PublishFeedback: overrideBool(true),
	}, "t"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get row: %v", err)
	}
	if row == nil {
		t.Fatal("row after publishFeedback-only PUT = nil, want persisted (not deleted)")
	}

	resp, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.PublishFeedback == nil || *resp.PublishFeedback != true {
		t.Errorf("publishFeedback = %v, want true", resp.PublishFeedback)
	}
}

func TestWebuiSettingsService_PutIsFullReplacement(t *testing.T) {
	t.Parallel()
	svc, _ := newWebuiSettingsSvc(t)

	if _, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{
		AppName:        override("Acme"),
		WelcomeMessage: override("Hi"),
	}, "t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{
		AppName: override("Acme"),
	}, "t"); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	resp, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.WelcomeMessage != nil {
		t.Errorf("welcomeMessage = %q, want nil after being omitted from the replacement PUT", *resp.WelcomeMessage)
	}
	if resp.AppName == nil || *resp.AppName != "Acme" {
		t.Errorf("appName = %v, want Acme", resp.AppName)
	}
}

func TestWebuiSettingsService_ValidationCollectsAllErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newWebuiSettingsSvc(t)

	_, err := svc.Set(context.Background(), UpdateWebuiSettingsRequest{
		AppName:        override("bad\nname"),
		LogoURL:        override("javascript:alert(1)"),
		SmallLogoURL:   override("ftp://cdn.acme.com/logo.png"),
		DisclaimerText: override(strings.Repeat("d", maxWebuiDisclaimerTextLen+1)),
		SystemPurpose:  override(strings.Repeat("p", maxWebuiPromptFieldLen+1)),
	}, "t")
	if err == nil {
		t.Fatal("expected ValidationError")
	}
	validErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	for _, field := range []string{"appName", "logoUrl", "smallLogoUrl", "disclaimerText", "systemPurpose"} {
		if len(validErr.Details[field]) == 0 {
			t.Errorf("expected a collected error for %s, details = %+v", field, validErr.Details)
		}
	}
}

func TestWebuiSettingsService_LogoURLSchemes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"https", "https://cdn.acme.com/logo.svg", true},
		{"http", "http://cdn.acme.com/logo.png", true},
		{"site relative", "/static/logo.svg", true},
		{"data svg", "data:image/svg+xml;base64,PHN2Zy8+", true},
		{"data png", "data:image/png;base64,iVBOR", true},
		{"empty override", "", true},
		{"javascript", "javascript:alert(1)", false},
		{"data non-image", "data:text/html;base64,PGh0bWw+", false},
		{"scheme-relative", "//evil.example.com/logo.svg", false},
		{"ftp", "ftp://cdn.acme.com/logo.png", false},
		{"case-tricked javascript", "JavaScript:alert(1)", false},
		{"oversized data uri", "data:image/png;base64," + strings.Repeat("A", maxWebuiLogoDataURILen), false},
	}

	for _, tc := range cases {
		for _, field := range []string{"logoUrl", "smallLogoUrl"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				t.Parallel()
				svc, _ := newWebuiSettingsSvc(t)
				req := UpdateWebuiSettingsRequest{}
				if field == "logoUrl" {
					req.LogoURL = override(tc.value)
				} else {
					req.SmallLogoURL = override(tc.value)
				}
				_, err := svc.Set(context.Background(), req, "t")
				if tc.valid && err != nil {
					t.Errorf("Set(%s=%q) = %v, want accepted", field, tc.value, err)
				}
				if !tc.valid && err == nil {
					t.Errorf("Set(%s=%q) accepted, want rejected", field, tc.value)
				}
			})
		}
	}
}

func newWebuiSettingsSvcWithOutbox(t *testing.T) (*WebuiSettingsService, *store.PlatformWebuiSettingsRepo, *authzEvents, *countingNotifier, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewPlatformWebuiSettingsRepo(db)
	outbox := store.NewOutboxRepo(db)
	notifier := &countingNotifier{}
	svc := NewWebuiSettingsService(repo, slog.Default())
	svc.WithOutbox(outbox, notifier)
	return svc, repo, &authzEvents{repo: outbox}, notifier, db
}

func TestWebuiSettingsService_RecordsAnOutboxEventPerCommittedWrite(t *testing.T) {
	t.Parallel()
	svc, _, events, notifier, _ := newWebuiSettingsSvcWithOutbox(t)

	if _, err := svc.Set(t.Context(), UpdateWebuiSettingsRequest{AppName: override("bad\nname")}, "t"); err == nil {
		t.Fatal("expected validation failure")
	}
	events.expectNone(t, "rejected write")

	if _, err := svc.Set(t.Context(), UpdateWebuiSettingsRequest{AppName: override("Acme")}, "t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	events.expect(t, "set", webuiSettingsOutboxChanged, webuiSettingsOutboxAggregateID)

	if _, err := svc.Set(t.Context(), UpdateWebuiSettingsRequest{}, "t"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	events.expect(t, "clear", webuiSettingsOutboxChanged, webuiSettingsOutboxAggregateID)

	if got := notifier.n.Load(); got != 2 {
		t.Errorf("poller woken %d times, want 2", got)
	}
}

func TestWebuiSettingsService_AFailedOutboxInsertRollsTheWriteBack(t *testing.T) {
	t.Parallel()
	svc, repo, _, notifier, db := newWebuiSettingsSvcWithOutbox(t)
	if _, err := db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
		t.Fatalf("drop outbox table: %v", err)
	}

	if _, err := svc.Set(t.Context(), UpdateWebuiSettingsRequest{AppName: override("Acme")}, "t"); err == nil {
		t.Fatal("Set succeeded without its outbox event")
	}
	row, err := repo.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row != nil {
		t.Errorf("settings row = %+v, want none: the write must roll back with its outbox event", row)
	}
	if got := notifier.n.Load(); got != 0 {
		t.Errorf("poller woken %d times, want 0", got)
	}
}

func TestWebuiSettingsService_AFailedSettingsWriteRecordsNoOutboxEvent(t *testing.T) {
	t.Parallel()
	svc, _, events, _, db := newWebuiSettingsSvcWithOutbox(t)
	if _, err := db.ExecContext(t.Context(), `DROP TABLE platform_webui_settings`); err != nil {
		t.Fatalf("drop settings table: %v", err)
	}

	if _, err := svc.Set(t.Context(), UpdateWebuiSettingsRequest{AppName: override("Acme")}, "t"); err == nil {
		t.Fatal("Set succeeded without its settings table")
	}
	events.expectNone(t, "failed settings write")
}
