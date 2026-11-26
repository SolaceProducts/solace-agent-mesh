// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func claimSource(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return string(b)
}

func projectedPairs(rows []store.UserIdentityClaim) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		verified := "unverified"
		if r.Verified {
			verified = "verified"
		}
		out = append(out, r.ClaimKey+"="+r.ClaimValueCanonical+" ("+verified+")")
	}
	slices.Sort(out)
	return out
}

func TestProjectIdentityClaims(t *testing.T) {
	t.Run("a verified phone projects in canonical form", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          "+1 (555) 123-4567",
			"phone_number_verified": true,
		}))
		want := []string{"phone_number=15551234567 (verified)"}
		if got := projectedPairs(rows); !slices.Equal(got, want) {
			t.Errorf("projection = %v, want %v", got, want)
		}
	})

	t.Run("an unverified phone is recorded but not verified", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          "+15551234567",
			"phone_number_verified": false,
		}))
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		if rows[0].Verified {
			t.Error("an unverified phone projected as verified — the phone_number_verified requirement is bypassed")
		}
	})

	t.Run("a denial spelled in the provider's own case still counts", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{
			"Phone_Number":          "+15551234567",
			"Phone_Number_verified": false,
		}))
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		if rows[0].Verified {
			t.Error("Phone_Number_verified: false was discarded — the companion lookup does not fold the way the policy selection does")
		}
		if rows[0].ClaimKey != "Phone_Number" {
			t.Errorf("ClaimKey = %q, want the provider's spelling kept, since the lookup queries by it", rows[0].ClaimKey)
		}
	})

	t.Run("an absent phone_number_verified is verified", func(t *testing.T) {
		rows, notes := projectIdentityClaims(claimSource(t, map[string]any{"phone_number": "+15551234567"}))
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		if !rows[0].Verified {
			t.Error("an absent phone_number_verified was refused; a provider that never sends one has every user turned away")
		}
		if len(notes) != 0 {
			t.Errorf("notes = %v, want none — nothing was refused, so there is nothing to warn about", notes)
		}
	})

	t.Run("an absent email_verified is verified", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{"email": "Person@Example.com"}))
		want := []string{"email=person@example.com (verified)"}
		if got := projectedPairs(rows); !slices.Equal(got, want) {
			t.Errorf("projection = %v, want %v", got, want)
		}
	})

	t.Run("an explicitly unverified email is not verified", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{
			"email":          "person@example.com",
			"email_verified": false,
		}))
		if len(rows) != 1 || rows[0].Verified {
			t.Errorf("projection = %v, want a single unverified row", projectedPairs(rows))
		}
	})

	t.Run("a claim with no built-in meaning is projected as text", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{"mobile": "+15551234567"}))
		if want := []string{"mobile=+15551234567 (verified)"}; !slices.Equal(projectedPairs(rows), want) {
			t.Errorf("projection = %v, want %v", projectedPairs(rows), want)
		}
	})

	t.Run("a custom claim projects case-folded", func(t *testing.T) {
		const key = "extension_deadbeef_waUsername"
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{key: "AliceSmith"}))
		want := []string{key + "=alicesmith (verified)"}
		if got := projectedPairs(rows); !slices.Equal(got, want) {
			t.Errorf("projection = %v, want %v", got, want)
		}
	})

	t.Run("a multi-valued claim projects every value", func(t *testing.T) {
		rows, _ := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          []any{"+15551234567", "+15559998888"},
			"phone_number_verified": true,
		}))
		want := []string{"phone_number=15551234567 (verified)", "phone_number=15559998888 (verified)"}
		if got := projectedPairs(rows); !slices.Equal(got, want) {
			t.Errorf("projection = %v, want %v", got, want)
		}
	})

	t.Run("an unparseable claim source projects nothing rather than failing", func(t *testing.T) {
		if rows, _ := projectIdentityClaims("{not json"); len(rows) != 0 {
			t.Errorf("projection = %v, want nothing", projectedPairs(rows))
		}
	})
}

func TestProjectionChangedSeesAClaimNoMappingReads(t *testing.T) {
	const key = "extension_deadbeef_waUsername"
	before := claimSource(t, map[string]any{"sub": "abc", key: "alice"})
	after := claimSource(t, map[string]any{"sub": "abc", key: "bob"})

	if !projectionChanged(before, after) {
		t.Error("a changed custom claim was not detected; the authz lookup cache would keep serving the old resolution")
	}
	if projectionChanged(before, before) {
		t.Error("an unchanged claim set reported as changed; every login would flush the whole pod's cache")
	}
	if !projectionChanged(claimSource(t, map[string]any{"mobile": "111"}), claimSource(t, map[string]any{"mobile": "222"})) {
		t.Error("a changed claim with no built-in meaning did not move the projection")
	}
}

