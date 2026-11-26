// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"reflect"
	"slices"
	"testing"
	"unsafe"
)

var unsignedIdentityFields = map[string]string{
	"authenticated":    "every signed task is authenticated by construction; the signature is the claim",
	"metadata":         "provider-specific extras with no agent-side reader",
	"profile":          "enrichment output travels in the unsigned user config, not the token",
	"enrichmentStatus": "enrichment state is gateway-local",
	"enrichmentReason": "enrichment state is gateway-local",
	"systemInitiated":  "gateway-local marker for background-minted identities",
	"scopesResolved":   "gateway-local flag; the signed scopes are already the resolution",
	"senderVerified":   "gateway-local gate on stamping platformUserID",
	"fullIdentity":     "gateway-local trust state; it decides whether SubmitTask signs at all",
}

var identityClaimFor = map[string]string{
	"userID":         "Subject",
	"email":          "Email",
	"name":           "Name",
	"authMethod":     "AuthMethod",
	"credentialID":   "CredentialID",
	"roles":          "Roles",
	"scopes":         "Scopes",
	"platformUserID": "PlatformUserID",
}

func TestTaskSignClaimsCopiesEveryIdentityFieldNotJustDeclaresIt(t *testing.T) {
	live := reflect.TypeOf(Subject{})
	srcVal := populatedValue(t, live)
	src := srcVal.Addr().Interface().(*Subject)
	claims := reflect.ValueOf(src.TaskSignClaims(""))

	for i := range live.NumField() {
		name := live.Field(i).Name
		if _, unsigned := unsignedIdentityFields[name]; unsigned {
			continue
		}
		want, ok := identityClaimFor[name]
		claim := claims.FieldByName(want)
		if !ok || !claim.IsValid() {
			t.Errorf("Subject.%s is neither carried by TaskSignClaims (mapped in identityClaimFor) nor listed in unsignedIdentityFields", name)
			continue
		}
		if sv := settable(srcVal.Field(i)).Interface(); !reflect.DeepEqual(sv, claim.Interface()) {
			t.Errorf("SignClaims.%s is declared but TaskSignClaims does not copy Subject.%s: got %v, want %v",
				want, name, claim.Interface(), sv)
		}
	}
}

func settable(f reflect.Value) reflect.Value {
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func populatedValue(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	v := reflect.New(typ).Elem()
	for i := range typ.NumField() {
		f := settable(v.Field(i))
		name := typ.Field(i).Name
		switch f.Kind() {
		case reflect.String:
			f.SetString("x-" + name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			elem := reflect.New(f.Type().Elem()).Elem()
			if elem.Kind() == reflect.String {
				elem.SetString("x-" + name)
			}
			f.Set(reflect.Append(reflect.MakeSlice(f.Type(), 0, 1), elem))
		case reflect.Map:
			m := reflect.MakeMap(f.Type())
			m.SetMapIndex(reflect.ValueOf("x-"+name).Convert(f.Type().Key()), reflect.Zero(f.Type().Elem()))
			f.Set(m)
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		default:
			t.Fatalf("populatedValue: no rule for %s field %s; add one", f.Kind(), name)
		}
	}
	return v
}

func TestTaskSignClaimsCopiesTheIdentity(t *testing.T) {
	id := NewTestSubject(SubjectFields{
		UserID:       "system:ci-deployer",
		Email:        "ops@example.com",
		Name:         "CI deployer",
		AuthMethod:   APIKeyAuthMethod,
		CredentialID: "key-1",
		Roles:        []string{"deployer"},
		Scopes:       []string{"agent:*:invoke"},
	})
	got := id.TaskSignClaims("task-1")

	if got.Subject != id.UserID() || got.TaskID != "task-1" || got.Email != id.Email() || got.Name != id.Name() {
		t.Errorf("identity fields not carried: %+v", got)
	}
	if got.AuthMethod != APIKeyAuthMethod || got.CredentialID != "key-1" {
		t.Errorf("AuthMethod, CredentialID = %q, %q, want %q, key-1", got.AuthMethod, got.CredentialID, APIKeyAuthMethod)
	}
	if !slices.Equal(got.Roles, id.Roles()) || !slices.Equal(got.Scopes, id.Scopes()) {
		t.Errorf("Roles, Scopes = %v, %v, want %v, %v", got.Roles, got.Scopes, id.Roles(), id.Scopes())
	}
}
