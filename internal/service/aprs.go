package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// APRSBridge sends position reports from TETRA LIP to APRS-IS.
type APRSBridge struct {
	logger   *log.Logger
	callsign string // Login callsign for APRS-IS
	passcode string // APRS-IS passcode
	server   string // APRS-IS server (e.g. euro.aprs2.net:14580)

	mu       sync.Mutex
	conn     net.Conn
	callCache map[uint32]string // ISSI -> callsign cache
	cacheTTL  map[uint32]time.Time
}

const (
	aprsReconnectDelay = 30 * time.Second
	aprsCacheTTL       = 24 * time.Hour
	aprsISServer       = "euro.aprs2.net:14580"
)

func newAPRSBridge(logger *log.Logger, callsign, passcode string) *APRSBridge {
	server := aprsISServer
	return &APRSBridge{
		logger:    logger,
		callsign:  callsign,
		passcode:  passcode,
		server:    server,
		callCache: make(map[uint32]string),
		cacheTTL:  make(map[uint32]time.Time),
	}
}

// connect establishes a connection to APRS-IS.
func (a *APRSBridge) connect() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		a.conn.Close()
	}

	a.logger.Printf("APRS-IS: connecting to %s as %s", a.server, a.callsign)

	conn, err := net.DialTimeout("tcp", a.server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("APRS-IS connect: %w", err)
	}

	// Read server banner
	reader := bufio.NewReader(conn)
	banner, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("APRS-IS banner: %w", err)
	}
	a.logger.Printf("APRS-IS: %s", strings.TrimSpace(banner))

	// Login
	login := fmt.Sprintf("user %s pass %s vers FreeTetra 1.0 filter r/53.5/10/500\r\n",
		a.callsign, a.passcode)
	_, err = conn.Write([]byte(login))
	if err != nil {
		conn.Close()
		return fmt.Errorf("APRS-IS login: %w", err)
	}

	// Read login response
	response, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("APRS-IS login response: %w", err)
	}
	a.logger.Printf("APRS-IS: %s", strings.TrimSpace(response))

	if strings.Contains(response, "unverified") {
		conn.Close()
		return fmt.Errorf("APRS-IS: login unverified (wrong passcode?)")
	}

	a.conn = conn
	a.logger.Printf("APRS-IS: connected and verified")

	// Start keepalive reader in background
	go func() {
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					a.logger.Printf("APRS-IS: read error: %v", err)
				}
				a.mu.Lock()
				a.conn = nil
				a.mu.Unlock()
				return
			}
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				a.logger.Printf("APRS-IS rx: %s", line)
			}
		}
	}()

	return nil
}

// ensureConnected connects if not already connected.
func (a *APRSBridge) ensureConnected() error {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	if connected {
		return nil
	}
	return a.connect()
}

// SendPosition sends an APRS position report for a TETRA subscriber.
func (a *APRSBridge) SendPosition(issi uint32, lat, lon float64) {
	// Look up callsign from RadioID
	call := a.lookupCallsign(issi)
	if call == "" {
		a.logger.Printf("APRS-IS: no callsign found for ISSI %d, skipping", issi)
		return
	}

	if err := a.ensureConnected(); err != nil {
		a.logger.Printf("APRS-IS: %v", err)
		return
	}

	// Build APRS position packet
	// Format: CALLSIGN>APTETR,TCPIP*:!DDMM.MMN/DDDMM.MME-TETRA ISSI:XXXXXXX via FreeTetra
	latDeg := int(lat)
	latMin := (lat - float64(latDeg)) * 60.0
	latDir := "N"
	if lat < 0 {
		latDeg = -latDeg
		latMin = -latMin
		latDir = "S"
	}

	lonDeg := int(lon)
	lonMin := (lon - float64(lonDeg)) * 60.0
	lonDir := "E"
	if lon < 0 {
		lonDeg = -lonDeg
		lonMin = -lonMin
		lonDir = "W"
	}

	// APRS position: !DDMM.MMN/DDDMM.MME- (- = house symbol)
	packet := fmt.Sprintf("%s>APTETR,TCPIP*:!%02d%05.2f%s/%03d%05.2f%s-TETRA ISSI:%d via FreeTetra\r\n",
		call, latDeg, latMin, latDir, lonDeg, lonMin, lonDir, issi)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn == nil {
		a.logger.Printf("APRS-IS: not connected, dropping packet for %s", call)
		return
	}

	_, err := a.conn.Write([]byte(packet))
	if err != nil {
		a.logger.Printf("APRS-IS: send failed: %v", err)
		a.conn.Close()
		a.conn = nil
		return
	}

	a.logger.Printf("APRS-IS: sent position %s (ISSI %d) lat=%.4f lon=%.4f", call, issi, lat, lon)
}

// lookupCallsign queries RadioID API to find the callsign for a TETRA ISSI.
func (a *APRSBridge) lookupCallsign(issi uint32) string {
	a.mu.Lock()
	if call, ok := a.callCache[issi]; ok {
		if time.Now().Before(a.cacheTTL[issi]) {
			a.mu.Unlock()
			return call
		}
	}
	a.mu.Unlock()

	// Try full ISSI first, then strip extension digits (TETRA ISSIs may have
	// trailing digits beyond the base RadioID; e.g. 262356300 -> 2623563).
	call := a.queryRadioIDOnce(issi)
	if call == "" {
		for try := issi / 10; try >= 1000000; try /= 10 {
			call = a.queryRadioIDOnce(try)
			if call != "" {
				break
			}
		}
	}
	if call == "" {
		return ""
	}

	a.mu.Lock()
	a.callCache[issi] = call
	a.cacheTTL[issi] = time.Now().Add(aprsCacheTTL)
	a.mu.Unlock()

	a.logger.Printf("APRS-IS: resolved ISSI %d -> %s", issi, call)
	return call
}

// queryRadioIDOnce does a single RadioID API call, returns callsign or empty.
func (a *APRSBridge) queryRadioIDOnce(issi uint32) string {
	url := fmt.Sprintf("https://radioid.net/api/dmr/user/?id=%d", issi)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		Results []struct {
			Callsign string `json:"callsign"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	if len(result.Results) == 0 || result.Results[0].Callsign == "" {
		return ""
	}
	return strings.ToUpper(result.Results[0].Callsign)
}

// Close shuts down the APRS-IS connection.
func (a *APRSBridge) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}
}

// aprsPasscode calculates the APRS-IS passcode for a callsign.
func aprsPasscode(callsign string) string {
	call := strings.ToUpper(strings.Split(callsign, "-")[0])
	hash := uint16(0x73e2)
	for i := 0; i < len(call); i += 2 {
		hash ^= uint16(call[i]) << 8
		if i+1 < len(call) {
			hash ^= uint16(call[i+1])
		}
	}
	return fmt.Sprintf("%d", hash&0x7FFF)
}
