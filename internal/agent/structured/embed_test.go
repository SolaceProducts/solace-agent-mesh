// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"testing"
)

func TestParseResultEmbed_FullFormat(t *testing.T) {
	text := `Here is the result. «result:artifact=report.json:3 status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.ArtifactName != "report.json" {
		t.Errorf("artifact name = %q, want %q", re.ArtifactName, "report.json")
	}
	if re.Version == nil || *re.Version != 3 {
		t.Errorf("version = %v, want 3", re.Version)
	}
	if re.Status != "success" {
		t.Errorf("status = %q, want %q", re.Status, "success")
	}
}

func TestParseResultEmbed_NoVersion(t *testing.T) {
	text := `Done. «result:artifact=output.json status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.ArtifactName != "output.json" {
		t.Errorf("artifact name = %q, want %q", re.ArtifactName, "output.json")
	}
	if re.Version != nil {
		t.Errorf("version = %v, want nil", *re.Version)
	}
}

func TestParseResultEmbed_VPrefixVersion(t *testing.T) {
	text := `«result:artifact=data.csv:v5 status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.Version == nil || *re.Version != 5 {
		t.Errorf("version = %v, want 5", re.Version)
	}
}

func TestParseResultEmbed_ErrorWithMessage(t *testing.T) {
	text := `Failed. «result:status=error message="task failed"»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.Status != "error" {
		t.Errorf("status = %q, want %q", re.Status, "error")
	}
	if re.Message != "task failed" {
		t.Errorf("message = %q, want %q", re.Message, "task failed")
	}
	if re.ArtifactName != "" {
		t.Errorf("artifact name = %q, want empty", re.ArtifactName)
	}
}

func TestParseResultEmbed_ErrorWithArtifact(t *testing.T) {
	text := `«result:artifact=partial.json:1 status=error message="validation failed"»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.Status != "error" {
		t.Errorf("status = %q, want %q", re.Status, "error")
	}
	if re.ArtifactName != "partial.json" {
		t.Errorf("artifact name = %q, want %q", re.ArtifactName, "partial.json")
	}
}

func TestParseResultEmbed_MultipleEmbedsTakesLast(t *testing.T) {
	text := `First attempt. «result:artifact=first.json status=error»
Second attempt. «result:artifact=second.json:2 status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.ArtifactName != "second.json" {
		t.Errorf("artifact name = %q, want %q (last embed)", re.ArtifactName, "second.json")
	}
	if re.Version == nil || *re.Version != 2 {
		t.Errorf("version = %v, want 2", re.Version)
	}
	if re.Status != "success" {
		t.Errorf("status = %q, want %q", re.Status, "success")
	}
}

func TestParseResultEmbed_NoResultEmbed(t *testing.T) {
	text := `This text has no result embed. Just «artifact_content:file.csv» which is different.`
	re := ParseResultEmbed(text)
	if re != nil {
		t.Errorf("expected nil, got %+v", re)
	}
}

func TestParseResultEmbed_EmptyText(t *testing.T) {
	re := ParseResultEmbed("")
	if re != nil {
		t.Errorf("expected nil for empty text, got %+v", re)
	}
}

func TestParseResultEmbed_MalformedNoArtifactNoError(t *testing.T) {
	text := `«result:status=success»`
	re := ParseResultEmbed(text)
	if re != nil {
		t.Errorf("expected nil for malformed embed (no artifact, status=success), got %+v", re)
	}
}

func TestParseResultEmbed_MixedWithOtherEmbeds(t *testing.T) {
	text := `«math:2+2» Then the agent says: «status_update:working...» And finally: «result:artifact=output.json status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.ArtifactName != "output.json" {
		t.Errorf("artifact name = %q, want %q", re.ArtifactName, "output.json")
	}
}

func TestParseResultEmbed_StandaloneVersionParam(t *testing.T) {
	text := `«result:artifact=out.json version=7 status=success»`
	re := ParseResultEmbed(text)
	if re == nil {
		t.Fatal("expected result embed, got nil")
	}
	if re.Version == nil || *re.Version != 7 {
		t.Errorf("version = %v, want 7", re.Version)
	}
}

func TestStripResultEmbeds_RemovesResultEmbed(t *testing.T) {
	text := `Here is the output. «result:artifact=formatted_output.json status=success»`
	got := StripResultEmbeds(text)
	want := "Here is the output."
	if got != want {
		t.Errorf("StripResultEmbeds = %q, want %q", got, want)
	}
}

func TestStripResultEmbeds_PreservesOtherEmbeds(t *testing.T) {
	text := `Value is «math:2+2» and «artifact_content:file.csv» done.`
	got := StripResultEmbeds(text)
	if got != text {
		t.Errorf("StripResultEmbeds should preserve non-result embeds, got %q", got)
	}
}

func TestStripResultEmbeds_NoEmbeds(t *testing.T) {
	text := "Just plain text with no embeds."
	got := StripResultEmbeds(text)
	if got != text {
		t.Errorf("StripResultEmbeds = %q, want %q", got, text)
	}
}

func TestStripResultEmbeds_MultipleResultEmbeds(t *testing.T) {
	text := `First «result:artifact=a.json status=error» then «result:artifact=b.json status=success»`
	got := StripResultEmbeds(text)
	want := "First  then"
	if got != want {
		t.Errorf("StripResultEmbeds = %q, want %q", got, want)
	}
}

func TestStripResultEmbeds_MixedEmbeds(t *testing.T) {
	text := `Output: «math:42» «result:artifact=out.json status=success»`
	got := StripResultEmbeds(text)
	want := "Output: «math:42»"
	if got != want {
		t.Errorf("StripResultEmbeds = %q, want %q", got, want)
	}
}

func TestStripResultEmbeds_EmptyString(t *testing.T) {
	got := StripResultEmbeds("")
	if got != "" {
		t.Errorf("StripResultEmbeds(\"\") = %q, want empty", got)
	}
}
