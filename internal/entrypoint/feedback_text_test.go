// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "testing"

func TestFeedbackCopyMatchesWebUI(t *testing.T) {
	const webUINotice = "This conversation may be viewed to help improve responses"

	if FeedbackDataUseNotice != webUINotice {
		t.Errorf("FeedbackDataUseNotice = %q, want %q", FeedbackDataUseNotice, webUINotice)
	}
	if FeedbackAckNotice == "" {
		t.Error("FeedbackAckNotice is empty")
	}
}
