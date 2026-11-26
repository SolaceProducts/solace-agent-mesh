// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"strings"
	"testing"
)

type sampleDefaultedInt struct {
	Name  string `json:"name"`
	Count *int   `json:"count" cfgapply:"default=3"`
}

func TestRegister_RejectsDefaultOnNonStringField(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "sampleDefaultedInt.Count") {
			t.Fatalf("Register panic = %q, want it to name the misused field", msg)
		}
	}()
	Register(Kind{Name: "zzz_test_bad_default", ResponseProto: sampleDefaultedInt{}})
}

func TestRegister_AcceptsDefaultOnStringFields(t *testing.T) {
	k := Kind{
		Name:          "zzz_test_good_default",
		CreateProto:   sampleDefaultedCreate{},
		ResponseProto: &sampleDefaultedResponse{},
	}
	if err := k.validateTags(); err != nil {
		t.Fatalf("validateTags: %v", err)
	}
}
