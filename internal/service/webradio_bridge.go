package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/config"
)

type WebRadioBridge struct {
	cfg    config.Config
	logger *log.Logger
	plane  InjectionPlane

	telemetry *webradioTelemetry

	mu     sync.Mutex
	cancel context.CancelFunc
	server *http.Server // optional, started when cfg.WebRadio.ListenAddr is set
	wg     sync.WaitGroup
}

func NewWebRadioBridge(cfg config.Config, logger *log.Logger, plane InjectionPlane) (*WebRadioBridge, error) {
	if strings.TrimSpace(cfg.WebRadio.StreamURL) == "" {
		return nil, fmt.Errorf("WEBRADIO_STREAM_URL is required when WEBRADIO_ENABLED=true")
	}
	if cfg.WebRadio.Talkgroup == 0 {
		return nil, fmt.Errorf("WEBRADIO_TALKGROUP must be > 0 when WEBRADIO_ENABLED=true")
	}
	if strings.TrimSpace(cfg.WebRadio.FFmpegBin) == "" {
		return nil, fmt.Errorf("WEBRADIO_FFMPEG_BIN is required")
	}
	if strings.TrimSpace(cfg.WebRadio.EncoderBin) == "" {
		return nil, fmt.Errorf("WEBRADIO_ENCODER_BIN is required")
	}
	return &WebRadioBridge{
		cfg:       cfg,
		logger:    logger,
		plane:     plane,
		telemetry: &webradioTelemetry{},
	}, nil
}

// Telemetry exposes the live observability state for the status endpoint
// and (in task 6) the silence-aware TX gate.
func (b *WebRadioBridge) Telemetry() *webradioTelemetry { return b.telemetry }

func (b *WebRadioBridge) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)

	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		cancel()
		return fmt.Errorf("webradio bridge already started")
	}
	b.cancel = cancel
	b.wg.Add(1)
	b.mu.Unlock()

	b.logger.Printf(
		"webradio bridge enabled stream=%s tg=%d source=%d encoder=%s frame_size=%d reconnect=%s",
		b.cfg.WebRadio.StreamURL,
		b.cfg.WebRadio.Talkgroup,
		b.cfg.WebRadio.SourceISSI,
		b.cfg.WebRadio.EncoderBin,
		b.encoderFrameSize(),
		b.reconnectDelay().String(),
	)

	go b.runLoop(runCtx)
	b.maybeStartHTTPServer()
	b.maybeStartTelemetryLogger(runCtx)
	return nil
}

// maybeStartHTTPServer brings up a small status server if ListenAddr is set.
// Failures are logged but don't break the audio pipeline — the operator gets
// a degraded experience (no /api/webradio/status), not a dead container.
func (b *WebRadioBridge) maybeStartHTTPServer() {
	addr := strings.TrimSpace(b.cfg.WebRadio.ListenAddr)
	if addr == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/webradio/status", b.handleStatus)
	srv := &http.Server{Addr: addr, Handler: mux}
	b.mu.Lock()
	b.server = srv
	b.mu.Unlock()
	go func() {
		b.logger.Printf("webradio status server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.logger.Printf("webradio status server error: %v", err)
		}
	}()
}

func (b *WebRadioBridge) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(b.telemetry.Snapshot())
}

// maybeStartTelemetryLogger emits a one-line summary every
// TelemetryLogEvery (default 30s). Skips if the cadence is zero.
func (b *WebRadioBridge) maybeStartTelemetryLogger(ctx context.Context) {
	every := b.cfg.WebRadio.TelemetryLogEvery
	if every <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap := b.telemetry.Snapshot()
				b.logger.Printf("webradio telemetry silence=%t silence_dur=%.2fs",
					snap.Silence, snap.SilenceDur)
			}
		}
	}()
}

func (b *WebRadioBridge) Stop() {
	b.mu.Lock()
	cancel := b.cancel
	srv := b.server
	b.cancel = nil
	b.server = nil
	b.mu.Unlock()

	if srv != nil {
		shutdownCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		c()
	}
	if cancel != nil {
		cancel()
	}
	b.wg.Wait()
}

