package config

import "testing"

func TestVoiceConfigNormalizedDefaultMode(t *testing.T) {
	cases := map[string]string{
		"":      "off",
		"off":   "off",
		"on":    "on",
		"ON":    "on",
		" tts ": "tts",
		"bogus": "off",
	}
	for in, want := range cases {
		if got := (VoiceConfig{DefaultMode: in}).NormalizedDefaultMode(); got != want {
			t.Errorf("NormalizedDefaultMode(%q) = %q, want %q", in, got, want)
		}
	}
}
