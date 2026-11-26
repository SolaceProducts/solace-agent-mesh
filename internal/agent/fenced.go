// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	BlockDelimiterOpen  = "\u00AB\u00AB\u00AB"
	BlockDelimiterClose = "\u00BB\u00BB\u00BB"
)

const (
	BlockPrefixSaveArtifact   = BlockDelimiterOpen + "save_artifact:"
	BlockPrefixTemplateLiquid = BlockDelimiterOpen + "template_liquid:"
	BlockPrefixTemplate       = BlockDelimiterOpen + "template:"
)

type FencedBlockType int

const (
	FencedBlockNone FencedBlockType = iota
	FencedBlockArtifact
	FencedBlockTemplate
)

type FencedBlockEvent struct {
	Type FencedBlockType

	Filename    string
	FilenameSet bool
	MIMEType    string
	Description string
	Tags        []string

	Status string

	Message string

	DataSource string
	JSONPath   string
	Limit      int

	Content string

	Incomplete bool
}

type fencedBlockOutputType int

const (
	fencedOutputText fencedBlockOutputType = iota
	fencedOutputBlockStart
	fencedOutputBlockContent
	fencedOutputBlockEnd
	fencedOutputToolCall
	fencedOutputReasoning
	fencedOutputError
)

type fencedOutput struct {
	outputType     fencedBlockOutputType
	text           string
	reasoningDelta string
	block          *FencedBlockEvent
	toolCall       *llm.ToolCall
	token          *Token
	err            error
	finishReason   string
	usage          *llm.Usage
}

type fencedBlockState int

const (
	fencedStateIdle fencedBlockState = iota
	fencedStateInArtifact
	fencedStateInTemplate
)

var paramPattern = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

func fencedBlockParser(ctx context.Context, in <-chan tokenizedChunk) <-chan fencedOutput {
	out := make(chan fencedOutput, 8)
	p := &fencedParser{ctx: ctx, out: out}
	go func() {
		defer close(out)
		p.run(in)
	}()
	return out
}

type fencedParser struct {
	ctx context.Context
	out chan<- fencedOutput

	state                             fencedBlockState
	nestingDepth                      int
	lineBuf                           string
	pendingFence                      string
	inCodeFence, closingFenceExpected bool
	blockContent                      strings.Builder
	currentBlock                      *FencedBlockEvent
}

func (p *fencedParser) run(in <-chan tokenizedChunk) {
	for {
		select {
		case <-p.ctx.Done():
			return
		case chunk, ok := <-in:
			if !ok {
				p.finish()
				return
			}
			p.handleChunk(chunk)
		}
	}
}

func (p *fencedParser) emit(o fencedOutput) {
	select {
	case p.out <- o:
	case <-p.ctx.Done():
	}
}

func (p *fencedParser) appendContent(text string) {
	p.blockContent.WriteString(text)
	p.emit(fencedOutput{
		outputType: fencedOutputBlockContent,
		text:       text,
		block:      p.currentBlock,
	})
}

func (p *fencedParser) closeBlock() {
	p.nestingDepth = 0
	p.currentBlock.Content = p.blockContent.String()
	p.emit(fencedOutput{outputType: fencedOutputBlockEnd, block: p.currentBlock})
	p.state = fencedStateIdle
	p.currentBlock = nil
}

func (p *fencedParser) flushPendingFence() {
	if p.pendingFence != "" {
		p.emit(fencedOutput{outputType: fencedOutputText, text: p.pendingFence + "\n"})
		p.pendingFence = ""
	}
}

func (p *fencedParser) handleChunk(chunk tokenizedChunk) {
	switch {
	case chunk.err != nil:
		p.emit(fencedOutput{outputType: fencedOutputError, err: chunk.err})
	case chunk.toolCall != nil:
		p.emit(fencedOutput{outputType: fencedOutputToolCall, toolCall: chunk.toolCall})
	case chunk.reasoningDelta != "":
		p.emit(fencedOutput{outputType: fencedOutputReasoning, reasoningDelta: chunk.reasoningDelta})
	case chunk.finishReason != "" || chunk.usage != nil:
		p.emit(fencedOutput{finishReason: chunk.finishReason, usage: chunk.usage})
	case chunk.token == nil:
	case chunk.token.Type == EmbedToken:
		p.handleEmbed(chunk.token)
	default:
		p.handleText(chunk.token.Text)
	}
}

func (p *fencedParser) handleEmbed(token *Token) {
	switch {
	case p.state != fencedStateIdle && p.currentBlock != nil:
		if p.lineBuf != "" {
			p.appendContent(p.lineBuf)
			p.lineBuf = ""
		}
		p.appendContent(token.Embed.Raw)
	case strings.HasPrefix(strings.TrimLeft(p.lineBuf, " \t"), BlockDelimiterOpen):
		p.lineBuf += token.Embed.Raw
	default:
		p.flushPendingFence()
		p.closingFenceExpected = false
		if p.lineBuf != "" {
			p.emit(fencedOutput{outputType: fencedOutputText, text: p.lineBuf})
			p.lineBuf = ""
		}
		p.emit(fencedOutput{outputType: fencedOutputText, token: token})
	}
}

func (p *fencedParser) handleText(text string) {
	p.lineBuf += text
	for {
		nlIdx := strings.Index(p.lineBuf, "\n")
		if nlIdx == -1 {
			return
		}
		line := p.lineBuf[:nlIdx]
		p.lineBuf = p.lineBuf[nlIdx+1:]
		if p.state == fencedStateIdle {
			p.processIdleLine(line)
		} else {
			p.processBlockLine(line)
		}
	}
}

