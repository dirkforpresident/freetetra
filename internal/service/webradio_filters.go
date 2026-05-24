package service

import (
	"fmt"
	"strings"

	"github.com/freetetra/server/internal/config"
)

// filterChain accumulates ffmpeg `-af` filter expressions in order. Empty
// entries are skipped so optional stages can `add` unconditionally.
type filterChain struct{ parts []string }

func (f *filterChain) add(expr string) {
	if e := strings.TrimSpace(expr); e != "" {
		f.parts = append(f.parts, e)
	}
}

func (f *filterChain) String() string {
	return strings.Join(f.parts, ",")
}

// BuildWebRadioFilterChain returns the value passed to ffmpeg's `-af` flag.
// The chain is intentionally linear and order-sensitive: volume → dynamics
// → extra filters. Each stage is independently togglable from config so
// later phases (HPF/LPF, loudnorm, limiter) can slot in without touching
// callers.
func BuildWebRadioFilterChain(cfg config.WebRadioConfig) string {
	fc := &filterChain{}
	if cfg.VolumeDB != 0 {
		fc.add(fmt.Sprintf("volume=%gdB", cfg.VolumeDB))
	}
	fc.add(cfg.Compressor)
	fc.add(cfg.ExtraFilters)
	return fc.String()
}
