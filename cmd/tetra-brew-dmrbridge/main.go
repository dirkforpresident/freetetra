// tetra-brew-dmrbridge connects to FreeTetra and to the BrandMeister-facing
// TetraPack Brew core simultaneously, mirroring group-call traffic between
// them for a configured set of talkgroups.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/freetetra/server/internal/config"
	"github.com/freetetra/server/internal/dmrbridge"
	"github.com/freetetra/server/internal/service"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Fatalf("base config: %v", err)
	}

	ftConn := config.BrewClientConfig{
		BaseURL:          envStr("DMRBRIDGE_FT_BASE_URL", "http://127.0.0.1:8091"),
		Path:             envStr("DMRBRIDGE_FT_PATH", "/brew"),
		Username:         envStr("DMRBRIDGE_FT_USER", ""),
		Password:         envStr("DMRBRIDGE_FT_PASS", ""),
		ReconnectDelay:   15 * time.Second,
		DiscoveryTimeout: 10 * time.Second,
		WriteTimeout:     5 * time.Second,
	}
	bmConn := config.BrewClientConfig{
		BaseURL:          envStr("DMRBRIDGE_BM_BASE_URL", "https://core.tetrapack.online:443"),
		Path:             envStr("DMRBRIDGE_BM_PATH", "/brew"),
		Username:         envStr("DMRBRIDGE_BM_USER", ""),
		Password:         envStr("DMRBRIDGE_BM_PASS", ""),
		ReconnectDelay:   15 * time.Second,
		DiscoveryTimeout: 10 * time.Second,
		WriteTimeout:     5 * time.Second,
	}

	if ftConn.Username == "" || bmConn.Username == "" {
		logger.Fatalf("DMRBRIDGE_FT_USER and DMRBRIDGE_BM_USER must be set")
	}

	ftISSI := envUint("DMRBRIDGE_FT_ISSI", 905001)
	bmISSI := envUint("DMRBRIDGE_BM_ISSI", 0)
	if bmISSI == 0 {
		// Default: derive from BM username (digits only).
		bmISSI = uint32Atoi(bmConn.Username)
	}

	tgs := parseTGs(envStr("DMRBRIDGE_TGS", ""))
	if len(tgs) == 0 {
		logger.Fatalf("DMRBRIDGE_TGS must list at least one talkgroup (comma-separated)")
	}

	ftPlane := service.NewBrewModulePlane(cfg, logger, ftISSI, tgs).
		WithClient(ftConn).WithLabel("FT")
	bmPlane := service.NewBrewModulePlane(cfg, logger, bmISSI, tgs).
		WithClient(bmConn).WithLabel("BM")

	// Optional source overrides for outbound calls. 0 = pass through original.
	// Set DMRBRIDGE_BM_SOURCE_OVERRIDE if BrandMeister rejects calls because
	// the radio's TETRA ISSI doesn't match a registered hotspot ID.
	bmSourceOverride := envUint("DMRBRIDGE_BM_SOURCE_OVERRIDE", 0)
	ftSourceOverride := envUint("DMRBRIDGE_FT_SOURCE_OVERRIDE", 0)

	bridge := dmrbridge.New(logger, ftPlane, bmPlane, ftSourceOverride, bmSourceOverride, tgs)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Printf("dmrbridge: FT=%s as %d (auth %s, override=%d), BM=%s as %d (auth %s, override=%d), TGs=%v",
		ftConn.BaseURL, ftISSI, ftConn.Username, ftSourceOverride,
		bmConn.BaseURL, bmISSI, bmConn.Username, bmSourceOverride, tgs)

	if err := bridge.Start(ctx); err != nil {
		logger.Fatalf("bridge: %v", err)
	}
}

func envStr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envUint(k string, def uint32) uint32 {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return def
	}
	return uint32(n)
}

func uint32Atoi(s string) uint32 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

func parseTGs(s string) []uint32 {
	out := []uint32{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err == nil && n > 0 {
			out = append(out, uint32(n))
		}
	}
	return out
}
