// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform_test

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestGoogleChatSchema_DriveAccessDaysMatchTheAdapter(t *testing.T) {
	def, ok := platform.LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat is not a registered gateway type")
	}
	for _, f := range def.Fields {
		if f.Name != "artifact_drive_access_days" {
			continue
		}
		if f.MinValue == nil || *f.MinValue != 1 {
			t.Errorf("MinValue = %v, want 1", ptrValue(f.MinValue))
		}
		if f.MaxValue == nil || *f.MaxValue != googlechat.MaxArtifactDriveAccessDays {
			t.Errorf("MaxValue = %v, want the adapter's maximum %d", ptrValue(f.MaxValue), googlechat.MaxArtifactDriveAccessDays)
		}
		if f.Default != googlechat.DefaultArtifactDriveAccessDays {
			t.Errorf("Default = %v, want the adapter's default %d", f.Default, googlechat.DefaultArtifactDriveAccessDays)
		}
		return
	}
	t.Error("artifact_drive_access_days is not in the Google Chat schema")
}

func ptrValue(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestGoogleChatSchema_PubSubDeliveryIsHiddenFromTheForm(t *testing.T) {
	def, ok := platform.LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat is not a registered gateway type")
	}
	for _, name := range []string{"delivery", "pubsub_subscription"} {
		found := false
		for _, f := range def.Fields {
			if f.Name != name {
				continue
			}
			found = true
			if f.UIVisible {
				t.Errorf("%s has UIVisible = true, want false", name)
			}
		}
		if !found {
			t.Errorf("%s is not in the Google Chat schema", name)
		}
	}
}

func TestGatewaySchemas_VisibleFieldsDoNotDependOnHiddenFields(t *testing.T) {
	for _, def := range platform.AllGatewayTypeDefs() {
		visible := map[string]bool{}
		for _, f := range def.Fields {
			visible[f.Name] = f.UIVisible
		}
		for _, f := range def.Fields {
			if !f.UIVisible {
				continue
			}
			for key := range f.ConditionalDisplay {
				if known, ok := visible[key]; ok && !known {
					t.Errorf("%s: visible field %s is conditioned on hidden field %s", def.Type, f.Name, key)
				}
			}
		}
	}
}