func (b *WebRadioBridge) runLoop(ctx context.Context) {
	defer b.wg.Done()

	for {
		if ctx.Err() != nil {
			return
		}

		err := b.runSession(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			b.logger.Printf("webradio session error: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(b.reconnectDelay()):
		}
	}
}

func (b *WebRadioBridge) runSession(ctx context.Context) error {
	callID := uuid.New()

	ffmpegCmd := exec.CommandContext(ctx, b.cfg.WebRadio.FFmpegBin, b.ffmpegArgs()...)
	encoderCmd := exec.CommandContext(ctx, b.cfg.WebRadio.EncoderBin, b.encoderArgs()...)

	ffmpegOut, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	ffmpegErr, err := ffmpegCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}
	encoderIn, err := encoderCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("encoder stdin pipe: %w", err)
	}
	encoderOut, err := encoderCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("encoder stdout pipe: %w", err)
	}
	encoderErr, err := encoderCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("encoder stderr pipe: %w", err)
	}

	if err := encoderCmd.Start(); err != nil {
		return fmt.Errorf("start encoder: %w", err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		_ = encoderCmd.Process.Kill()
		_ = encoderCmd.Wait()
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	b.logger.Printf("webradio session %s: ffmpeg and encoder started", callID.String()[:8])
	go b.logCommandOutput("webradio ffmpeg", ffmpegErr)
	go b.logCommandOutput("webradio encoder", encoderErr)

	copyErrCh := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(encoderIn, ffmpegOut)
		_ = encoderIn.Close()
		copyErrCh <- copyErr
	}()

	activeCallID, callStarted, readErr := b.readEncoderFrames(ctx, callID, encoderOut)
	if readErr != nil && ctx.Err() == nil {
		if ffmpegCmd.Process != nil {
			_ = ffmpegCmd.Process.Kill()
		}
		if encoderCmd.Process != nil {
			_ = encoderCmd.Process.Kill()
		}
	}
	copyErr := <-copyErrCh
	ffmpegWaitErr := ffmpegCmd.Wait()
	encoderWaitErr := encoderCmd.Wait()

	if callStarted && activeCallID != uuid.Nil {
		b.plane.ReleaseInjectedCall("webradio", activeCallID, b.cfg.WebRadio.ReleaseCause)
	}

	if ctx.Err() != nil {
		return context.Canceled
	}

	// Log detailed diagnostics when session fails
	if readErr != nil {
		b.logger.Printf("webradio session %s: frame read error: %v", callID.String()[:8], readErr)
		return fmt.Errorf("read encoded frames: %w", readErr)
	}
	if copyErr != nil {
		b.logger.Printf("webradio session %s: ffmpeg->encoder pipe error: %v", callID.String()[:8], copyErr)
		return fmt.Errorf("pipe ffmpeg->encoder: %w", copyErr)
	}
	if ffmpegWaitErr != nil && !strings.Contains(ffmpegWaitErr.Error(), "context canceled") {
		b.logger.Printf("webradio session %s: ffmpeg exited unexpectedly: %v", callID.String()[:8], ffmpegWaitErr)
		return fmt.Errorf("ffmpeg exit: %w", ffmpegWaitErr)
	}
	if encoderWaitErr != nil && !strings.Contains(encoderWaitErr.Error(), "context canceled") {
		b.logger.Printf("webradio session %s: encoder exited unexpectedly: %v", callID.String()[:8], encoderWaitErr)
		return fmt.Errorf("encoder exit: %w", encoderWaitErr)
	}
	return nil
}

func (b *WebRadioBridge) readEncoderFrames(ctx context.Context, callID uuid.UUID, r io.Reader) (uuid.UUID, bool, error) {
	reader := bufio.NewReader(r)
	frameSize := b.encoderFrameSize()
	frame := make([]byte, frameSize)
	var pendingCodec18 []byte
	currentCallID := callID
	activeCallID := uuid.Nil
	callStarted := false
	frameInCall := 0
	lastFrameTime := time.Now()

	for {
		if ctx.Err() != nil {
			return activeCallID, callStarted, ctx.Err()
		}
		_, err := io.ReadFull(reader, frame)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				if !callStarted {
					b.logger.Printf("webradio session %s: stream ended (no frames produced)", callID.String()[:8])
				}
				return activeCallID, callStarted, nil
			}
			b.logger.Printf("webradio session %s: frame read failed after %v: %v", callID.String()[:8], time.Since(lastFrameTime), err)
			return activeCallID, callStarted, err
		}

		// Monitor time between frames to detect stalls
		now := time.Now()
		timeSinceLastFrame := now.Sub(lastFrameTime)
		if timeSinceLastFrame > 5*time.Second {
			b.logger.Printf("webradio session %s: frame stall detected (%.1fs since last frame)", callID.String()[:8], timeSinceLastFrame.Seconds())
		}
		lastFrameTime = now

		ste, ready, err := normalizeRadioFrame(frame, &pendingCodec18)
		if err != nil {
			return activeCallID, callStarted, err
		}
		if !ready {
			continue
		}

		// Silence-aware gating: while the parser reports silence, release
		// any active call and drop incoming frames. The next non-silent
		// frame falls through to the "!callStarted" branch below and
		// allocates a fresh callID, so receivers see a clean stop/start
		// instead of dead air being held on the TG.
		if b.cfg.WebRadio.SilenceGating && b.telemetry != nil && b.telemetry.IsSilent() {
			if callStarted {
				b.plane.IdleInjectedCall("webradio", currentCallID, b.cfg.WebRadio.ReleaseCause)
				b.logger.Printf("webradio silence gate: idled call=%s", currentCallID.String())
				callStarted = false
				activeCallID = uuid.Nil
				currentCallID = uuid.New()
				frameInCall = 0
			}
			continue
		}

		if !callStarted {
			if !b.plane.StartInjectedCall("webradio", currentCallID, b.cfg.WebRadio.SourceISSI, b.cfg.WebRadio.Talkgroup) {
				continue
			}
			callStarted = true
			activeCallID = currentCallID
			frameInCall = 0
			b.logger.Printf("webradio call started on tg=%d call=%s", b.cfg.WebRadio.Talkgroup, currentCallID.String())
		}

		// Periodically release and restart call to stay within TETRA group call model
		frameInCall++
		if frameInCall >= 500 { // ~15 seconds
			b.plane.IdleInjectedCall("webradio", currentCallID, b.cfg.WebRadio.ReleaseCause)
			callStarted = false
			activeCallID = uuid.Nil
			currentCallID = uuid.New()
			frameInCall = 0
			continue
		}

		b.plane.InjectedVoiceFrame("webradio", currentCallID, ste)
	}
}

