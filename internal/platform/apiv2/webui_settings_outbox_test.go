// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"testing"
)

func TestWebuiSettings_PutRecordsOneOutboxNotice(t *testing.T) {
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)

	expectStatus(t, api.call(http.MethodPut, webuiSettingsPath, map[string]any{"appName": "Acme"}), http.StatusOK)

	var notices int
	if err := core.DB().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM outbox_events WHERE event_type LIKE 'webui_settings.%'`).Scan(&notices); err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if notices != 1 {
		t.Errorf("webui_settings outbox events = %d, want 1", notices)
	}
}
