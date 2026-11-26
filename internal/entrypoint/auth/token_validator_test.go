// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"testing"
)

func TestTokenValidatorRegistry_RegisterAndCreate(t *testing.T) {
	name := "test-validator-" + t.Name()
	fakeIdentity := &Subject{userID: "u1", authenticated: true}
	RegisterValidatorFactory(name, func(map[string]any) (TokenValidator, error) {
		return TokenValidatorFunc(func(_ context.Context, tok string) (*Subject, error) {
			if tok == "good" {
				return fakeIdentity, nil
			}
			return nil, errors.New("bad")
		}), nil
	})
	t.Cleanup(func() { unregisterValidatorFactory(name) })

	v, err := CreateValidator(name, nil)
	if err != nil {
		t.Fatalf("CreateValidator: %v", err)
	}
	id, err := v.Validate(context.Background(), "good")
	if err != nil || id != fakeIdentity {
		t.Fatalf("Validate(good) = %v, %v; want fakeIdentity, nil", id, err)
	}
	if _, err := v.Validate(context.Background(), "bad"); err == nil {
		t.Fatal("Validate(bad) returned nil error")
	}
}

func TestTokenValidatorRegistry_UnknownFactory(t *testing.T) {
	if _, err := CreateValidator("nonexistent-"+t.Name(), nil); err == nil {
		t.Fatal("CreateValidator returned nil error for unknown factory")
	}
}