func normalizeRadioFrame(frame []byte, pendingCodec18 *[]byte) ([]byte, bool, error) {
	switch len(frame) {
	case 18:
		if len(*pendingCodec18) == 0 {
			*pendingCodec18 = append([]byte(nil), frame...)
			return nil, false, nil
		}
		ste := pairCodec18ToSTE(*pendingCodec18, frame)
		*pendingCodec18 = nil
		return ste, true, nil
	case 35:
		ste := make([]byte, 36)
		copy(ste[1:], frame)
		return sanitizeSTEFrame(ste), true, nil
	case 36:
		return sanitizeSTEFrame(frame), true, nil
	case 274:
		if !allBytesAreBits(frame) {
			return nil, false, fmt.Errorf("274-byte encoder frame is not 1-bit-per-byte")
		}
		return packCodecBitsToSTE(frame), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported WEBRADIO_ENCODER_FRAME_SIZE=%d", len(frame))
	}
}

func pairCodec18ToSTE(a, b []byte) []byte {
	bits := make([]byte, 0, 274)
	bits = append(bits, unpackMSBBits(a, 137)...)
	bits = append(bits, unpackMSBBits(b, 137)...)
	return packCodecBitsToSTE(bits)
}

func unpackMSBBits(src []byte, count int) []byte {
	out := make([]byte, count)
	for i := 0; i < count; i++ {
		b := src[i/8]
		shift := 7 - (i % 8)
		out[i] = (b >> shift) & 1
	}
	return out
}

func packCodecBitsToSTE(bits []byte) []byte {
	ste := make([]byte, 36)
	for i := 0; i < 35; i++ {
		var v byte
		for bit := 0; bit < 8; bit++ {
			idx := i*8 + bit
			if idx < len(bits) && bits[idx] != 0 {
				v |= 1 << (7 - bit)
			}
		}
		ste[i+1] = v
	}
	return sanitizeSTEFrame(ste)
}

func (b *WebRadioBridge) logCommandOutput(prefix string, r io.Reader) {
	sc := bufio.NewScanner(r)
	// ffmpeg can emit long status lines (e.g. silencedetect spam under heavy
	// dynamics); bump the per-token buffer above bufio's 64 KiB default so
	// the scanner doesn't bail with ErrTooLong.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Tee through the telemetry parser before logging. Recognized lines
		// still go through the logger — visibility matters more than the
		// few bytes of duplication.
		if b.telemetry != nil {
			_ = b.telemetry.ParseLine(line)
		}
		b.logger.Printf("%s: %s", prefix, line)
	}
}

func (b *WebRadioBridge) ffmpegArgs() []string {
	args := []string{
		"-re",
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-i", b.cfg.WebRadio.StreamURL,
	}
	if chain := BuildWebRadioFilterChain(b.cfg.WebRadio); chain != "" {
		args = append(args, "-af", chain)
	}
	args = append(args,
		"-f", "s16le",
		"-ac", "1",
		"-ar", "8000",
		"pipe:1",
	)
	return args
}

func (b *WebRadioBridge) encoderArgs() []string {
	if strings.TrimSpace(b.cfg.WebRadio.EncoderArgs) == "" {
		return nil
	}
	return strings.Fields(b.cfg.WebRadio.EncoderArgs)
}

func (b *WebRadioBridge) encoderFrameSize() int {
	if b.cfg.WebRadio.EncoderFrameSize < 1 {
		return 18
	}
	return b.cfg.WebRadio.EncoderFrameSize
}

func (b *WebRadioBridge) reconnectDelay() time.Duration {
	if b.cfg.WebRadio.ReconnectDelay <= 0 {
		return 3 * time.Second
	}
	return b.cfg.WebRadio.ReconnectDelay
}
