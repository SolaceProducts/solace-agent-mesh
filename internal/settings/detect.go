// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Candidate struct {
	Provider      string `json:"provider"`
	EnvVarName    string `json:"envVarName"`
	Key           string `json:"key"`
	MaskedKey     string `json:"maskedKey"`
	BaseURL       string `json:"baseURL"`
	BaseURLEnvVar string `json:"baseURLEnvVar"`
}

type knownKeyEntry struct {
	provider       string
	endpointVar    string
	defaultBaseURL string
}

var knownKeyEnvVars = map[string]knownKeyEntry{
	"OPENAI_API_KEY":       {provider: "openai", endpointVar: "OPENAI_API_BASE"},
	"ANTHROPIC_API_KEY":    {provider: "anthropic"},
	"GEMINI_API_KEY":       {provider: "google"},
	"GOOGLE_API_KEY":       {provider: "google"},
	"MISTRAL_API_KEY":      {provider: "mistral"},
	"GROQ_API_KEY":         {provider: "groq"},
	"TOGETHER_API_KEY":     {provider: "openai", defaultBaseURL: "https://api.together.xyz"},
	"DEEPSEEK_API_KEY":     {provider: "openai", defaultBaseURL: "https://api.deepseek.com"},
	"AZURE_OPENAI_API_KEY": {provider: "azure", endpointVar: "AZURE_OPENAI_ENDPOINT"},
	"COHERE_API_KEY":       {provider: "cohere"},
	"PERPLEXITYAI_API_KEY": {provider: "perplexity"},
	"CEREBRAS_API_KEY":     {provider: "cerebras"},
	"OPENROUTER_API_KEY":   {provider: "openrouter"},
	"HF_TOKEN":             {provider: "huggingface"},
	"NEBIUS_API_KEY":       {provider: "nebius"},
	"XAI_API_KEY":          {provider: "xai"},
	"REPLICATE_API_KEY":    {provider: "replicate"},
}

var knownBaseURLEnvVars = []string{
	"OPENAI_API_BASE",
	"OPENAI_BASE_URL",
	"LLM_SERVICE_ENDPOINT",
	"LITELLM_API_BASE",
	"LITELLM_PROXY_URL",
	"OLLAMA_HOST",
}

var keyPrefixes = []struct {
	prefix   string
	provider string
}{
	{"sk-ant-", "anthropic"},
	{"sk-proj-", "openai"},
	{"sk-", "openai"},
	{"AIza", "google"},
	{"gsk_", "groq"},
	{"dsk-", "openai"},
	{"mis-", "mistral"},
	{"xai-", "xai"},
	{"pplx-", "perplexity"},
	{"hf_", "huggingface"},
	{"r8_", "replicate"},
}

func DetectFromEnv() []Candidate {
	var candidates []Candidate

	var detectedBaseURL string
	var detectedBaseURLEnvVar string
	for _, envVar := range knownBaseURLEnvVars {
		if v := os.Getenv(envVar); v != "" {
			detectedBaseURL = normalizeBaseURL(v)
			detectedBaseURLEnvVar = envVar
			break
		}
	}

	for envVar, entry := range knownKeyEnvVars {
		key := os.Getenv(envVar)
		if key == "" {
			continue
		}
		baseURL := ""
		baseURLEnvVar := ""
		if entry.endpointVar != "" {
			if v := os.Getenv(entry.endpointVar); v != "" {
				baseURL = normalizeBaseURL(v)
				baseURLEnvVar = entry.endpointVar
			}
		}
		if baseURL == "" && entry.defaultBaseURL != "" {
			baseURL = entry.defaultBaseURL
		}
		if baseURL == "" && !providerHasNativeAPI(entry.provider) {
			baseURL = detectedBaseURL
			baseURLEnvVar = detectedBaseURLEnvVar
		}
		candidates = append(candidates, Candidate{
			Provider:      entry.provider,
			EnvVarName:    envVar,
			Key:           key,
			MaskedKey:     MaskKey(key),
			BaseURL:       baseURL,
			BaseURLEnvVar: baseURLEnvVar,
		})
	}

	return candidates
}

func DetectFromDotEnv(path string) []Candidate {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		vars[key] = val
	}

	var detectedBaseURL string
	var detectedBaseURLEnvVar string
	for _, envVar := range knownBaseURLEnvVars {
		if v, ok := vars[envVar]; ok && v != "" {
			detectedBaseURL = normalizeBaseURL(v)
			detectedBaseURLEnvVar = envVar
			break
		}
	}

	var candidates []Candidate
	for envVar, entry := range knownKeyEnvVars {
		val, ok := vars[envVar]
		if !ok || val == "" {
			continue
		}
		if os.Getenv(envVar) != "" {
			continue
		}
		baseURL := ""
		baseURLEnvVar := ""
		if entry.endpointVar != "" {
			if v, ok := vars[entry.endpointVar]; ok && v != "" {
				baseURL = normalizeBaseURL(v)
				baseURLEnvVar = entry.endpointVar
			}
		}
		if baseURL == "" && entry.defaultBaseURL != "" {
			baseURL = entry.defaultBaseURL
		}
		if baseURL == "" && !providerHasNativeAPI(entry.provider) {
			baseURL = detectedBaseURL
			baseURLEnvVar = detectedBaseURLEnvVar
		}
		candidates = append(candidates, Candidate{
			Provider:      entry.provider,
			EnvVarName:    envVar,
			Key:           val,
			MaskedKey:     MaskKey(val),
			BaseURL:       baseURL,
			BaseURLEnvVar: baseURLEnvVar,
		})
	}

	return candidates
}

