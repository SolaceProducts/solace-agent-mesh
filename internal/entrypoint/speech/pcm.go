// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
)

const defaultPCMSampleRate = 24000

func isPCMMime(mime string) bool {
	m := strings.ToLower(mime)
	return strings.HasPrefix(m, "audio/l16") ||
		strings.HasPrefix(m, "audio/pcm") ||
		strings.Contains(m, "codec=pcm")
}

func sampleRateFromMime(mime string) int {
	for part := range strings.SplitSeq(mime, ";") {
		part = strings.TrimSpace(strings.ToLower(part))
		if after, ok := strings.CutPrefix(part, "rate="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(after)); err == nil && n > 0 {
				return n
			}
		}
	}
	return defaultPCMSampleRate
}

func pcmToWAV(pcm []byte, sampleRate int) []byte {
	const (
		channels      = 1
		bitsPerSample = 16
	)
	if sampleRate <= 0 {
		sampleRate = defaultPCMSampleRate
	}
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataLen := len(pcm)

	buf := bytes.NewBuffer(make([]byte, 0, 44+dataLen))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(pcm)
	return buf.Bytes()
}