func TestProjectionChangedIgnoresClaimOrder(t *testing.T) {
	a := `{"email":"p@example.com","phone_number":"+15551234567","phone_number_verified":true}`
	b := `{"phone_number_verified":true,"phone_number":"+15551234567","email":"p@example.com"}`
	if projectionChanged(a, b) {
		t.Error("claim order changed the fingerprint")
	}
}

func TestClaimVerified(t *testing.T) {
	t.Parallel()

	phone, ok := policyForClaimKey(ClaimKeyPhoneNumber)
	if !ok {
		t.Fatal("phone_number is not admissible; it is a built-in key")
	}

	cases := []struct {
		name    string
		raw     any
		present bool
		want    verificationVerdict
	}{
		{"absent", nil, false, verificationVerified},
		{"null is the provider declining to answer, not a no", nil, true, verificationVerified},
		{"bool true", true, true, verificationVerified},
		{"canonical string", "true", true, verificationVerified},
		{"canonical string, mixed case", "TRUE", true, verificationVerified},
		{"padded string", "  true  ", true, verificationVerified},
		{"bool false", false, true, verificationDenied},
		{"string false", "false", true, verificationDenied},
		{"string false, mixed case", "False", true, verificationDenied},
		{"zero", "0", true, verificationDenied},
		{"no", "no", true, verificationDenied},
		{"off", "off", true, verificationDenied},
		{"one is not true, the same as emailVerified, so the predicates agree", "1", true, verificationUnreadable},
		{"an unrecognized word", "maybe", true, verificationUnreadable},
		{"a number", float64(1), true, verificationUnreadable},
		{"an object", map[string]any{"value": true}, true, verificationUnreadable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{ClaimKeyPhoneNumber: "+15551234567"}
			if tc.present {
				claims["phone_number_verified"] = tc.raw
			}
			if got := claimVerified(claims, phone); got != tc.want {
				t.Errorf("claimVerified = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnverifiedClaimNotes(t *testing.T) {
	t.Parallel()

	t.Run("a denial is reported as one", func(t *testing.T) {
		rows, notes := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          "+15551234567",
			"phone_number_verified": false,
		}))
		if len(rows) != 1 || rows[0].Verified {
			t.Fatalf("rows = %v, want one unverified row", projectedPairs(rows))
		}
		if len(notes) != 1 {
			t.Fatalf("notes = %v, want one", notes)
		}
		if notes[0].Reason != "provider_says_unverified" {
			t.Errorf("reason = %q, want provider_says_unverified", notes[0].Reason)
		}
		if notes[0].ClaimKey != ClaimKeyPhoneNumber || notes[0].VerificationClaim != "phone_number_verified" {
			t.Errorf("note names %q/%q, want the claim and its companion", notes[0].ClaimKey, notes[0].VerificationClaim)
		}
		if notes[0].Values != 1 {
			t.Errorf("valueCount = %d, want 1", notes[0].Values)
		}
	})

	t.Run("an unreadable companion claim is distinguished", func(t *testing.T) {
		_, notes := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          "+15551234567",
			"phone_number_verified": "maybe",
		}))
		if len(notes) != 1 || notes[0].Reason != "verification_claim_unreadable" {
			t.Fatalf("notes = %v, want one unreadable note", notes)
		}
	})

	t.Run("a claim the provider never sends produces no note", func(t *testing.T) {
		_, notes := projectIdentityClaims(claimSource(t, map[string]any{
			"email": "person@example.com",
		}))
		if len(notes) != 0 {
			t.Errorf("notes = %v, want none", notes)
		}
	})

	t.Run("a multi-valued claim warns once and counts", func(t *testing.T) {
		_, notes := projectIdentityClaims(claimSource(t, map[string]any{
			"phone_number":          []any{"+15551234567", "+15559998888"},
			"phone_number_verified": false,
		}))
		if len(notes) != 1 {
			t.Fatalf("notes = %v, want one", notes)
		}
		if notes[0].Values != 2 {
			t.Errorf("valueCount = %d, want 2", notes[0].Values)
		}
	})
}

func TestLogUnverifiedClaimsSaysWhatToDo(t *testing.T) {
	t.Parallel()

	logger, buf := captureLogger()
	h := NewUserUpsertHandler(nil, nil, UserUpsertConfig{Namespace: "ns"}, logger)
	h.logUnverifiedClaims(&store.User{ID: "user-uuid-1"}, []unverifiedClaimNote{{
		ClaimKey:          ClaimKeyPhoneNumber,
		VerificationClaim: "phone_number_verified",
		Reason:            "provider_says_unverified",
		Values:            1,
	}})

	out := buf.String()
	for _, want := range []string{
		"level=WARN",
		"no_match",
		"userId=user-uuid-1",
		"identityClaimKey=phone_number",
		"verificationClaim=phone_number_verified",
		"reason=provider_says_unverified",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log does not carry %q; got %s", want, out)
		}
	}
}