func (p *fencedParser) processIdleLine(line string) {
	if prefix, paramStr, ok := blockOpener(line); ok {
		p.openBlock(prefix, paramStr)
		return
	}

	fence := isCodeFenceLine(line)
	if p.closingFenceExpected {
		p.closingFenceExpected = false
		if fence && strings.TrimSpace(line) == codeFence {
			return
		}
	}
	p.flushPendingFence()
	if fence && !p.inCodeFence {
		p.pendingFence, p.inCodeFence = line, true
		return
	}
	if fence {
		p.inCodeFence = false
	}

	p.emit(fencedOutput{outputType: fencedOutputText, text: line + "\n"})
}

func (p *fencedParser) openBlock(prefix, paramStr string) {
	if p.pendingFence != "" {
		p.pendingFence, p.inCodeFence, p.closingFenceExpected = "", false, true
	}
	var block *FencedBlockEvent
	if prefix == BlockPrefixSaveArtifact {
		block = &FencedBlockEvent{Type: FencedBlockArtifact}
		parseBlockParams(paramStr, block)
		p.state = fencedStateInArtifact
	} else {
		block = &FencedBlockEvent{Type: FencedBlockTemplate, Limit: -1}
		parseTemplateParams(paramStr, block)
		p.state = fencedStateInTemplate
	}
	p.currentBlock = block
	p.nestingDepth = 1
	p.blockContent.Reset()
	p.emit(fencedOutput{outputType: fencedOutputBlockStart, block: block})
}

func (p *fencedParser) processBlockLine(line string) {
	if _, _, ok := blockOpener(line); ok {
		p.nestingDepth++
		p.appendContent(line + "\n")
		return
	}

	trimmed := strings.TrimSpace(line)
	closerAlone := trimmed == BlockDelimiterClose
	closerInline := !closerAlone && strings.HasSuffix(trimmed, BlockDelimiterClose)

	switch {
	case !closerAlone && !closerInline:
		p.appendContent(line + "\n")
	case p.nestingDepth > 1:
		p.nestingDepth--
		p.appendContent(line + "\n")
	default:
		if closerInline {
			p.appendContent(textBeforeCloser(line) + "\n")
		}
		p.closeBlock()
	}
}

func (p *fencedParser) finish() {
	if p.state == fencedStateIdle {
		p.flushPendingFence()
	}
	if p.lineBuf != "" {
		if p.state != fencedStateIdle {
			p.finishBlockLine()
		} else if !p.closingFenceExpected || strings.TrimSpace(p.lineBuf) != codeFence {
			p.emit(fencedOutput{outputType: fencedOutputText, text: p.lineBuf})
		}
	}
	if p.state != fencedStateIdle && p.currentBlock != nil {
		p.currentBlock.Incomplete = true
		p.currentBlock.Content = p.blockContent.String()
		p.emit(fencedOutput{outputType: fencedOutputBlockEnd, block: p.currentBlock})
	}
}

func textBeforeCloser(line string) string {
	body := strings.TrimRightFunc(line, unicode.IsSpace)
	return body[:len(body)-len(BlockDelimiterClose)]
}

func (p *fencedParser) finishBlockLine() {
	trimmed := strings.TrimSpace(p.lineBuf)
	closerAlone := trimmed == BlockDelimiterClose
	closerInline := !closerAlone && strings.HasSuffix(trimmed, BlockDelimiterClose)

	switch {
	case (closerAlone || closerInline) && p.nestingDepth <= 1:
		if closerInline {
			p.appendContent(textBeforeCloser(p.lineBuf))
		}
		p.closeBlock()
	case closerAlone || closerInline:
		p.nestingDepth--
		p.appendContent(p.lineBuf)
	default:
		p.appendContent(p.lineBuf)
	}
}

const codeFence = "```"

func blockOpener(line string) (prefix, params string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimLeft(line, " \t"), BlockDelimiterOpen)
	if !found {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " ")
	for _, p := range []string{BlockPrefixSaveArtifact, BlockPrefixTemplateLiquid, BlockPrefixTemplate} {
		if after, ok := strings.CutPrefix(rest, strings.TrimPrefix(p, BlockDelimiterOpen)); ok {
			return p, strings.TrimSpace(after), true
		}
	}
	return "", "", false
}

func isCodeFenceLine(line string) bool {
	tag, ok := strings.CutPrefix(strings.TrimSpace(line), codeFence)
	return ok && !strings.ContainsAny(tag, " `")
}

func parseBlockParams(paramStr string, block *FencedBlockEvent) {
	matches := paramPattern.FindAllStringSubmatch(paramStr, -1)
	for _, m := range matches {
		key := m[1]
		value := m[2]
		switch key {
		case "filename":
			block.Filename = value
			block.FilenameSet = true
		case "mime_type":
			block.MIMEType = value
		case "description":
			block.Description = value
		case "tags":
			if value != "" {
				for tag := range strings.SplitSeq(value, ",") {
					tag = strings.TrimSpace(tag)
					if tag != "" {
						block.Tags = append(block.Tags, tag)
					}
				}
			}
		case "status":
			block.Status = value
		case "message":
			block.Message = value
		}
	}
}

func parseTemplateParams(paramStr string, block *FencedBlockEvent) {
	matches := paramPattern.FindAllStringSubmatch(paramStr, -1)
	for _, m := range matches {
		key := m[1]
		value := m[2]
		switch key {
		case "data":
			block.DataSource = value
		case "jsonpath":
			block.JSONPath = value
		case "limit":
			if v, err := strconv.Atoi(value); err == nil {
				block.Limit = v
			}
		}
	}
}
