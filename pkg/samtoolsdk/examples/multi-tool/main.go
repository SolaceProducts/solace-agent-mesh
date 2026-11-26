// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

// multi-tool shows how to host several tools in a single binary.
//
// A single sdk.Run call registers all tools. The STR discovers them via
// `./multi-tool --schema` at startup and routes each invocation to the
// correct handler via the tool_name field in runner_args.json.
//
// Grouping related tools into one binary keeps the manifest lean and lets
// tools share helper code (e.g. a shared HTTP client) compiled once.
package main

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type WordCountParams struct {
	Text string `json:"text" desc:"Text to analyse"`
}

func wordCount(_ context.Context, p WordCountParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	words := len(strings.Fields(p.Text))
	chars := utf8.RuneCountInString(p.Text)
	lines := strings.Count(p.Text, "\n") + 1

	return sdk.OK(
		fmt.Sprintf("%d words, %d chars, %d lines", words, chars, lines),
		sdk.WithData(map[string]any{
			"words": words,
			"chars": chars,
			"lines": lines,
		}),
	), nil
}

type ReverseParams struct {
	Text string `json:"text" desc:"Text to reverse"`
}

func reverse(_ context.Context, p ReverseParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	runes := []rune(p.Text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return sdk.OK(string(runes)), nil
}

type RepeatParams struct {
	Text  string `json:"text"  desc:"Text to repeat"`
	Times int    `json:"times" desc:"Number of repetitions (1–20)"`
}

func repeat(_ context.Context, p RepeatParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.Times < 1 || p.Times > 20 {
		return sdk.Error("times must be between 1 and 20"), nil
	}
	_ = tc.SendStatus(fmt.Sprintf("repeating %d times…", p.Times))
	return sdk.OK(strings.Repeat(p.Text, p.Times)), nil
}

func main() {
	sdk.Run(
		sdk.NewTool(
			"word_count",
			"Counts words, characters, and lines in the provided text",
			wordCount,
		),
		sdk.NewTool(
			"reverse_text",
			"Reverses the characters in the provided text (Unicode-safe)",
			reverse,
		),
		sdk.NewTool(
			"repeat_text",
			"Repeats the provided text N times (max 20)",
			repeat,
			sdk.WithInstructions("Use this when the user explicitly wants text repeated."),
		),
	)
}
