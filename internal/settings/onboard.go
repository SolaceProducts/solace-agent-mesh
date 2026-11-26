// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

func RunOnboarding(isDesktop bool) (*Settings, error) {
	fmt.Println()
	fmt.Println("  Welcome to Solace Agent Mesh!")
	fmt.Println("  Let's set up your AI connection.")
	fmt.Println()

	candidates := DetectFromEnv()
	dotenvCandidates := DetectFromDotEnv(".env")
	candidates = append(candidates, dotenvCandidates...)
	candidates = dedup(candidates)

	var selected Candidate
	var err error

	if len(candidates) > 0 {
		selected, err = promptSelectKey(candidates)
	} else {
		selected, err = promptPasteKey()
	}
	if err != nil {
		return nil, err
	}

	if reviewErr := promptReviewConfig(&selected); reviewErr != nil {
		return nil, reviewErr
	}

	model, err := promptSelectModel(selected)
	if err != nil {
		return nil, err
	}

	if testErr := promptTestConnection(selected, model); testErr != nil {
		return nil, testErr
	}

	saveDir, err := promptSaveLocation(isDesktop)
	if err != nil {
		return nil, err
	}

	s := &Settings{
		LLM: LLMSettings{
			Provider:     selected.Provider,
			DefaultModel: model,
			BaseURL:      selected.BaseURL,
		},
	}
	if selected.EnvVarName != "" {
		s.LLM.APIKeyEnv = selected.EnvVarName
	} else {
		s.LLM.APIKey = selected.Key
	}

	if err := Save(s, saveDir); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}

	fmt.Printf("\n  Settings saved to %s/settings.yaml\n\n", saveDir)
	return s, nil
}

func promptSelectKey(candidates []Candidate) (Candidate, error) {
	if len(candidates) == 1 {
		c := candidates[0]
		var useIt bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Found %s key: %s (%s)", c.Provider, c.EnvVarName, c.MaskedKey)).
					Description("Use this key?").
					Value(&useIt),
			),
		)
		if err := form.Run(); err != nil {
			return Candidate{}, err
		}
		if useIt {
			return c, nil
		}
		return promptPasteKey()
	}

	options := make([]huh.Option[int], len(candidates))
	for i, c := range candidates {
		label := fmt.Sprintf("%s — %s (%s)", c.Provider, c.EnvVarName, c.MaskedKey)
		options[i] = huh.NewOption(label, i)
	}
	options = append(options, huh.NewOption("Enter a different key", -1))

	var choice int
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Found API keys in your environment").
				Description("Which one should SAM use?").
				Options(options...).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		return Candidate{}, err
	}

	if choice == -1 {
		return promptPasteKey()
	}
	return candidates[choice], nil
}

func promptPasteKey() (Candidate, error) {
	for {
		var input string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title("Paste your API key, URL, or config snippet").
					Description("We'll figure out the provider and key automatically.\nExamples: sk-proj-..., OPENAI_API_KEY=sk-..., or a curl command.").
					Value(&input),
			),
		)
		if err := form.Run(); err != nil {
			return Candidate{}, err
		}

		c, err := ParsePastedInput(input)
		if err != nil {
			fmt.Printf("\n  %s\n  Please try again.\n\n", err)
			continue
		}
		return *c, nil
	}
}

func promptReviewConfig(c *Candidate) error {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("API type").
				Description("The LLM API protocol (e.g. openai, anthropic, google). Use \"openai\" for OpenAI-compatible services.").
				Value(&c.Provider),
			huh.NewInput().
				Title("Base URL (empty = provider default)").
				Value(&c.BaseURL),
		),
	)
	return form.Run()
}

func promptSelectModel(c Candidate) (string, error) {
	defaultModel := DefaultModelForProvider(c.Provider)

	ctx := context.Background()
	models, _ := ListModels(ctx, c.BaseURL, c.Key, c.Provider)

	if len(models) > 0 {
		models = RankModels(models, c.Provider)

		if len(models) > 10 {
			models = models[:10]
		}

		options := make([]huh.Option[string], len(models))
		for i, m := range models {
			label := m
			if i == 0 {
				label += " (recommended)"
			}
			options[i] = huh.NewOption(label, m)
		}
		options = append(options, huh.NewOption("Enter model name manually", "__manual__"))

		var choice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select a model").
					Options(options...).
					Value(&choice),
			),
		)
		if err := form.Run(); err != nil {
			return "", err
		}

		if choice != "__manual__" {
			return choice, nil
		}
	}

	model := defaultModel
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Model name").
				Description(fmt.Sprintf("Default: %s", defaultModel)).
				Value(&model),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	if model == "" {
		model = defaultModel
	}
	return model, nil
}

func displayBaseURL(baseURL string) string {
	if baseURL == "" {
		return "(unset — set base_url before testing)"
	}
	return baseURL
}

func promptTestConnection(c Candidate, model string) error {
	endpoint := displayBaseURL(c.BaseURL)

	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Test connection?").
				Description(fmt.Sprintf("Will send a small request to: %s\nUsing model: %s", endpoint, model)).
				Value(&confirm),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	if !confirm {
		fmt.Println("  Skipping connection test.")
		return nil
	}

	fmt.Println("  Testing connection...")

	key := c.Key
	if key == "" && c.EnvVarName != "" {
		key = os.Getenv(c.EnvVarName)
	}

	ctx := context.Background()
	if err := TestConnection(ctx, c.BaseURL, key, c.Provider, model); err != nil {
		fmt.Printf("\n  Connection test failed: %s\n", err)

		var retry bool
		retryForm := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Continue anyway?").
					Description("You can fix the configuration later in settings.yaml").
					Value(&retry),
			),
		)
		if retryErr := retryForm.Run(); retryErr != nil {
			return retryErr
		}
		if !retry {
			return fmt.Errorf("connection test failed and user chose not to continue")
		}
		return nil
	}

	fmt.Println("  Connection successful!")
	return nil
}

func promptSaveLocation(isDesktop bool) (string, error) {
	if isDesktop {
		dir, err := DefaultGlobalDir()
		if err != nil {
			return "", err
		}
		return dir, nil
	}

	globalDir, err := DefaultGlobalDir()
	if err != nil {
		return "", err
	}
	projectDir := DefaultProjectDir()

	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Where should SAM store this configuration?").
				Description("Project settings override global settings.").
				Options(
					huh.NewOption(fmt.Sprintf("Global — %s (recommended)", globalDir), globalDir),
					huh.NewOption(fmt.Sprintf("This project — %s", projectDir), projectDir),
				).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return choice, nil
}

func dedup(candidates []Candidate) []Candidate {
	seen := make(map[string]bool)
	var result []Candidate
	for _, c := range candidates {
		key := c.EnvVarName
		if key == "" {
			key = c.Key
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, c)
	}
	return result
}

func SettingsSummary(s *Settings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  API type: %s\n", s.LLM.Provider)
	fmt.Fprintf(&b, "  Model:    %s\n", s.LLM.DefaultModel)
	if s.LLM.APIKeyEnv != "" {
		fmt.Fprintf(&b, "  Key:      $%s\n", s.LLM.APIKeyEnv)
	} else if s.LLM.APIKey != "" {
		fmt.Fprintf(&b, "  Key:      %s\n", MaskKey(s.LLM.APIKey))
	}
	if s.LLM.BaseURL != "" {
		fmt.Fprintf(&b, "  URL:      %s\n", s.LLM.BaseURL)
	}
	return b.String()
}
