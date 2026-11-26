// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var freshNameExtensions = map[string]string{
	"text/plain":       ".txt",
	"text/csv":         ".csv",
	"text/html":        ".html",
	"text/markdown":    ".md",
	"application/json": ".json",
	"application/xml":  ".xml",
	"text/xml":         ".xml",
	"application/yaml": ".yaml",
	"text/yaml":        ".yaml",
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/svg+xml":    ".svg",
	"application/pdf":  ".pdf",
	"audio/mpeg":       ".mp3",
	"audio/wav":        ".wav",
}

func FreshName(mimeType string) string {
	ext, ok := freshNameExtensions[mimeType]
	if !ok {
		ext = ".bin"
	}
	return "output_" + freshNameSuffix() + ext
}

func freshNameSuffix() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strings.ReplaceAll(idgen.NewRandom(), "-", "")[:16]
	}
	return hex.EncodeToString(random[:])
}
