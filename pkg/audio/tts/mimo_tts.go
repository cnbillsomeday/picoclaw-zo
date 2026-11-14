package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

type MimoTTSProvider struct {
	apiKey     string
	apiBase    string
	voice      string
	format     string
	model      string
	httpClient *http.Client
}

func NewMimoTTSProvider(apiKey string, apiBase string, model string, proxyURL string) *MimoTTSProvider {
	return NewMimoTTSProviderWithVoice(apiKey, apiBase, model, proxyURL, "")
}

// NewMimoTTSProviderWithVoice builds a MiMo TTS provider with an optional
// voice override. MiMo's official API rejects the historic default_zh voice
// and instead accepts names such as "mimo_default", "冰糖", "茉莉", "苏打".
// Passing a non-empty voice here lets configuration pick a valid one.
func NewMimoTTSProviderWithVoice(apiKey string, apiBase string, model string, proxyURL string, voice string) *MimoTTSProvider {
	if apiBase == "" {
		apiBase = "https://api.xiaomimimo.com/v1/chat/completions"
	} else {
		if u, err := url.Parse(apiBase); err == nil && u.Scheme != "" && u.Host != "" {
			path := u.Path
			if u.Host == "api.xiaomimimo.com" {
				if path == "" || path == "/" || path == "/v1" || path == "/v1/" {
					path = "/v1/chat/completions"
				} else {
					if !strings.HasPrefix(path, "/") {
						path = "/" + path
					}
					if !strings.HasPrefix(path, "/v1/") {
						path = "/v1" + strings.TrimSuffix(path, "/")
					}
					if !strings.HasSuffix(path, "/chat/completions") {
						path = strings.TrimSuffix(path, "/") + "/chat/completions"
					}
				}
			} else {
				if !strings.HasSuffix(path, "/chat/completions") {
					path = strings.TrimSuffix(path, "/") + "/chat/completions"
				}
			}
			u.Path = path
			apiBase = u.String()
		} else {
			if apiBase == "https://api.xiaomimimo.com/v1" {
				apiBase = "https://api.xiaomimimo.com/v1/chat/completions"
			} else if !strings.HasSuffix(apiBase, "/chat/completions") {
				apiBase = strings.TrimSuffix(apiBase, "/") + "/chat/completions"
			}
		}
	}

	model = strings.TrimSpace(model)
	if model == "" {
		model = "mimo-v2-tts"
	}
	if voice == "" {
		voice = "default_zh" // historic default; override with a valid MiMo voice via config
	}

	client := &http.Client{Timeout: 60 * time.Second}
	if proxyURL != "" {
		if pURL, err := url.Parse(proxyURL); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(pURL)}
		} else {
			logger.WarnF(
				"NewMimoTTSProvider: invalid proxy URL; proceeding without proxy",
				map[string]any{"proxyURL": proxyURL, "error": err},
			)
		}
	}

	return &MimoTTSProvider{
		apiKey:     apiKey,
		apiBase:    apiBase,
		voice:      voice,
		format:     "mp3",
		model:      model,
		httpClient: client,
	}
}

func (t *MimoTTSProvider) Name() string {
	return "mimo-tts"
}

func (t *MimoTTSProvider) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	logger.DebugCF("voice-tts", "Starting TTS synthesis", map[string]any{"text_len": len(text), "provider": t.Name()})

	reqBody := map[string]any{
		"model": t.model,
		"messages": []map[string]string{
			{"role": "assistant", "content": text},
		},
		"audio": map[string]string{
			"format": t.format,
			"voice":  t.voice,
		},
		"stream": false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.apiBase, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Choices []struct {
			Message struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(payload.Choices) == 0 || payload.Choices[0].Message.Audio.Data == "" {
		return nil, fmt.Errorf("invalid TTS response: missing audio data")
	}

	audioBytes, err := base64.StdEncoding.DecodeString(payload.Choices[0].Message.Audio.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode audio data: %w", err)
	}

	return io.NopCloser(bytes.NewReader(audioBytes)), nil
}
