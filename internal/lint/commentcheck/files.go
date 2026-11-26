// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package commentcheck

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var excludedPath = regexp.MustCompile(`(^|/)(testdata|vendor|node_modules|examples)/|^(webui|public)/`)

func Excluded(path string) bool {
	return excludedPath.MatchString(path)
}

func TrackedFiles(repoRoot string, pathspecs ...string) ([]string, error) {
	args := append([]string{"-C", repoRoot, "ls-files", "-z", "--"}, pathspecs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, path := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if path == "" || Excluded(path) || LanguageOf(path) == Unsupported {
			continue
		}
		files = append(files, path)
	}
	return files, nil
}

type AddedLines map[string]map[int]bool

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func AddedSince(repoRoot, base string) (AddedLines, error) {
	mergeBase, err := exec.Command("git", "-C", repoRoot, "merge-base", base, "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("git merge-base %s HEAD: %w", base, err)
	}
	out, err := exec.Command("git", "-C", repoRoot, "diff", "-U0", "--no-color", "--no-ext-diff", strings.TrimSpace(string(mergeBase))).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return parseAddedLines(out), nil
}

func parseAddedLines(diff []byte) AddedLines {
	added := AddedLines{}
	var current string
	scanner := bufio.NewScanner(bytes.NewReader(diff))
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if path, ok := strings.CutPrefix(line, "+++ b/"); ok {
			current = path
			continue
		}
		m := hunkHeader.FindStringSubmatch(line)
		if m == nil || current == "" {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		if added[current] == nil {
			added[current] = map[int]bool{}
		}
		for n := start; n < start+count; n++ {
			added[current][n] = true
		}
	}
	return added
}

var knowledgeWords = regexp.MustCompile(`(?i)\b(because|workaround|bug|hack|must|never|don't|do not|otherwise|rfc|spec|upstream|security|datadog|race|deadlock|invariant|compat|legacy|python|todo|fixme)\b`)

func CarriesKnowledge(text string) bool {
	return knowledgeWords.MatchString(text)
}
