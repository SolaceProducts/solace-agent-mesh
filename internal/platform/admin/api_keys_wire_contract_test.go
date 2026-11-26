// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestAPIKeyCreateResponseIsPinnedToTheV2Contract(t *testing.T) {
	expires := int64(1798675200000)
	key := &store.APIKey{
		ID:          "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		UserID:      "9c858901-8a57-4791-81fe-4c455b099bc9",
		Label:       "deploy-pipeline",
		KeyHash:     "must-not-appear",
		Prefix:      "a1b2c3d4",
		ExpiresTime: &expires,
		CreatedBy:   "admin@example.com",
		UpdatedBy:   "admin@example.com",
		CreatedTime: 1759152191123,
		UpdatedTime: 1759152191123,
	}
	resp := apiKeyToResponse(key, []string{"agent_builder:_:create"}, 1759152191123)
	resp.Secret = "samk_PLAINTEXT"

	encoded, err := json.Marshal(DataResponse[*APIKeyResponse]{Data: resp})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"data":{` +
		`"id":"3f2504e0-4f89-11d3-9a0c-0305e82c3301",` +
		`"type":"apiKey",` +
		`"userId":"9c858901-8a57-4791-81fe-4c455b099bc9",` +
		`"label":"deploy-pipeline",` +
		`"prefix":"a1b2c3d4",` +
		`"scopes":["agent_builder:_:create"],` +
		`"status":"active",` +
		`"expiresTime":"2026-12-31T00:00:00.000Z",` +
		`"lastUsedTime":null,` +
		`"createdBy":"admin@example.com",` +
		`"createdTime":"2025-09-29T13:23:11.123Z",` +
		`"updatedBy":"admin@example.com",` +
		`"updatedTime":"2025-09-29T13:23:11.123Z",` +
		`"secret":"samk_PLAINTEXT"}}`

	if got := string(encoded); got != want {
		t.Errorf("create response JSON changed.\n got: %s\nwant: %s", got, want)
	}
}

func TestAPIKeyReadResponseIsPinnedToTheV2Contract(t *testing.T) {
	lastUsed := int64(1759238591456)
	key := &store.APIKey{
		ID:           "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		UserID:       "9c858901-8a57-4791-81fe-4c455b099bc9",
		Label:        "renamed",
		Prefix:       "a1b2c3d4",
		LastUsedTime: &lastUsed,
		CreatedBy:    "admin@example.com",
		UpdatedBy:    "other@example.com",
		CreatedTime:  1759152191123,
		UpdatedTime:  1759152251789,
	}
	encoded, err := json.Marshal(DataResponse[*APIKeyResponse]{Data: apiKeyToResponse(key, []string{"agent:*:invoke"}, 1759238591456)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"data":{` +
		`"id":"3f2504e0-4f89-11d3-9a0c-0305e82c3301",` +
		`"type":"apiKey",` +
		`"userId":"9c858901-8a57-4791-81fe-4c455b099bc9",` +
		`"label":"renamed",` +
		`"prefix":"a1b2c3d4",` +
		`"scopes":["agent:*:invoke"],` +
		`"status":"active",` +
		`"expiresTime":null,` +
		`"lastUsedTime":"2025-09-30T13:23:11.456Z",` +
		`"createdBy":"admin@example.com",` +
		`"createdTime":"2025-09-29T13:23:11.123Z",` +
		`"updatedBy":"other@example.com",` +
		`"updatedTime":"2025-09-29T13:24:11.789Z"}}`

	if got := string(encoded); got != want {
		t.Errorf("read response JSON changed.\n got: %s\nwant: %s", got, want)
	}
}

func TestAPIKeyResponseNeverSerializesTheStoredHash(t *testing.T) {
	key := &store.APIKey{
		ID: "id", UserID: "owner", Label: "l", Prefix: "p",
		KeyHash: "0badc0ffee", CreatedTime: 1, UpdatedTime: 1,
	}
	encoded, err := json.Marshal(apiKeyToResponse(key, nil, 2))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "0badc0ffee") {
		t.Errorf("response carries the stored key hash: %s", encoded)
	}
}

