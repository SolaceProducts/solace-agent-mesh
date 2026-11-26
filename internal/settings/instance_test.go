// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import "testing"

func TestDesktopInstanceRoundTrip(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())

	if _, ok, err := ReadDesktopInstance(); err != nil || ok {
		t.Fatalf("ReadDesktopInstance on empty home: ok=%v err=%v; want ok=false err=nil", ok, err)
	}

	if err := WriteDesktopInstance("http://127.0.0.1:8800"); err != nil {
		t.Fatalf("WriteDesktopInstance: %v", err)
	}

	info, ok, err := ReadDesktopInstance()
	if err != nil || !ok {
		t.Fatalf("ReadDesktopInstance after write: ok=%v err=%v", ok, err)
	}
	if info.URL != "http://127.0.0.1:8800" {
		t.Errorf("URL = %q; want http://127.0.0.1:8800", info.URL)
	}

	if err := ClearDesktopInstance(); err != nil {
		t.Fatalf("ClearDesktopInstance: %v", err)
	}
	if _, ok, _ := ReadDesktopInstance(); ok {
		t.Error("ReadDesktopInstance after clear: ok=true; want false")
	}

	if err := ClearDesktopInstance(); err != nil {
		t.Errorf("ClearDesktopInstance on missing file: %v; want nil", err)
	}
}
