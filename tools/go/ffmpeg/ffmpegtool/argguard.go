// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ffmpegtool

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

var blockedProtocols = map[string]bool{
	"async": true, "bluray": true, "cache": true, "concat": true, "concatf": true, "crypto": true,
	"data": true, "dtls": true, "fd": true, "ftp": true, "gopher": true, "gophers": true,
	"hls": true, "http": true, "httpproxy": true, "https": true, "icecast": true, "ipfs": true,
	"ipns": true, "librist": true, "libsrt": true, "libssh": true, "md5": true, "mmsh": true,
	"mmst": true, "pipe": true, "prompeg": true, "rist": true, "rtmp": true, "rtmpe": true,
	"rtmps": true, "rtmpt": true, "rtmpte": true, "rtmpts": true, "rtp": true, "sctp": true,
	"sftp": true, "smb": true, "srt": true, "srtp": true, "subfile": true, "tcp": true,
	"tee": true, "tls": true, "udp": true, "udplite": true, "unix": true, "zmq": true,
}

var blockedOptions = map[string]string{
	"-filter_script":         "it reads the filtergraph from a file",
	"-filter_complex_script": "it reads the filtergraph from a file",
	"-protocol_whitelist":    "the tool sets the protocol whitelist itself",
	"-protocol_blacklist":    "the tool sets the protocol whitelist itself",
	"-dump_attachment":       "it can write to a path named inside the input",
}

var blockedInputFormats = map[string]bool{
	"hls": true, "applehttp": true, "dash": true, "sdp": true, "rtp": true, "rtsp": true,
}

var captureDevices = map[string]bool{
	"alsa": true, "android_camera": true, "avfoundation": true, "bktr": true, "decklink": true,
	"dshow": true, "dv1394": true, "fbdev": true, "gdigrab": true, "iec61883": true, "jack": true,
	"kmsgrab": true, "libcdio": true, "libdc1394": true, "openal": true, "oss": true, "pulse": true,
	"sndio": true, "v4l2": true, "video4linux2": true, "vfwcap": true, "x11grab": true, "xcbgrab": true,
}

var blockedInputExtensions = map[string]bool{".m3u8": true, ".m3u": true, ".mpd": true, ".sdp": true}

const concatFormat = "concat"

var (
	placeholderPattern   = regexp.MustCompile(`\{(?:input\[\d+\]|output|output_dir)\}`)
	inputPlaceholder     = regexp.MustCompile(`^\{input\[(\d+)\]\}$`)
	filterFormatOption   = regexp.MustCompile(`(?i)(?:^|[:=])\s*(?:f|format_name)\s*=\s*([A-Za-z0-9_]+)`)
	protocolPattern      = regexp.MustCompile(`(?:^|[=,;'"\[\]|])([A-Za-z][A-Za-z0-9+.-]*):\S`)
	absolutePattern      = regexp.MustCompile(`(?:^|[=:,;|'"\[\]])(?:[/\\~]|[A-Za-z]:[/\\])`)
	parentPattern        = regexp.MustCompile(`(?:^|[/\\=:,;|'"\[\]])\.\.(?:[/\\]|$)`)
	dashManifestPattern  = regexp.MustCompile(`(?i)<mpd`)
	blockedFilterPattern = regexp.MustCompile(`(?i)(?:^|[,;\]])\s*(frei0r|frei0r_src|ladspa|lv2|zmq|azmq|sendcmd|asendcmd)\b`)
)

const protocolWhitelist = "file"

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
		token := tokens[i]
		option := strings.ToLower(token)
		if strings.HasPrefix(token, "-/") {
			return &commandViolation{token, "blocked_option", "it reads the option's value from a file"}
		}
		name, _, _ := strings.Cut(option, ":")
		if reason, ok := blockedOptions[name]; ok {
			return &commandViolation{token, "blocked_option", reason}
		}
		if strings.HasPrefix(option, "-metadata") {
			i++
			continue
		}
		if option == "-f" && i+1 < len(tokens) {
			if v := inputFormatViolation(tokens[i+1], tokens[i+2:]); v != nil {
				return v
			}
		}
		if rule, reason := tokenViolation(token); rule != "" {
			return &commandViolation{token, rule, reason}
		}
	}
	return nil
}

func inputFormatViolation(format string, rest []string) *commandViolation {
	input, ok := nextInput(rest)
	if !ok {
		return nil
	}
	name := strings.ToLower(format)
	if blockedInputFormats[name] {
		return &commandViolation{format, "blocked_input_format", "this input format opens the files or streams a playlist names"}
	}
	if captureDevices[name] {
		return &commandViolation{format, "capture_device", "capture devices record the host's screen, camera or microphone"}
	}
	if name == concatFormat && !inputPlaceholder.MatchString(input) {
		return &commandViolation{input, "concat_list_reference", "open a concat list only as {input[N]}, so the tool can check the files it names"}
	}
	return nil
}

