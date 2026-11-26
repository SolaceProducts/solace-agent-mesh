// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	usageSourceAgent        = "agent"
	usageSourceTool         = "tool"
	usageSourceUnattributed = "unattributed"

	producedBySourceTool  = "tool"
	producedBySourceBlock = "block"

	maxStoredFailureDetailBytes = 4 << 10
)

type pendingRunFacts struct {
	mu    sync.Mutex
	facts session.RunFacts
}

func (p *pendingRunFacts) addUsage(model, source string, u llm.Usage) {
	tokens, err := json.Marshal(u)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts.Usage = append(p.facts.Usage, session.CallUsage{Model: model, Source: source, Tokens: tokens})
}

func (p *pendingRunFacts) addFile(f session.ProducedFile) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts.Files = append(p.facts.Files, f)
}

func (p *pendingRunFacts) setFailure(d *tool.ToolFailedDetail) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d == nil {
		p.facts.Failure = &session.ToolFailure{}
		return
	}
	p.facts.Failure = &session.ToolFailure{
		Code:       d.Code,
		Message:    truncateUTF8(d.Message, maxStoredFailureDetailBytes),
		StderrTail: truncateUTF8(d.StderrTail, maxStoredFailureDetailBytes),
		StackTrace: truncateUTF8(d.StackTrace, maxStoredFailureDetailBytes),
	}
}

func (p *pendingRunFacts) markFinal(visible, stored string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts.Final = true
	if visible != stored {
		p.facts.Answer = visible
	}
}

func (p *pendingRunFacts) markQuiet() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts.Quiet = true
}

func (p *pendingRunFacts) take() *session.RunFacts {
	p.mu.Lock()
	defer p.mu.Unlock()
	if isEmptyRunFacts(&p.facts) {
		return nil
	}
	taken := p.facts
	p.facts = session.RunFacts{}
	return &taken
}

func (p *pendingRunFacts) snapshot() *session.RunFacts {
	p.mu.Lock()
	defer p.mu.Unlock()
	if isEmptyRunFacts(&p.facts) {
		return nil
	}
	c := p.facts
	c.Usage = append([]session.CallUsage(nil), p.facts.Usage...)
	c.Files = append([]session.ProducedFile(nil), p.facts.Files...)
	return &c
}

func (p *pendingRunFacts) restore(f *session.RunFacts) {
	if f == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts = *f
}

func (p *pendingRunFacts) giveBack(f *session.RunFacts) {
	if f == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts.Usage = append(append([]session.CallUsage(nil), f.Usage...), p.facts.Usage...)
	p.facts.Files = append(append([]session.ProducedFile(nil), f.Files...), p.facts.Files...)
	if p.facts.Failure == nil {
		p.facts.Failure = f.Failure
	}
	p.facts.Final = p.facts.Final || f.Final
	p.facts.Quiet = p.facts.Quiet || f.Quiet
	if p.facts.Answer == "" {
		p.facts.Answer = f.Answer
	}
}

func isEmptyRunFacts(f *session.RunFacts) bool {
	return len(f.Usage) == 0 && len(f.Files) == 0 && f.Failure == nil && !f.Final && f.Answer == "" && !f.Quiet
}

func producedFileOf(rec artifactCreationRecord, source string) session.ProducedFile {
	return session.ProducedFile{
		Filename: rec.Filename,
		Version:  rec.Version,
		Source:   source,
		Status:   rec.Status,
		Error:    rec.ErrorMsg,
		Tags:     rec.Tags,
	}
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func attachPendingRunFacts(batch []session.Message, pending *pendingRunFacts) *session.RunFacts {
	if len(batch) == 0 {
		return nil
	}
	facts := pending.take()
	if facts != nil {
		batch[len(batch)-1].RunFacts = facts
	}
	return facts
}
