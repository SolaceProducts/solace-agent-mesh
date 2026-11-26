// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	EmbedOpen  = directive.Open
	EmbedClose = directive.Close
)

type TokenType int

const (
	TextToken TokenType = iota

	EmbedToken
)

type Token struct {
	Type  TokenType
	Text  string
	Embed *EmbedRef
}

type EmbedRef struct {
	Type    string
	Params  map[string]string
	Raw     string
	IsEarly bool
}

const maxEmbedBufferSize = 512

var earlyEmbedTypes = map[string]bool{
	"math":          true,
	"datetime":      true,
	"uuid":          true,
	"artifact_meta": true,
	"status_update": true,
}

func isBuiltinEmbedType(embedType string) bool {
	return earlyEmbedTypes[embedType] || embedType == "artifact_content" || embedType == "artifact_return" || embedType == "result"
}

func afterStrayRune(s string) (string, bool) {
	if rest := strings.TrimLeft(s, string(utf8.RuneError)); rest != s {
		return rest, true
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '§' {
		return "", false
	}
	return s[size:], true
}

func builtinTypeAfterStrayRune(embedType string) (string, bool) {
	if lower := strings.ToLower(embedType); lower != embedType && isBuiltinEmbedType(lower) {
		return lower, true
	}
	recovered, ok := afterStrayRune(embedType)
	return recovered, ok && isBuiltinEmbedType(recovered)
}

const (
	statusUpdateTypePrefix = "status_update:"
	maxJunkWordRunes       = 12
)

func statusUpdateAfterJunkWord(raw string) (string, bool) {
	inner, ok := strings.CutPrefix(raw, EmbedOpen)
	if !ok {
		return "", false
	}
	idx := indexFoldASCII(inner, statusUpdateTypePrefix)
	if idx < 0 {
		return "", false
	}
	junk := inner[:idx]
	value := inner[idx+len(statusUpdateTypePrefix):]
	if junk == "" && inner[:len(statusUpdateTypePrefix)] == statusUpdateTypePrefix {
		return "", false
	}
	if utf8.RuneCountInString(junk) > maxJunkWordRunes || strings.ContainsRune(junk, '\n') {
		return "", false
	}
	return EmbedOpen + statusUpdateTypePrefix + value, true
}

const maxMangledStatusRunes = 24

func mangledStatusUpdate(raw string) (string, bool) {
	inner, ok := strings.CutPrefix(raw, EmbedOpen)
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, EmbedClose)
	if !ok {
		return "", false
	}
	typeName, value, _ := strings.Cut(inner, ":")
	lower := strings.ToLower(typeName)
	if lower == "status" || lower == "status_update" || utf8.RuneCountInString(typeName) > maxMangledStatusRunes || strings.ContainsFunc(typeName, unicode.IsSpace) {
		return "", false
	}
	if !strings.HasSuffix(lower, "status") && !strings.HasSuffix(lower, "tus_update") {
		return "", false
	}
	return value, true
}

const maxProgressQuoteRunes = 200

const tripleOpen = EmbedOpen + EmbedOpen + EmbedOpen

func typelessQuote(raw string) (string, bool) {
	inner, ok := strings.CutPrefix(raw, EmbedOpen)
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, EmbedClose)
	if !ok || strings.TrimSpace(inner) == "" || strings.ContainsAny(inner, "\n"+EmbedOpen) || utf8.RuneCountInString(inner) > maxProgressQuoteRunes {
		return "", false
	}
	if _, typed := embedTypeToken(inner); typed {
		return "", false
	}
	return strings.TrimSpace(inner), true
}

func unclosedLine(buf string, closeIdx int) (string, string, bool) {
	nl := strings.IndexByte(buf, '\n')
	if nl < 0 || (closeIdx >= 0 && len(EmbedOpen)+closeIdx < nl) {
		return "", "", false
	}
	return buf[len(EmbedOpen):nl], strings.TrimLeft(buf[nl:], strayWordGap), true
}

func progressFollows(rest string) bool {
	_, statusNext := statusUpdateFollows(rest)
	return strings.HasPrefix(rest, tripleOpen) || statusNext
}

func progressMayFollow(rest string) bool {
	return strings.HasPrefix(tripleOpen, rest) || statusUpdateMayFollow(rest)
}

