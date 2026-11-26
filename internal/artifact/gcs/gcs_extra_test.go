// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gcs

import (
	"errors"
	"testing"
)

func TestIsNotFound_StringContains(t *testing.T) {
	err := errors.New("storage: object doesn't exist")
	if !isNotFound(err) {
		t.Error("expected isNotFound to return true for string-matching error")
	}
}

func TestIsNotFound_Nil(t *testing.T) {
	if isNotFound(nil) {
		t.Error("nil error should not be not-found")
	}
}

func TestIsNotFound_OtherError(t *testing.T) {
	err := errors.New("network timeout")
	if isNotFound(err) {
		t.Error("unrelated error should not be not-found")
	}
}

func TestResolveFilename_UserScoped(t *testing.T) {
	name, userScoped := resolveFilename("user:report.pdf")
	if name != "report.pdf" {
		t.Errorf("name = %q, want report.pdf", name)
	}
	if !userScoped {
		t.Error("expected userScoped = true")
	}
}

func TestResolveFilename_NotUserScoped(t *testing.T) {
	name, userScoped := resolveFilename("report.pdf")
	if name != "report.pdf" {
		t.Errorf("name = %q, want report.pdf", name)
	}
	if userScoped {
		t.Error("expected userScoped = false")
	}
}