func TestVolatileClaimsDoNotMoveTheProjection(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{
			"keycloak-shaped, fresh session and token ids",
			`{"email":"a@example.com","email_verified":true,"jti":"j-1","sid":"s-1","session_state":"ss-1","at_hash":"h-1"}`,
			`{"email":"a@example.com","email_verified":true,"jti":"j-2","sid":"s-2","session_state":"ss-2","at_hash":"h-2"}`,
		},
		{
			"entra-shaped, fresh request ids",
			`{"email":"a@example.com","aio":"a-1","rh":"r-1","uti":"u-1","nonce":"n-1"}`,
			`{"email":"a@example.com","aio":"a-2","rh":"r-2","uti":"u-2","nonce":"n-2"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if projectionChanged(tc.before, tc.after) {
				t.Error("an ordinary re-login moved the projection, so the pod-wide lookup cache is flushed on every sign-in")
			}
		})
	}

	if !projectionChanged(
		`{"phone_number":"+15551234567","jti":"j-1"}`,
		`{"phone_number":"+15559999999","jti":"j-1"}`,
	) {
		t.Error("a changed phone claim did not move the projection")
	}
}

func TestUnreadableClaimValuesAreRecorded(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		wantRows     int
	}{
		{"a number, as a Keycloak long-typed mapper emits", `{"phone_number":15551234567}`, 0},
		{"a bool", `{"phone_number":true}`, 0},
		{"an object", `{"phone_number":{"value":"+15551234567"}}`, 0},
		{"the non-string members of a mixed array", `{"phone_number":[15551234567,"+15559999999"]}`, 1},
		{"a value the canonical form empties out", `{"phone_number":"not a phone"}`, 0},
		{"a value too long for the index that holds it", `{"phone_number":"` + strings.Repeat("1", 600) + `"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, notes := projectIdentityClaims(tc.source)
			if len(rows) != tc.wantRows {
				t.Errorf("rows = %d, want %d", len(rows), tc.wantRows)
			}
			found := false
			for _, n := range notes {
				if n.Reason == reasonValueUnreadable {
					found = true
				}
			}
			if !found {
				t.Errorf("notes = %+v, want one naming an unreadable value — without it the operator cannot tell this from a claim the provider never sent", notes)
			}
		})
	}

	_, notes := projectIdentityClaims(`{"email":"a@example.com","email_verified":true}`)
	if len(notes) != 0 {
		t.Errorf("an ordinary verified claim produced notes: %+v", notes)
	}

	rows, notes := projectIdentityClaims(`{"email":"a@example.com","exp":1758403600,"iat":1758400000,"nbf":1758400000,"auth_time":1758399990,"updated_at":1700000000,"xms_tcdt":1500000000}`)
	if len(notes) != 0 {
		t.Errorf("the token's time claims produced notes: %+v", notes)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want only the email; a timestamp is not an identity", len(rows))
	}
}

func TestUnreadableClaimValuesLogAtInfo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := NewUserUpsertHandler(nil, nil, UserUpsertConfig{Namespace: "ns"}, logger)
	h.logUnverifiedClaims(&store.User{ID: "user-uuid-1"}, []unverifiedClaimNote{
		{ClaimKey: ClaimKeyPhoneNumber, Reason: reasonValueUnreadable, Values: 1},
		{ClaimKey: ClaimKeyPhoneNumber, VerificationClaim: "phone_number_verified", Reason: "provider_says_unverified", Values: 1},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "level=INFO") || !strings.Contains(lines[0], "reason=claim_value_unreadable") {
		t.Errorf("unreadable note = %q, want INFO", lines[0])
	}
	if !strings.Contains(lines[1], "level=WARN") || !strings.Contains(lines[1], "reason=provider_says_unverified") {
		t.Errorf("unverified note = %q, want WARN", lines[1])
	}
}

func TestDivergentProviderSpellingsBothProject(t *testing.T) {
	rows, _ := projectIdentityClaims(`{"phone_number":"+52 1 555 123 4567","phone_number_verified":true}`)
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		if !r.Verified {
			t.Errorf("row %q came out unverified", r.ClaimValueCanonical)
		}
		got = append(got, r.ClaimValueCanonical)
	}
	slices.Sort(got)
	want := []string{"525551234567", "5215551234567"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("projected %v, want both Mexican spellings %v", got, want)
	}
}

func TestImplausiblePhoneValuesAreNoted(t *testing.T) {
	rows, notes := projectIdentityClaims(`{"phone_number":"555-12"}`)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the value still stored so the directory shows it", len(rows))
	}
	found := false
	for _, n := range notes {
		if n.Reason == reasonValueImplausible {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %+v, want one naming an implausible value", notes)
	}
}