func endsAtBoundary(text string) bool {
	text = strings.TrimRight(text, " \t")
	if text == "" {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text)
	return strings.ContainsRune(".!?\n", r)
}

const statusUpdateOpener = EmbedOpen + "status_update"

func isStrayWordEmbed(raw string) bool {
	word, ok := strings.CutPrefix(raw, EmbedOpen)
	if !ok {
		return false
	}
	word, ok = strings.CutSuffix(word, EmbedClose)
	if !ok || word == "" || utf8.RuneCountInString(word) > maxJunkWordRunes || isBuiltinEmbedType(strings.ToLower(word)) {
		return false
	}
	for _, r := range word {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != utf8.RuneError {
			return false
		}
	}
	return true
}

const strayWordGap = " \t\r\n"

func statusUpdateFollows(s string) (string, bool) {
	s = strings.TrimLeft(s, strayWordGap)
	return s, len(s) >= len(statusUpdateOpener) && strings.EqualFold(s[:len(statusUpdateOpener)], statusUpdateOpener)
}

func statusUpdateMayFollow(s string) bool {
	s = strings.TrimLeft(s, strayWordGap)
	return len(s) < len(statusUpdateOpener) && strings.EqualFold(s, statusUpdateOpener[:len(s)])
}

func indexFoldASCII(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func builtinTypeTokenAfterStrayRune(s string) (string, bool) {
	rest, ok := afterStrayRune(s)
	if !ok {
		return "", false
	}
	name, ok := embedTypeToken(rest)
	return name, ok && isBuiltinEmbedType(name)
}

func blockRunTypeToken(s string) (string, bool) {
	if name, ok := embedTypeToken(s); ok {
		return name, true
	}
	return builtinTypeTokenAfterStrayRune(s)
}

func parseStreamedEmbed(raw string) *EmbedRef {
	if recovered, ok := statusUpdateAfterJunkWord(raw); ok {
		raw = recovered
	} else if value, ok := mangledStatusUpdate(raw); ok && value != "" {
		raw = EmbedOpen + statusUpdateTypePrefix + value + EmbedClose
	}
	ref := ParseEmbed(raw)
	if ref == nil {
		return nil
	}
	recovered, isGarbled := builtinTypeAfterStrayRune(ref.Type)
	if !isGarbled {
		return ref
	}
	ref.Type = recovered
	ref.IsEarly = earlyEmbedTypes[recovered]
	ref.Raw = EmbedOpen + recovered + EmbedClose
	if value, hasValue := ref.Params["value"]; hasValue {
		ref.Raw = EmbedOpen + recovered + ":" + value + EmbedClose
	}
	return ref
}

func leadingRun(s, delim string) int {
	n := 0
	for strings.HasPrefix(s, delim) {
		s = s[len(delim):]
		n++
	}
	return n
}

func embedTypeToken(s string) (string, bool) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", false
	}
	name := s[:colon]
	for _, r := range name {
		if r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return "", false
		}
	}
	return name, true
}

func isBlockType(name string) bool {
	switch name {
	case "save_artifact", "template_liquid", "template":
		return true
	}
	return false
}

func embedTokenizer(ctx context.Context, in <-chan accumulatedChunk) <-chan tokenizedChunk {
	out := make(chan tokenizedChunk, 8)
	t := &tokenizer{ctx: ctx, out: out, atBoundary: true}
	go t.run(in)
	return out
}

type tokenizer struct {
	ctx             context.Context
	out             chan<- tokenizedChunk
	buf             string
	pendingCloseRun bool
	atBoundary      bool
}

type bufferStep int

const (
	stepContinue bufferStep = iota
	stepWait
)

func (t *tokenizer) run(in <-chan accumulatedChunk) {
	defer close(t.out)
	for {
		select {
		case <-t.ctx.Done():
			return
		case chunk, ok := <-in:
			if !ok {
				t.flush()
				return
			}
			if !t.handle(chunk) {
				return
			}
		}
	}
}

func (t *tokenizer) handle(chunk accumulatedChunk) bool {
	if chunk.toolCall != nil || chunk.reasoningDelta != "" || chunk.err != nil || chunk.finishReason != "" || chunk.usage != nil {
		return t.passThrough(chunk)
	}
	if chunk.text == "" {
		return true
	}
	t.buf += chunk.text
	t.processBuffer()
	return true
}

