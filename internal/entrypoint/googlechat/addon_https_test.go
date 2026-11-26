// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"testing"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	addOnSignerForTest   = "service-123456789012@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"
	addOnEndpointForTest = "https://sam.example.com/gw/gc/events"
)

func TestParseConfig_AddOnOverHTTPS(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"delivery": "https", "auth_audience_type": "endpoint_url", "endpoint_url": addOnEndpointForTest,
		"addon_service_account_email": addOnSignerForTest,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	verifier, err := newRequestVerifier(t.Context(), cfg, verifierEnv{})
	if err != nil {
		t.Fatalf("newRequestVerifier: %v", err)
	}
	v, ok := verifier.(*endpointURLVerifier)
	if !ok || v.signer != addOnSignerForTest || v.audience != addOnEndpointForTest {
		t.Errorf("verifier = %+v, want the add-on signer on the endpoint URL", v)
	}
}

func buttonFunctions(cards []*chat.CardWithId) []string {
	var fns []string
	for _, c := range cards {
		for _, sec := range c.Card.Sections {
			for _, w := range sec.Widgets {
				if w.ButtonList == nil {
					continue
				}
				for _, b := range w.ButtonList.Buttons {
					fns = append(fns, b.OnClick.Action.Function)
				}
			}
		}
	}
	return fns
}

func TestAddOnButtonsOverHTTPSPointAtTheEndpoint(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.cfg.Delivery, a.cfg.EndpointURL = DeliveryHTTPS, addOnEndpointForTest

	addOn := dmMeta()
	addOn.AddOn = true
	if !a.buttonsReachAdapter(addOn) {
		t.Fatal("an add-on over HTTPS must get buttons")
	}
	for _, fn := range buttonFunctions(a.taskControls(tid("task-1"), addOn)) {
		if fn != addOnEndpointForTest {
			t.Errorf("add-on button function = %q, want the endpoint URL", fn)
		}
	}
	fns := buttonFunctions(a.taskControls(tid("task-2"), dmMeta()))
	if len(fns) != 1 || fns[0] != actionCancelTask {
		t.Errorf("classic button functions = %v, want [%s]", fns, actionCancelTask)
	}
}