func nextInput(rest []string) (string, bool) {
	previous := ""
	for i, token := range rest {
		if token == "-i" && i+1 < len(rest) {
			return rest[i+1], true
		}
		if strings.Contains(token, "{output") && !strings.HasPrefix(previous, "-") {
			return "", false
		}
		previous = token
	}
	return "", false
}

func tokenViolation(token string) (rule, reason string) {
	neutral := placeholderPattern.ReplaceAllString(token, "P")
	unescaped := strings.ReplaceAll(neutral, `\`, "")
	if strings.Contains(unescaped, "://") {
		return "url", "URLs are not allowed; fetch remote media with a web tool and pass it in input_files, and put text containing a URL in an input file used with drawtext textfile="
	}
	for _, match := range protocolPattern.FindAllStringSubmatch(unescaped, -1) {
		if protocol := strings.ToLower(match[1]); blockedProtocols[protocol] {
			return "blocked_protocol", fmt.Sprintf("the %s: protocol reaches the network or opens what the tool does not provide", protocol)
		}
	}
	if match := blockedFilterPattern.FindStringSubmatch(unescaped); match != nil {
		return "blocked_filter", fmt.Sprintf("the %s filter loads a plugin, opens a network socket or reads commands from a file", strings.ToLower(match[1]))
	}
	for _, match := range filterFormatOption.FindAllStringSubmatch(unescaped, -1) {
		if format := strings.ToLower(match[1]); blockedInputFormats[format] || captureDevices[format] || format == concatFormat {
			return "blocked_input_format", fmt.Sprintf("a filter may not open its input with the %s format", format)
		}
	}
	if absolutePattern.MatchString(neutral) {
		return "absolute_path", "absolute and home-directory paths are not allowed; reference files only through {input[N]}, {output} and {output_dir}"
	}
	if parentPattern.MatchString(neutral) {
		return "parent_path", "'..' path segments are not allowed"
	}
	return "", ""
}

func validateInput(name string, content []byte, concat bool) *commandViolation {
	ext := strings.ToLower(filepath.Ext(name))
	if blockedInputExtensions[ext] {
		return &commandViolation{name, "blocked_input_type", fmt.Sprintf("%s files are playlists that name further files or streams", ext)}
	}
	head := content[:min(len(content), 4096)]
	if bytes.HasPrefix(head, []byte("#EXTM3U")) || dashManifestPattern.Match(content) {
		return &commandViolation{name, "blocked_input_type", "playlists and DASH manifests name further files or streams"}
	}
	if concat || ext == ".ffconcat" || bytes.HasPrefix(head, []byte("ffconcat")) {
		return validateConcatList(name, content)
	}
	return nil
}

func validateConcatList(name string, content []byte) *commandViolation {
	for _, raw := range bytes.FieldsFunc(content, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line := strings.TrimSpace(string(raw))
		directive, rest := line, ""
		if end := strings.IndexAny(line, " \t\v\f"); end >= 0 {
			directive, rest = line[:end], line[end+1:]
		}
		if directive == "option" {
			return &commandViolation{name, "concat_option", "the concat option directive is not allowed"}
		}
		if directive != "file" {
			continue
		}
		words, err := parseArgs(rest, "")
		if err != nil || len(words) != 1 {
			return &commandViolation{name, "concat_entry", fmt.Sprintf("the concat entry %q could not be read as one file name", strings.TrimSpace(rest))}
		}
		entry := words[0]
		if strings.IndexAny(entry, `/\~`) == 0 || strings.Contains(entry, ":") || parentPattern.MatchString(entry) {
			return &commandViolation{name, "concat_entry", fmt.Sprintf("the concat entry %q must name another input file by its file name", entry)}
		}
	}
	return nil
}

func concatInputs(tokens []string) map[int]bool {
	inputs := map[int]bool{}
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] != "-f" || !strings.EqualFold(tokens[i+1], concatFormat) {
			continue
		}
		input, ok := nextInput(tokens[i+2:])
		if !ok {
			continue
		}
		if match := inputPlaceholder.FindStringSubmatch(input); match != nil {
			index, _ := strconv.Atoi(match[1])
			inputs[index] = true
		}
	}
	return inputs
}

func withProtocolWhitelist(args []string) []string {
	out := make([]string, 0, len(args)+2)
	for _, arg := range args {
		if arg == "-i" {
			out = append(out, "-protocol_whitelist", protocolWhitelist)
		}
		out = append(out, arg)
	}
	return out
}

func isPlainFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func inputFilename(name string, index int, taken map[string]bool) (filename string, renamed bool) {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if !isPlainFilename(base) {
		base = fmt.Sprintf("input_%d", index)
	}
	if taken[strings.ToLower(base)] {
		base = fmt.Sprintf("%d_%s", index, base)
		renamed = true
	}
	taken[strings.ToLower(base)] = true
	return base, renamed
}

func logRejection(tc *sdk.ToolContext, tool, rule string) {
	slog.Default().With("logger_name", "ffmpeg").Warn("ffmpeg call rejected", "taskID", tc.TaskID, "tool", tool, "rule", rule)
}
