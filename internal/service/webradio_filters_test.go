package service

import (
	"testing"

	"github.com/freetetra/server/internal/config"
)

func TestBuildWebRadioFilterChain_DefaultsMatchLegacy(t *testing.T) {
	// The legacy hardcoded chain was:
	//   volume=-14dB,acompressor=threshold=-20dB:ratio=4:attack=5:release=50
	// With config defaults loaded by LoadFromEnv (VolumeDB=-14, default
	// Compressor) the builder must reproduce it byte-for-byte so the audio
	// pipeline stays identical until a later task knowingly changes it.
	cfg := config.WebRadioConfig{
		VolumeDB:   -14,
		Compressor: "acompressor=threshold=-20dB:ratio=4:attack=5:release=50",
	}
	got := BuildWebRadioFilterChain(cfg)
	want := "volume=-14dB,acompressor=threshold=-20dB:ratio=4:attack=5:release=50"
	if got != want {
		t.Errorf("default chain mismatch:\n got = %q\nwant = %q", got, want)
	}
}

func TestBuildWebRadioFilterChain_EmptyConfigYieldsEmptyString(t *testing.T) {
	got := BuildWebRadioFilterChain(config.WebRadioConfig{})
	if got != "" {
		t.Errorf("empty config produced %q, want empty string", got)
	}
}

func TestBuildWebRadioFilterChain_VolumeOmittedAtZero(t *testing.T) {
	cfg := config.WebRadioConfig{Compressor: "acompressor=ratio=2"}
	got := BuildWebRadioFilterChain(cfg)
	want := "acompressor=ratio=2"
	if got != want {
		t.Errorf("zero VolumeDB should omit volume=; got %q want %q", got, want)
	}
}

func TestBuildWebRadioFilterChain_ExtraFiltersAppendedLast(t *testing.T) {
	cfg := config.WebRadioConfig{
		VolumeDB:     -10,
		Compressor:   "acompressor=ratio=3",
		ExtraFilters: "aecho=0.8:0.9:1000:0.3",
	}
	got := BuildWebRadioFilterChain(cfg)
	want := "volume=-10dB,acompressor=ratio=3,aecho=0.8:0.9:1000:0.3"
	if got != want {
		t.Errorf("extra filters position wrong:\n got = %q\nwant = %q", got, want)
	}
}

func TestBuildWebRadioFilterChain_CompressorOptional(t *testing.T) {
	cfg := config.WebRadioConfig{VolumeDB: -6}
	got := BuildWebRadioFilterChain(cfg)
	want := "volume=-6dB"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}
