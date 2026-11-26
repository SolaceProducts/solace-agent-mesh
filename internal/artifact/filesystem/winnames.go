// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package filesystem

import "strings"

const escapeChar = '%'

const windowsReservedChars = `<>:"|?*\`

var escapeAnywhereCodes = map[string]byte{
	"3C": '<', "3E": '>', "3A": ':', "22": '"', "7C": '|', "3F": '?', "2A": '*', "5C": '\\',
	"25": escapeChar,
}

var trailingCodes = map[string]byte{"2E": '.', "20": ' '}

var deviceInitialCodes = map[string]byte{
	"41": 'A', "61": 'a', "43": 'C', "63": 'c', "4C": 'L', "6C": 'l',
	"4E": 'N', "6E": 'n', "50": 'P', "70": 'p',
}

var windowsDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

const upperHex = "0123456789ABCDEF"

func encodeWindowsName(name string) string {
	trailingStart := len(strings.TrimRight(name, ". "))
	device := isWindowsDeviceName(name)

	tokens := make([]string, len(name))
	suffixIsTrailingRun := make([]bool, len(name)+1)
	suffixIsTrailingRun[len(name)] = true
	for i := len(name) - 1; i >= 0; i-- {
		c := name[i]
		switch {
		case i >= trailingStart:
			tokens[i] = escapeByte(c)
			suffixIsTrailingRun[i] = suffixIsTrailingRun[i+1]
			continue
		case i == 0 && device:
			tokens[i] = escapeByte(c)
		case strings.IndexByte(windowsReservedChars, c) >= 0:
			tokens[i] = escapeByte(c)
		case c == escapeChar && decoderWouldReadEscape(name, i, suffixIsTrailingRun):
			tokens[i] = escapeByte(c)
		default:
			tokens[i] = string(c)
		}
		suffixIsTrailingRun[i] = false
	}
	return strings.Join(tokens, "")
}

func decoderWouldReadEscape(name string, i int, suffixIsTrailingRun []bool) bool {
	code := escapeCodeAt(name, i)
	if _, ok := escapeAnywhereCodes[code]; ok {
		return true
	}
	if _, ok := trailingCodes[code]; ok && suffixIsTrailingRun[i+3] {
		return true
	}
	if initial, ok := deviceInitialCodes[code]; ok && i == 0 {
		return isWindowsDeviceName(string(initial) + name[3:])
	}
	return false
}

func decodeWindowsName(encoded string) string {
	runStart := len(encoded)
	for runStart >= 3 {
		if _, ok := trailingCodes[escapeCodeAt(encoded, runStart-3)]; !ok {
			break
		}
		runStart -= 3
	}

	var b strings.Builder
	b.Grow(len(encoded))
	body := encoded[:runStart]
	start := 0
	if initial, ok := deviceInitialCodes[escapeCodeAt(body, 0)]; ok {
		if rest := string(initial) + decodeAnywhere(body[3:]) + decodeTrailingRun(encoded[runStart:]); isWindowsDeviceName(rest) {
			b.WriteByte(initial)
			start = 3
		}
	}
	b.WriteString(decodeAnywhere(body[start:]))
	b.WriteString(decodeTrailingRun(encoded[runStart:]))
	return b.String()
}

func decodeAnywhere(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c, ok := escapeAnywhereCodes[escapeCodeAt(s, i)]; ok {
			b.WriteByte(c)
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func decodeTrailingRun(run string) string {
	var b strings.Builder
	for i := 0; i+3 <= len(run); i += 3 {
		b.WriteByte(trailingCodes[escapeCodeAt(run, i)])
	}
	return b.String()
}

func escapeCodeAt(s string, i int) string {
	if i < 0 || i+3 > len(s) || s[i] != escapeChar {
		return ""
	}
	return strings.ToUpper(s[i+1 : i+3])
}

func escapeByte(c byte) string {
	return string([]byte{escapeChar, upperHex[c>>4], upperHex[c&0x0F]})
}

func isWindowsDeviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	return windowsDeviceNames[strings.ToUpper(strings.TrimRight(stem, " "))]
}
