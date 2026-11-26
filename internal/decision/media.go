// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/gabriel-vasile/mimetype"
)

const (
	DefaultMaxMedia      = 2
	DefaultMaxMediaBytes = 20 << 20
	MaxMediaPerRequest   = 128
	MaxMediaBytesLimit   = 64 << 20
)

const (
	wireM4A       = "audio/m4a"
	wireMP4       = "video/mp4"
	wireVideoWebM = "video/webm"
)

var mediaWireTypes = map[MediaKind]map[string]string{
	MediaImage: {
		"image/png":  "image/png",
		"image/jpeg": "image/jpeg",
		"image/webp": "image/webp",
		"image/heic": "image/heic",
		"image/heif": "image/heif",
		"image/gif":  "image/gif",
		"image/bmp":  "image/bmp",
		"image/tiff": "image/tiff",
	},
	MediaAudio: {
		"audio/wav":   "audio/wav",
		"audio/mpeg":  "audio/mpeg",
		"audio/aiff":  "audio/aiff",
		"audio/aac":   "audio/aac",
		"audio/ogg":   "audio/ogg",
		"audio/flac":  "audio/flac",
		"audio/x-m4a": wireM4A,
		"audio/mp4":   wireM4A,
		wireVideoWebM: "audio/webm",
	},
	MediaVideo: {
		wireMP4:           wireMP4,
		"video/x-m4v":     wireMP4,
		"video/mpeg":      "video/mpeg",
		"video/quicktime": "video/mov",
		"video/x-msvideo": "video/avi",
		wireVideoWebM:     wireVideoWebM,
		"video/x-ms-asf":  "video/wmv",
		"video/x-flv":     "video/x-flv",
		"video/3gpp":      "video/3gpp",
	},
	MediaDocument: {
		"application/pdf": "application/pdf",
	},
}

func MediaMIMETypes(kind MediaKind) []string {
	return slices.Compact(slices.Sorted(maps.Values(mediaWireTypes[kind])))
}

func SniffMedia(data []byte, kind MediaKind) (string, bool) {
	detected := mimetype.Detect(data)
	wire := mediaWireTypes[kind]
	if mapped, ok := wire[detected.String()]; ok {
		return mapped, true
	}
	if kind == MediaAudio && detected.String() == wireMP4 && audioOnlyMP4(data) {
		return wireM4A, true
	}
	if kind != MediaImage {
		return detected.String(), false
	}
	for m := detected.Parent(); m != nil; m = m.Parent() {
		if mapped, ok := wire[m.String()]; ok {
			return mapped, true
		}
	}
	return detected.String(), false
}

var ErrMediaUnsupported = errors.New("this decision model does not accept this media")

func MediaUnsupportedError(kind MediaKind, model string) error {
	return fmt.Errorf("decision model %q does not read %s: %w", model, kindNoun(kind), ErrMediaUnsupported)
}

func kindNoun(kind MediaKind) string {
	switch kind {
	case MediaImage:
		return "images"
	case MediaDocument:
		return "documents"
	default:
		return string(kind)
	}
}

type ProviderLimitError struct {
	What  string
	Bytes int64
	Limit int64
}

func (e *ProviderLimitError) Error() string {
	return fmt.Sprintf("%s total %d bytes; the provider takes at most %d in one call (send smaller or fewer files)", e.What, e.Bytes, e.Limit)
}

type MediaTooLargeError struct {
	reason     mediaTooLargeReason
	file       string
	count      int
	countLimit int
	bytes      int64
	byteLimit  int64
}

type mediaTooLargeReason int

const (
	tooManyMedia mediaTooLargeReason = iota + 1
	mediaOverBudget
	mediaFileTooLarge
)

func TooManyMedia(count, limit int) *MediaTooLargeError {
	return &MediaTooLargeError{reason: tooManyMedia, count: count, countLimit: limit}
}

func MediaOverBudget(bytes, byteLimit int64) *MediaTooLargeError {
	return &MediaTooLargeError{reason: mediaOverBudget, bytes: bytes, byteLimit: byteLimit}
}

func MediaFileTooLarge(name string, bytes, byteLimit int64) *MediaTooLargeError {
	return &MediaTooLargeError{reason: mediaFileTooLarge, file: name, bytes: bytes, byteLimit: byteLimit}
}

func (e *MediaTooLargeError) Error() string {
	switch e.reason {
	case tooManyMedia:
		return fmt.Sprintf("decision sends %d audio, video and document files; the limit is %d (set max_media, or send fewer)", e.count, e.countLimit)
	case mediaOverBudget:
		return fmt.Sprintf("decision audio, video and documents are %d bytes; the limit is %d (set max_media_bytes, or send smaller files)", e.bytes, e.byteLimit)
	case mediaFileTooLarge:
		return fmt.Sprintf("file %q is %d bytes; the limit is %d (set max_media_bytes, or send a smaller file)", e.file, e.bytes, e.byteLimit)
	default:
		return "decision audio, video and documents are over a limit"
	}
}