func TestAPIKeySecretIsAbsentUnlessSet(t *testing.T) {
	key := &store.APIKey{ID: "id", UserID: "owner", Label: "l", CreatedTime: 1, UpdatedTime: 1}
	encoded, err := json.Marshal(apiKeyToResponse(key, nil, 2))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Errorf("non-create response carries a secret field: %s", encoded)
	}
}

func TestAPIKeyScopesMarshalAsAnEmptyArrayNotNull(t *testing.T) {
	key := &store.APIKey{ID: "id", UserID: "owner", CreatedTime: 1, UpdatedTime: 1}
	encoded, err := json.Marshal(apiKeyToResponse(key, nil, 2))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"scopes":[]`) {
		t.Errorf("expected an empty scopes array, got: %s", encoded)
	}
}

func TestAPIKeyListResponseUsesThePaginatedEnvelope(t *testing.T) {
	page := PaginatedResponse[APIKeyResponse]{
		Data: []APIKeyResponse{},
		Meta: NewPaginationMeta(1, 20, 0),
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"data":[],"meta":{"pagination":{"pageNumber":1,"count":0,"pageSize":20,"nextPage":null,"totalPages":0}}}`
	if got := string(encoded); got != want {
		t.Errorf("list envelope changed.\n got: %s\nwant: %s", got, want)
	}
}

func TestRESTTimestampEmitsMillisecondsAndZ(t *testing.T) {
	encoded, err := json.Marshal(restTimestamp(1759152191123))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `"2025-09-29T13:23:11.123Z"`; got != want {
		t.Errorf("timestamp format: got %s want %s", got, want)
	}
}

func TestNullableRESTTimestampDistinguishesUnsetFromZero(t *testing.T) {
	if got := nullableRESTTimestamp(nil); got != nil {
		t.Errorf("nil epoch-ms should stay nil, got %v", *got)
	}
	zero := int64(0)
	encoded, err := json.Marshal(nullableRESTTimestamp(&zero))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `"1970-01-01T00:00:00.000Z"`; got != want {
		t.Errorf("epoch zero should render as an instant, got %s want %s", got, want)
	}
}

func TestRESTTimestampNormalizesAnOffsetToUTC(t *testing.T) {
	var ts RESTTimestamp
	if err := json.Unmarshal([]byte(`"2026-12-31T01:00:00.000+01:00"`), &ts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	encoded, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `"2026-12-31T00:00:00.000Z"`; got != want {
		t.Errorf("offset not normalized: got %s want %s", got, want)
	}
}

func TestRESTTimestampRejectsNonISOInput(t *testing.T) {
	for _, raw := range []string{`1759152191123`, `"yesterday"`, `"2026-12-31"`, `true`} {
		var ts RESTTimestamp
		if err := json.Unmarshal([]byte(raw), &ts); err == nil {
			t.Errorf("accepted %s as a timestamp", raw)
		}
	}
}

func TestRESTTimestampMarshalsInUTCRegardlessOfProcessZone(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("IST", 5*3600+1800)
	t.Cleanup(func() { time.Local = original })

	ts := RESTTimestamp(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC).UnixMilli())
	encoded, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `"2026-09-29T12:00:00.000Z"`; string(encoded) != want {
		t.Errorf("marshal = %s, want %s (the local zone leaked into a Z-suffixed instant)", encoded, want)
	}
}

func TestRESTTimestampKeepsMilliseconds(t *testing.T) {
	var ts RESTTimestamp
	if err := json.Unmarshal([]byte(`"2026-12-31T00:00:00.123Z"`), &ts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	encoded, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `"2026-12-31T00:00:00.123Z"`; got != want {
		t.Errorf("round trip = %s, want %s", got, want)
	}
}
