// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import "context"

type STTProvider interface {
	Transcribe(ctx context.Context, audio []byte, mimeType string, opts STTOptions) (string, error)
}

type STTOptions struct {
	Language string
}

type TTSProvider interface {
	Synthesize(ctx context.Context, text string, opts TTSOptions) ([]byte, string, error)

	SynthesizeStream(ctx context.Context, text string, opts TTSOptions) (<-chan AudioChunk, error)

	ListVoices(ctx context.Context) ([]Voice, error)

	GetSample(ctx context.Context, voiceID string, text string) ([]byte, string, error)
}

type TTSOptions struct {
	VoiceID string
	Speed   float64
}

type Voice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Gender      string `json:"gender,omitempty"`
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
}

type AudioChunk struct {
	Data  []byte
	Error error
}
