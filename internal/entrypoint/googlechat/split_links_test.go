// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func splitWithoutKeepingLinks(text string, limit int) []string {
	var chunks []string
	for utf8.RuneCountInString(text) > limit {
		runes := []rune(text)
		cut := bestBreak(runes[:limit], max(limit-limit/6, minSplitRunes))
		chunks = append(chunks, strings.TrimRight(string(runes[:cut]), " \n"))
		text = strings.TrimLeft(string(runes[cut:]), " \n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func withoutSpace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func assertWholeChunks(t *testing.T, input string, chunks []string, limit int) {
	t.Helper()
	for i, chunk := range chunks {
		if chunk == "" {
			t.Errorf("chunk %d is empty", i)
		}
		if n := utf8.RuneCountInString(chunk); n > limit {
			t.Errorf("chunk %d is %d runes, over the limit of %d", i, n, limit)
		}
		if opened, closed := strings.Count(chunk, "<"), strings.Count(chunk, ">"); opened != closed {
			t.Errorf("chunk %d has %d link starts and %d ends: %.120q", i, opened, closed, chunk)
		}
	}
	if got, want := withoutSpace(strings.Join(chunks, "")), withoutSpace(input); got != want {
		t.Errorf("the chunks do not hold the text: %d runes, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
}

func TestSplitKeepsLinksWholeAfterTextInAnyScript(t *testing.T) {
	link := "<https://example.com/" + strings.Repeat("a", 1500) + "|[1]>"
	for _, unit := range []string{"word ", "é ", "日本語 "} {
		for _, back := range []int{2600, 2000, 1600, 1000, 400, 40} {
			t.Run(fmt.Sprintf("%q/%d", unit, back), func(t *testing.T) {
				text := strings.Repeat(unit, (maxMessageRunes-back)/utf8.RuneCountInString(unit)) + link + " and the rest of the answer, " + strings.Repeat(unit, 200)

				assertWholeChunks(t, text, splitForChat(text, maxMessageRunes), maxMessageRunes)
			})
		}
	}
}

func TestSplitKeepsAnHTTPLinkWhole(t *testing.T) {
	link := "<http://example.com/" + strings.Repeat("a", 1500) + "|[1]>"
	text := strings.Repeat("word ", (maxMessageRunes-1000)/5) + link + " and the rest " + strings.Repeat("word ", 300)

	assertWholeChunks(t, text, splitForChat(text, maxMessageRunes), maxMessageRunes)
}

func TestSplitOfAnOpenLinkAtTheStartTerminates(t *testing.T) {
	text := "<https://x.example/" + strings.Repeat("a", 6000)
	for _, limit := range []int{maxMessageRunes, 1000, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			done := make(chan []string, 1)
			go func() { done <- splitForChat(text, limit) }()

			select {
			case chunks := <-done:
				if got, want := withoutSpace(strings.Join(chunks, "")), withoutSpace(text); got != want {
					t.Errorf("the chunks do not hold the text")
				}
				for i, chunk := range chunks {
					if chunk == "" || utf8.RuneCountInString(chunk) > limit {
						t.Errorf("chunk %d is %d runes", i, utf8.RuneCountInString(chunk))
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("splitForChat did not return")
			}
		})
	}
}

func TestSplitOfTextWithoutLinksIsUnchanged(t *testing.T) {
	tokens := []string{"word ", "a < b ", "<b>", "x > y", "\n", "  ", "é", "日本", "```", "<", ">", "<ht", "code ", "\n\n"}
	for seed := int64(1); seed <= 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var b strings.Builder
		for b.Len() < 12000 && rng.Intn(400) != 0 || b.Len() < 200 {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		text := b.String()
		if strings.Contains(text, linkStart) {
			continue
		}
		for _, limit := range []int{maxMessageRunes, 1500, 100} {
			got, want := splitForChat(text, limit), splitWithoutKeepingLinks(text, limit)
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("seed %d limit %d: split differs from the reference: %d chunks, want %d", seed, limit, len(got), len(want))
			}
		}
	}
}

func TestSplitOfTextWithLinksHoldsTogether(t *testing.T) {
	links := []string{
		"<https://example.com/a|[1]>",
		"<https://example.com/" + strings.Repeat("b", 900) + "|[2]>",
		"<http://example.org/" + strings.Repeat("c", 1900) + "|[3]>",
	}
	tokens := append([]string{"word ", "é ", "日本語 ", "\n", "code "}, links...)
	for seed := int64(1); seed <= 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var b strings.Builder
		for b.Len() < 14000 {
			b.WriteString(tokens[rng.Intn(len(tokens))])
			if rng.Intn(3) == 0 {
				b.WriteString(" ")
			}
		}
		text := b.String()
		for _, limit := range []int{maxMessageRunes, 3000} {
			assertWholeChunks(t, text, splitForChat(text, limit), limit)
		}
	}
}

func TestSplitIgnoresAnOpenLinkFarFromTheCut(t *testing.T) {
	text := "a <http://x " + strings.Repeat("word ", 1000)

	chunks := splitForChat(text, maxMessageRunes)

	if want := splitWithoutKeepingLinks(text, maxMessageRunes); strings.Join(chunks, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("chunk sizes = %v, want %v", chunkSizes(chunks), chunkSizes(want))
	}
}

func chunkSizes(chunks []string) []int {
	sizes := make([]int, len(chunks))
	for i, c := range chunks {
		sizes[i] = utf8.RuneCountInString(c)
	}
	return sizes
}

func TestSplitKeepsALinkWholeWhenTheCutFallsInItsFirstCharacters(t *testing.T) {
	const limit = 100
	link := "<https://x.example/p|[1]> and the rest of it, which is long enough to need more than one message."
	for into := 1; into <= len(linkStart); into++ {
		t.Run(fmt.Sprint(into), func(t *testing.T) {
			text := strings.Repeat("a", limit-into) + link

			assertWholeChunks(t, text, splitForChat(text, limit), limit)
		})
	}
}

func TestSplitDoesNotCutBeforeALinkItHasPassed(t *testing.T) {
	const limit = 100
	text := strings.Repeat("a", 50) + "<https://x.example/a|[1]>" + strings.Repeat("b", 200)

	got, want := splitForChat(text, limit), splitWithoutKeepingLinks(text, limit)

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("chunk sizes = %v, want %v: the cut came before a link it was past", chunkSizes(got), chunkSizes(want))
	}
}