func (t *tokenizer) passThrough(chunk accumulatedChunk) bool {
	if chunk.toolCall != nil {
		t.progressQuoteBeforeToolCall()
	}
	t.flush()
	select {
	case t.out <- tokenizedChunk{
		toolCall:       chunk.toolCall,
		reasoningDelta: chunk.reasoningDelta,
		err:            chunk.err,
		finishReason:   chunk.finishReason,
		usage:          chunk.usage,
	}:
	case <-t.ctx.Done():
		return false
	}
	return chunk.err == nil
}

func (t *tokenizer) swallowCloseRun() {
	if !t.pendingCloseRun {
		return
	}
	if n := leadingRun(t.buf, EmbedClose); n > 0 {
		t.buf = t.buf[n*len(EmbedClose):]
	}
	if t.buf != "" {
		t.pendingCloseRun = false
	}
}

func (t *tokenizer) flush() {
	t.swallowCloseRun()
	if t.buf == "" {
		return
	}
	select {
	case t.out <- tokenizedChunk{token: &Token{Type: TextToken, Text: t.buf}}:
	case <-t.ctx.Done():
		return
	}
	t.atBoundary = endsAtBoundary(t.buf)
	t.buf = ""
}

func (t *tokenizer) emitToken(tok *Token) {
	switch tok.Type {
	case TextToken:
		t.atBoundary = endsAtBoundary(tok.Text)
	default:
		t.atBoundary = tok.Embed.Type == "status_update"
	}
	select {
	case t.out <- tokenizedChunk{token: tok}:
	case <-t.ctx.Done():
	}
}

func (t *tokenizer) emitText(n int) {
	t.emitToken(&Token{Type: TextToken, Text: t.buf[:n]})
	t.buf = t.buf[n:]
}

func (t *tokenizer) flushIfOversized() {
	if len(t.buf) > maxEmbedBufferSize {
		t.emitText(len(t.buf))
	}
}

func (t *tokenizer) emitProgressQuote(quote string) {
	t.atBoundary = true
	if !strings.ContainsFunc(quote, unicode.IsSpace) {
		return
	}
	if ref := parseStreamedEmbed(EmbedOpen + statusUpdateTypePrefix + quote + EmbedClose); ref != nil {
		t.emitToken(&Token{Type: EmbedToken, Embed: ref})
	}
}

func (t *tokenizer) progressQuoteBeforeToolCall() {
	end := strings.Index(t.buf, EmbedClose)
	if !t.atBoundary || !strings.HasPrefix(t.buf, EmbedOpen) || end < 0 {
		return
	}
	raw := t.buf[:end+len(EmbedClose)]
	if strings.TrimLeft(t.buf[len(raw):], strayWordGap) != "" {
		return
	}
	if quote, ok := typelessQuote(raw); ok {
		t.emitProgressQuote(quote)
		t.buf = ""
	}
}

func (t *tokenizer) processBuffer() {
	for {
		if t.step() == stepWait {
			return
		}
	}
}

func (t *tokenizer) step() bufferStep {
	if t.pendingCloseRun {
		t.swallowCloseRun()
		if t.buf == "" {
			return stepWait
		}
	}

	startIdx := strings.Index(t.buf, EmbedOpen)
	if startIdx == -1 {
		if t.buf != "" {
			t.emitText(len(t.buf))
		}
		return stepWait
	}

	if startIdx > 0 {
		t.emitText(startIdx)
	}

	if run := leadingRun(t.buf, EmbedOpen); run >= 2 {
		return t.openRun(run)
	}

	endIdx := strings.Index(t.buf[len(EmbedOpen):], EmbedClose)
	if line, rest, ok := unclosedLine(t.buf, endIdx); ok && t.atBoundary {
		if next, handled := t.unclosedProgressLine(line, rest); handled {
			return next
		}
	}
	if endIdx == -1 {
		t.flushIfOversized()
		return stepWait
	}

	return t.completeEmbed(len(EmbedOpen) + endIdx + len(EmbedClose))
}

