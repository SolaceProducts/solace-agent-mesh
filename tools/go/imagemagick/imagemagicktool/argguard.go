// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var alwaysBlockedCoders = map[string]bool{
	"url": true, "http": true, "https": true, "ftp": true, "fd": true,
	"msl": true, "mvg": true, "ephemeral": true, "inline": true, "print": true,
}

var networkCoders = map[string]bool{"url": true, "http": true, "https": true, "ftp": true}

var blockedInputExtensions = map[string]bool{".mvg": true, ".msl": true}

var pathOnlyCoders = map[string]bool{"text": true, "txt": true, "file": true}

var textCoders = map[string]bool{"label": true, "caption": true, "pango": true}

var textOptions = map[string]int{
	"-annotate": 2, "-comment": 1, "-label": 1, "-caption": 1, "-title": 1, "-format": 1, "-print": 1,
}

var blockedOptions = map[string]bool{"-script": true}

var (
	placeholderPattern = regexp.MustCompile(`\{(?:input\[\d+\]|output|output_dir)\}`)
	coderPattern       = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):(.*)$`)
	absolutePattern    = regexp.MustCompile(`^(?:[/\\]|[A-Za-z]:[/\\])`)
	parentPattern      = regexp.MustCompile(`(?:^|[/\\])\.\.(?:[/\\]|$)`)
	drawImagePattern   = regexp.MustCompile(`(?i)\bimage\s+\S+\s+\S+\s+\S+\s+('[^']*'|"[^"]*"|\S+)`)
	drawFontPattern    = regexp.MustCompile(`(?i)\bfont\s+('[^']*'|"[^"]*"|\S+)`)
	drawFileRefPattern = regexp.MustCompile(`(?:^|[\s'"])@`)
)

type commandViolation struct {
	token  string
	rule   string
	reason string
}

func (v *commandViolation) Error() string {
	return fmt.Sprintf("argument %q is not allowed: %s", v.token, v.reason)
}

func validateArgs(tokens []string) *commandViolation {
	for i := 0; i < len(tokens); i++ {
		option := strings.ToLower(tokens[i])
		if blockedOptions[option] {
			return &commandViolation{tokens[i], "blocked_option", "it reads ImageMagick commands from a file"}
		}
		if n := textOptions[option]; n > 0 {
			for _, text := range tokens[i+1 : min(i+1+n, len(tokens))] {
				if rule, reason := textViolation(text); rule != "" {
					return &commandViolation{text, rule, reason}
				}
			}
			i += n
			continue
		}
		if rule, reason := tokenViolation(tokens[i]); rule != "" {
			return &commandViolation{tokens[i], rule, reason}
		}
		if option == "-draw" && i+1 < len(tokens) {
			if rule, reason := drawViolation(tokens[i+1]); rule != "" {
				return &commandViolation{tokens[i+1], rule, reason}
			}
		}
	}
	return nil
}

func validateInputName(name string) *commandViolation {
	if ext := strings.ToLower(filepath.Ext(name)); blockedInputExtensions[ext] {
		return &commandViolation{name, "blocked_input_type", fmt.Sprintf("%s files are ImageMagick scripts that can open arbitrary files", ext)}
	}
	return nil
}

func isPlainFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func inputFilename(name string, index int) string {
	base := filepath.Base(strings.ReplaceAll(name, `\`, string(os.PathSeparator)))
	if !isPlainFilename(base) {
		return fmt.Sprintf("input_%d", index)
	}
	return base
}

func tokenViolation(token string) (rule, reason string) {
	match := coderPattern.FindStringSubmatch(token)
	if match == nil {
		return pathViolation(token)
	}
	coder, rest := strings.ToLower(match[1]), match[2]
	switch {
	case alwaysBlockedCoders[coder]:
		return "blocked_coder", blockedCoderReason(coder)
	case pathOnlyCoders[coder] && !placeholderPattern.MatchString(rest):
		return "path_only_coder", fmt.Sprintf("the %s: coder may only read a provided artifact, as %s:{input[N]}", coder, coder)
	case textCoders[coder]:
		return textViolation(rest)
	}
	return pathViolation(rest)
}

func blockedCoderReason(coder string) string {
	if networkCoders[coder] {
		return fmt.Sprintf("the %s: coder reaches the network; fetch the image with a web tool and pass it in input_images as {input[N]}", coder)
	}
	return fmt.Sprintf("the %s: coder opens files or runs scripts the tool does not provide", coder)
}

func textViolation(text string) (rule, reason string) {
	if strings.HasPrefix(text, "@") {
		return "file_reference", "text starting with @ is read from a file; drop the @ or put a space before it"
	}
	return "", ""
}

func pathViolation(value string) (rule, reason string) {
	neutral := placeholderPattern.ReplaceAllString(value, "P")
	candidates := []string{neutral}
	if _, assigned, ok := strings.Cut(neutral, "="); ok {
		candidates = append(candidates, assigned)
	}
	for _, path := range candidates {
		switch {
		case strings.HasPrefix(path, "@"):
			return "file_reference", "@ reads the argument's value from a file; pass the content as an input artifact instead"
		case strings.HasPrefix(path, "|"):
			return "pipe", "a leading | runs a command"
		case strings.HasPrefix(path, "~"):
			return "home_path", "paths in the home directory are not allowed; reference files only through {input[N]}, {output} and {output_dir}"
		case absolutePattern.MatchString(path):
			return "absolute_path", "absolute paths are not allowed; reference files only through {input[N]}, {output} and {output_dir}, and fonts by name"
		}
	}
	if parentPattern.MatchString(neutral) {
		return "parent_path", "'..' path segments are not allowed"
	}
	return "", ""
}

func drawViolation(draw string) (rule, reason string) {
	for _, match := range drawImagePattern.FindAllStringSubmatch(draw, -1) {
		if rule, reason := tokenViolation(strings.Trim(match[1], `'"`)); rule != "" {
			return rule, reason
		}
	}
	for _, match := range drawFontPattern.FindAllStringSubmatch(draw, -1) {
		if rule, reason := pathViolation(strings.Trim(match[1], `'"`)); rule != "" {
			return rule, reason
		}
	}
	if drawFileRefPattern.MatchString(draw) {
		return "file_reference", "@ inside -draw reads text from a file"
	}
	return "", ""
}
