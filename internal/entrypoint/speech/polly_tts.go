// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/polly/types"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

var pollyNeuralVoices = []string{
	"Joanna", "Matthew", "Ruth", "Stephen", "Kendra", "Joey", "Kimberly", "Salli", "Ivy",
	"Amy", "Emma", "Brian", "Arthur",
	"Olivia",
	"Liam",
	"Kajal", "Aria",
}

type PollyTTS struct {
	client       *polly.Client
	defaultVoice string
	engine       string
	voices       []string
	logger       *slog.Logger
}

func NewPollyTTS(cfg PollyConfig, logger *slog.Logger) (*PollyTTS, error) {
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(cfg.Region))

	if IsValidAPIKey(cfg.AWSAccessKeyID) && IsValidAPIKey(cfg.AWSSecretAccessKey) {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, ""),
		))
	}

	awsCfg, err := awsretry.Load(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("polly: load aws config: %w", err)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = "neural"
	}
	voice := cfg.DefaultVoice
	if voice == "" {
		voice = "Joanna"
	}

	return &PollyTTS{
		client:       polly.NewFromConfig(awsCfg),
		defaultVoice: voice,
		engine:       engine,
		voices:       cfg.Voices,
		logger:       logger,
	}, nil
}

func (p *PollyTTS) Synthesize(ctx context.Context, text string, opts TTSOptions) ([]byte, string, error) {
	voiceID := opts.VoiceID
	if voiceID == "" {
		voiceID = p.defaultVoice
	}

	input := &polly.SynthesizeSpeechInput{
		Text:         &text,
		VoiceId:      types.VoiceId(voiceID),
		Engine:       types.Engine(p.engine),
		OutputFormat: types.OutputFormatMp3,
	}

	result, err := p.client.SynthesizeSpeech(ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("polly tts: synthesize: %w", err)
	}
	defer func() { _ = result.AudioStream.Close() }()

	audio, err := io.ReadAll(result.AudioStream)
	if err != nil {
		return nil, "", fmt.Errorf("polly tts: read audio: %w", err)
	}

	return audio, "audio/mpeg", nil
}

func (p *PollyTTS) SynthesizeStream(ctx context.Context, text string, opts TTSOptions) (<-chan AudioChunk, error) {
	audio, _, err := p.Synthesize(ctx, text, opts)
	if err != nil {
		return nil, err
	}

	ch := make(chan AudioChunk, 1)
	ch <- AudioChunk{Data: audio}
	close(ch)
	return ch, nil
}

func (p *PollyTTS) ListVoices(_ context.Context) ([]Voice, error) {
	names := p.voices
	if len(names) == 0 {
		names = pollyNeuralVoices
	}
	return voicesFromList(names), nil
}

func (p *PollyTTS) GetSample(ctx context.Context, voiceID string, text string) ([]byte, string, error) {
	if text == "" {
		text = "Hello! This is a sample of my voice. How can I help you today?"
	}
	return p.Synthesize(ctx, text, TTSOptions{VoiceID: voiceID})
}