func (t *tokenizer) openRun(run int) bufferStep {
	openBytes := run * len(EmbedOpen)
	rest := t.buf[openBytes:]
	typeName, ok := blockRunTypeToken(rest)
	switch {
	case !ok:
		t.flushIfOversized()
		return stepWait
	case isBlockType(typeName):
		t.emitText(openBytes)
		return stepContinue
	default:
		return t.wrappedInlineEmbed(openBytes, rest)
	}
}

func (t *tokenizer) wrappedInlineEmbed(openBytes int, rest string) bufferStep {
	closeIdx := strings.Index(rest, EmbedClose)
	if closeIdx == -1 {
		t.flushIfOversized()
		return stepWait
	}
	inner := rest[:closeIdx]
	closeRun := leadingRun(rest[closeIdx:], EmbedClose)
	afterRun := closeIdx + closeRun*len(EmbedClose)
	consumed := openBytes + afterRun
	ref := parseStreamedEmbed(EmbedOpen + inner + EmbedClose)
	if ref == nil {
		t.emitText(consumed)
		return stepContinue
	}
	t.emitToken(&Token{Type: EmbedToken, Embed: ref})
	if afterRun >= len(rest) {
		t.pendingCloseRun = true
	}
	t.buf = t.buf[consumed:]
	return stepContinue
}

func (t *tokenizer) unclosedProgressLine(line, rest string) (bufferStep, bool) {
	if progressFollows(rest) {
		if quote, isQuote := typelessQuote(EmbedOpen + line + EmbedClose); isQuote {
			t.emitProgressQuote(quote)
			t.buf = rest
			return stepContinue, true
		}
	}
	if progressMayFollow(rest) && len(t.buf) <= maxEmbedBufferSize {
		return stepWait, true
	}
	return stepContinue, false
}

func (t *tokenizer) completeEmbed(embedEnd int) bufferStep {
	embedRaw := t.buf[:embedEnd]
	if value, ok := mangledStatusUpdate(embedRaw); ok && value == "" {
		t.buf = t.buf[embedEnd:]
		return stepContinue
	}
	if next, handled := t.strayWord(embedRaw, embedEnd); handled {
		return next
	}
	if next, handled := t.typelessProgress(embedRaw, embedEnd); handled {
		return next
	}
	t.buf = t.buf[embedEnd:]

	ref := parseStreamedEmbed(embedRaw)
	if ref != nil {
		t.emitToken(&Token{Type: EmbedToken, Embed: ref})
	} else {
		t.emitToken(&Token{Type: TextToken, Text: embedRaw})
	}
	return stepContinue
}

func (t *tokenizer) strayWord(embedRaw string, embedEnd int) (bufferStep, bool) {
	if !isStrayWordEmbed(embedRaw) {
		return stepContinue, false
	}
	after := t.buf[embedEnd:]
	if fromStatus, ok := statusUpdateFollows(after); ok {
		t.buf = fromStatus
		return stepContinue, true
	}
	if statusUpdateMayFollow(after) && len(t.buf) <= maxEmbedBufferSize {
		return stepWait, true
	}
	return stepContinue, false
}

func (t *tokenizer) typelessProgress(embedRaw string, embedEnd int) (bufferStep, bool) {
	quote, ok := typelessQuote(embedRaw)
	if !ok || !t.atBoundary {
		return stepContinue, false
	}
	rest := strings.TrimLeft(t.buf[embedEnd:], strayWordGap)
	if progressFollows(rest) {
		t.emitProgressQuote(quote)
		t.buf = rest
		return stepContinue, true
	}
	if progressMayFollow(rest) && len(t.buf) <= maxEmbedBufferSize {
		return stepWait, true
	}
	return stepContinue, false
}

type tokenizedChunk struct {
	token          *Token
	toolCall       *llm.ToolCall
	reasoningDelta string
	err            error
	finishReason   string
	usage          *llm.Usage
}

func ParseEmbed(raw string) *EmbedRef {
	embedType, value, hasValue, ok := directive.ParseInlineEmbed(raw)
	if !ok {
		return nil
	}
	params := make(map[string]string)
	if hasValue {
		params["value"] = value
	}
	return &EmbedRef{
		Type:    embedType,
		Params:  params,
		Raw:     raw,
		IsEarly: earlyEmbedTypes[embedType],
	}
}
