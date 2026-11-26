// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"
)

func TestValidateArtifactHeader_Valid(t *testing.T) {
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "data.csv",
		FilenameSet: true,
		MIMEType:    "text/csv",
	}
	if err := validateArtifactHeader(block); err != nil {
		t.Errorf("expected no error for valid header, got: %v", err)
	}
}

func TestValidateArtifactHeader_MissingFilename(t *testing.T) {
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		FilenameSet: false,
	}
	err := validateArtifactHeader(block)
	if err == nil {
		t.Fatal("expected error for missing filename parameter")
	}
	if got := err.Error(); !strings.Contains(got, "filename") {
		t.Errorf("error should mention 'filename', got: %s", got)
	}
}

func TestValidateArtifactHeader_EmptyFilename(t *testing.T) {
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "",
		FilenameSet: true,
	}
	err := validateArtifactHeader(block)
	if err == nil {
		t.Fatal("expected error for empty filename")
	}
	if got := err.Error(); !strings.Contains(got, "empty") {
		t.Errorf("error should mention 'empty', got: %s", got)
	}
}

func TestValidateTemplateSyntax_Valid(t *testing.T) {
	template := `{% for item in items %}{{ item.name }}{% endfor %}`
	if err := validateTemplateSyntax(template); err != nil {
		t.Errorf("expected no error for valid template, got: %v", err)
	}
}

func TestValidateTemplateSyntax_ValidSimple(t *testing.T) {
	template := `Hello {{ name }}, welcome!`
	if err := validateTemplateSyntax(template); err != nil {
		t.Errorf("expected no error for simple template, got: %v", err)
	}
}

func TestValidateTemplateSyntax_InvalidUnclosedTag(t *testing.T) {
	template := `{% for item in items %}{{ item.name }}`
	err := validateTemplateSyntax(template)
	if err == nil {
		t.Fatal("expected error for unclosed for tag")
	}
}

func TestValidateTemplateSyntax_InvalidSyntax(t *testing.T) {
	template := `{% invalid_tag %}content{% endinvalid_tag %}`
	err := validateTemplateSyntax(template)
	if err == nil {
		t.Fatal("expected error for invalid tag")
	}
}

func TestValidateTemplateSyntax_EmptyTemplate(t *testing.T) {
	if err := validateTemplateSyntax(""); err != nil {
		t.Errorf("expected no error for empty template, got: %v", err)
	}
}

func TestBlockValidationError_ErrorMethod(t *testing.T) {
	bve := &blockValidationError{
		ErrorMsg: "test error message",
	}
	if bve.Error() != "test error message" {
		t.Errorf("Error() = %q, want %q", bve.Error(), "test error message")
	}
}
