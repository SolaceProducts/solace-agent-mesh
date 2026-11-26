// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
)

func TestDetectBinaryMIMEKeepsSpecificTypeOverZipContainer(t *testing.T) {
	const zipSig = "PK\x03\x04\x14\x00\x00\x00\x08\x00"
	for _, tc := range []struct{ name, stored, want string }{
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"epub", "application/epub+zip", "application/epub+zip"},
		{"plain zip", "application/zip", "application/zip"},
		{"plain zip, no stored type", "", "application/zip"},
		{"plain zip, generic stored type", "application/octet-stream", "application/zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectBinaryMIME([]byte(zipSig), tc.stored); got != tc.want {
				t.Errorf("detectBinaryMIME(zip sig, %q) = %q, want %q", tc.stored, got, tc.want)
			}
		})
	}

	pdf := "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"
	if got := detectBinaryMIME([]byte(pdf), "application/force-download"); got != "application/pdf" {
		t.Errorf("detectBinaryMIME(pdf sig, lying stored) = %q, want application/pdf", got)
	}
}

func TestDetectBinaryMIMEFlagsUTF16AsUnsearchable(t *testing.T) {
	utf16LE := append([]byte{0xFF, 0xFE}, []byte("E\x00R\x00R\x00O\x00R\x00")...)
	got := detectBinaryMIME(utf16LE, "text/plain")
	if got == "" {
		t.Error("UTF-16 accepted as greppable text; a search for ERROR would return " +
			"\"No matches found\" on a file that contains it")
	}
	if !strings.Contains(got, "utf-16") {
		t.Errorf("detectBinaryMIME(utf-16) = %q, want a type naming the encoding so the "+
			"model knows what to convert", got)
	}

	if got := detectBinaryMIME([]byte("hello world\n"), "text/plain"); got != "" {
		t.Errorf("ASCII text flagged as binary: %q", got)
	}
	if got := detectBinaryMIME([]byte("caf\xe9 ERROR na\xefve\n"), "text/plain"); got != "" {
		t.Errorf("latin-1 text flagged as binary: %q", got)
	}
	if got := detectBinaryMIME([]byte("{\n  \"a\": 1\n}\n"), "application/json"); got != "" {
		t.Errorf("JSON flagged as binary: %q", got)
	}
}