var urlRegex = regexp.MustCompile(`https?://[^\s"'\` + "`" + `]+`)

var tokenRegex = regexp.MustCompile(`[A-Za-z0-9_-]{20,}`)

func ParsePastedInput(input string) (*Candidate, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("no input provided")
	}

	var foundKey string
	var foundURL string
	var foundProvider string

	if urls := urlRegex.FindAllString(input, -1); len(urls) > 0 {
		foundURL = normalizeBaseURL(urls[0])
	}

	for envVar, entry := range knownKeyEnvVars {
		patterns := []string{
			envVar + "=",
			envVar + " =",
			envVar + `="`,
			envVar + `='`,
		}
		for _, pat := range patterns {
			if idx := strings.Index(input, pat); idx >= 0 {
				rest := input[idx+len(envVar)+1:]
				rest = strings.TrimLeft(rest, "= ")
				rest = strings.Trim(rest, `"'`)
				rest = strings.Fields(rest)[0]
				if len(rest) >= 10 {
					foundKey = rest
					foundProvider = entry.provider
					break
				}
			}
		}
		if foundKey != "" {
			break
		}
	}

	if foundKey == "" {
		if idx := strings.Index(input, "Bearer "); idx >= 0 {
			rest := input[idx+7:]
			rest = strings.Trim(rest, `"'`)
			if fields := strings.Fields(rest); len(fields) > 0 && len(fields[0]) >= 10 {
				foundKey = fields[0]
			}
		}
	}

	if foundKey == "" {
		for _, pat := range []string{`"api_key"`, `api_key:`, `"apiKey"`, `apiKey:`} {
			if idx := strings.Index(input, pat); idx >= 0 {
				rest := input[idx+len(pat):]
				rest = strings.TrimLeft(rest, `: "'`)
				if fields := strings.Fields(rest); len(fields) > 0 {
					token := strings.Trim(fields[0], `"',}`)
					if len(token) >= 10 {
						foundKey = token
						break
					}
				}
			}
		}
	}

	if foundKey == "" {
		tokens := tokenRegex.FindAllString(input, -1)
		for _, tok := range tokens {
			if strings.HasPrefix(tok, "http") {
				continue
			}
			foundKey = tok
			break
		}
	}

	if foundKey == "" {
		return nil, fmt.Errorf("no API key found in input, keys typically look like sk-, AIza-, or gsk_")
	}

	if foundProvider == "" {
		foundProvider = providerFromKeyPrefix(foundKey)
	}

	return &Candidate{
		Provider:  foundProvider,
		Key:       foundKey,
		MaskedKey: MaskKey(foundKey),
		BaseURL:   foundURL,
	}, nil
}

func providerFromKeyPrefix(key string) string {
	for _, kp := range keyPrefixes {
		if strings.HasPrefix(key, kp.prefix) {
			return kp.provider
		}
	}
	return "openai"
}

func MaskKey(key string) string {
	if len(key) <= 8 {
		return key[:2] + "******"
	}
	return key[:6] + "******"
}

func DefaultModelForProvider(provider string) string {
	switch provider {
	case "openai":
		return "gpt-5.6-sol"
	case "anthropic":
		return "claude-sonnet-5"
	case "google":
		return "gemini-3.6-flash"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "mistral":
		return "mistral-large-latest"
	case "azure":
		return "gpt-4o"
	case "cohere":
		return "command-r-plus"
	case "perplexity":
		return "sonar-pro"
	case "cerebras":
		return "llama-3.3-70b"
	case "openrouter":
		return "openai/gpt-4o"
	case "huggingface":
		return "meta-llama/Llama-3.1-70B-Instruct"
	case "xai":
		return "grok-2"
	case "replicate":
		return "meta/llama-3.1-405b-instruct"
	case "nebius":
		return "meta-llama/Llama-3.1-70B-Instruct"
	default:
		return "gpt-4o"
	}
}

func providerHasNativeAPI(provider string) bool {
	switch provider {
	case "anthropic", "google", "cohere", "bedrock", "vertex",
		"mistral", "groq", "perplexity", "cerebras", "openrouter",
		"huggingface", "nebius", "xai", "replicate":
		return true
	default:
		return false
	}
}

func normalizeBaseURL(u string) string {
	u = strings.TrimRight(u, "/")
	for _, suffix := range []string{
		"/v1/chat/completions",
		"/v1/models",
		"/v1/embeddings",
		"/v1",
	} {
		if strings.HasSuffix(u, suffix) {
			u = strings.TrimSuffix(u, suffix)
			break
		}
	}
	return u
}
