// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"strconv"
	"strings"
	"testing"
)

func TestScopesDocNamesTheCapabilityScopeLimit(t *testing.T) {
	doc := ReadUserCapabilitiesRequest{}.APIDoc().Fields["Scopes"].Doc
	if !strings.Contains(doc, "more than "+strconv.Itoa(MaxCapabilityScopes)+" distinct scopes") {
		t.Errorf("the scopes doc does not state the limit of %d: %q", MaxCapabilityScopes, doc)
	}
}
