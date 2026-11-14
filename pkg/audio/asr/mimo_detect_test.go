package asr

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/audio/tts"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMimoVoiceDetection(t *testing.T) {
	cfg := &config.Config{
		ModelList: []*config.ModelConfig{
			{
				ModelName: "mimo-tts",
				Provider:  "mimo",
				Model:     "mimo-v2.5-tts",
				APIBase:   "https://api.xiaomimimo.com/v1",
				ExtraBody: map[string]any{"voice": "mimo_default"},
				APIKeys:   config.SimpleSecureStrings("sk-test"),
			},
			{
				ModelName: "mimo-asr",
				Provider:  "mimo",
				Model:     "mimo-v2.5-asr",
				APIBase:   "https://api.xiaomimimo.com/v1",
				APIKeys:   config.SimpleSecureStrings("sk-test"),
			},
		},
		Voice: config.VoiceConfig{
			ModelName:    "mimo-asr",
			TTSModelName: "mimo-tts",
		},
	}

	tr := DetectTranscriber(cfg)
	if tr == nil {
		t.Fatalf("DetectTranscriber returned nil for mimo-asr config")
	}
	t.Logf("transcriber: %s", tr.Name())

	tp := tts.DetectTTS(cfg)
	if tp == nil {
		t.Fatalf("DetectTTS returned nil for mimo-tts config")
	}
	t.Logf("tts provider: %s", tp.Name())
}
