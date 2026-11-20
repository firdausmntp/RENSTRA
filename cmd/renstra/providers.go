package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/anthropic"
	"renstra/internal/provider/google"
	"renstra/internal/provider/groq"
	"renstra/internal/provider/mistral"
	"renstra/internal/provider/ollama"
	"renstra/internal/provider/openai"
	"renstra/internal/provider/openrouter"
	"renstra/internal/provider/xai"
	"renstra/internal/tool"
)

func buildProvider(pcfg config.ProviderConfig) (provider.Provider, error) {
	apiKey := pcfg.APIKey
	if pcfg.APIKeyEnv != "" {
		if envVal := os.Getenv(pcfg.APIKeyEnv); envVal != "" {
			apiKey = envVal
		}
	}
	switch pcfg.Kind {
	case "", "openai", "custom":
		return openai.New(pcfg, apiKey)
	case "anthropic":
		return anthropic.New(pcfg, apiKey)
	case "openrouter":
		return openrouter.New(pcfg, apiKey)
	case "groq":
		return groq.New(pcfg, apiKey)
	case "ollama":
		return ollama.New(pcfg, apiKey)
	case "mistral":
		return mistral.New(pcfg, apiKey)
	case "xai":
		return xai.New(pcfg, apiKey)
	case "google":
		return google.New(pcfg, apiKey)
	default:
		return nil, fmt.Errorf("provider kind %q tidak dikenal", pcfg.Kind)
	}
}

func buildFallbacks(cfg *config.Config) []agent.FallbackTarget {
	var targets []agent.FallbackTarget
	for _, ref := range cfg.Fallbacks {
		pcfg, model, err := config.Resolve(ref, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peringatan fallback %q tidak valid: %v\n", ref, err)
			continue
		}
		prov, err := buildProvider(pcfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peringatan build provider fallback %q: %v\n", ref, err)
			continue
		}
		mc := pcfg.ModelConfig(model)
		targets = append(targets, agent.FallbackTarget{
			Provider:      prov,
			Model:         model,
			MaxTokens:     mc.MaxOutput,
			PriceIn:       mc.PriceIn,
			PriceOut:      mc.PriceOut,
			ContextWindow: mc.Context,
			Vision:        mc.Vision,
			ToolsDisabled: mc.Tools != nil && !*mc.Tools,
		})
	}
	return targets
}

func resolveAuxTarget(cfg *config.Config, primaryProv provider.Provider, primaryModel string, primaryMC config.ModelConfig) (provider.Provider, string, float64, float64, error) {
	if cfg == nil || strings.TrimSpace(cfg.Agent.AuxModel) == "" {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, nil
	}
	auxPCfg, auxModel, err := config.Resolve(cfg.Agent.AuxModel, cfg)
	if err != nil {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, err
	}
	auxProv, err := buildProvider(auxPCfg)
	if err != nil {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, err
	}
	auxMC := auxPCfg.ModelConfig(auxModel)
	return auxProv, auxModel, auxMC.PriceIn, auxMC.PriceOut, nil
}

func pruneRetention(cfg *config.Config, home, workspace string) {
	days := 14
	if cfg != nil && cfg.Agent.FileRetentionDays > 0 {
		days = cfg.Agent.FileRetentionDays
	} else if cfg != nil && cfg.Agent.FileRetentionDays < 0 {
		return
	}
	maxAge := time.Duration(days) * 24 * time.Hour
	for _, dir := range []string{
		filepath.Join(workspace, "inbox"),
		filepath.Join(workspace, "tmp"),
		filepath.Join(workspace, ".tool-output"),
		filepath.Join(home, "checkpoints"),
	} {
		_, _ = tool.PruneOldFiles(dir, maxAge)
	}
}

func buildTranscriber(cfg *config.Config) func(context.Context, string) (string, error) {
	if cfg == nil {
		return nil
	}
	var baseURL, apiKey, model string
	for _, p := range cfg.Providers {
		kind := strings.ToLower(strings.TrimSpace(p.Kind))
		if kind != "openai" && kind != "groq" && kind != "" {
			continue
		}
		key := p.APIKey
		if p.APIKeyEnv != "" {
			if envVal := os.Getenv(p.APIKeyEnv); envVal != "" {
				key = envVal
			}
		}
		if key == "" && kind == "openai" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" && kind == "groq" {
			key = os.Getenv("GROQ_API_KEY")
		}
		if key == "" {
			continue
		}
		baseURL = strings.TrimRight(p.BaseURL, "/")
		if baseURL == "" {
			if kind == "groq" {
				baseURL = "https://api.groq.com/openai/v1"
			} else {
				baseURL = "https://api.openai.com/v1"
			}
		}
		apiKey = key
		if kind == "groq" {
			model = "whisper-large-v3-turbo"
		} else {
			model = "whisper-1"
		}
		break
	}
	if apiKey == "" {
		return nil
	}
	return func(ctx context.Context, audioPath string) (string, error) {
		return transcribeAudioFile(ctx, baseURL, apiKey, model, audioPath)
	}
}

func transcribeAudioFile(ctx context.Context, baseURL, apiKey, model, audioPath string) (string, error) {
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("transcribe HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}
