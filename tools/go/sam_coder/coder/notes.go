// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const (
	notesReminderSteps  = 8
	notesReviewMinSteps = 10
)

type notesKeeper struct {
	path, rel   string
	last        [32]byte
	lastChanged int
	reviewed    bool
}

func newNotesKeeper(root, rel string) *notesKeeper {
	if rel == "" {
		return nil
	}
	return &notesKeeper{path: filepath.Join(root, rel), rel: rel}
}

func (n *notesKeeper) reminder(step int) string {
	if n == nil {
		return ""
	}
	data, _ := os.ReadFile(n.path)
	if sum := sha256.Sum256(data); sum != n.last {
		n.last, n.lastChanged = sum, step
		return ""
	}
	if step-n.lastChanged < notesReminderSteps {
		return ""
	}
	n.lastChanged = step
	return fmt.Sprintf("\n\n%s has not changed in %d steps. Update it now with what you have found and what is still open.", n.rel, notesReminderSteps)
}

func (n *notesKeeper) afterElision() string {
	return fmt.Sprintf("Older tool output was trimmed to save context. Read %s to recover what you found and what is still open before you continue.", n.rel)
}

func (n *notesKeeper) review(stepsLeft int) string {
	if n == nil || n.reviewed || stepsLeft < notesReviewMinSteps {
		return ""
	}
	n.reviewed = true
	return fmt.Sprintf("Not yet accepted. Read %s and check that nothing you listed as open or next is left undone and that every part of the task is covered. Finish what is missing, then call submit_result again.", n.rel)
}
