package service

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freetetra/server/internal/config"
)

type zelloTrafficTranscoder struct {
	logger *log.Logger

	decoderCmd *exec.Cmd
	ffmpegCmd  *exec.Cmd
	decoderIn  io.WriteCloser

	mu       sync.RWMutex
	asyncErr error
	stopping bool

	done     chan struct{}
	stopOnce sync.Once
}

func newZelloTrafficTranscoder(
	cfg config.ZelloConfig,
	logger *log.Logger,
	onOpusPacket func([]byte) error,
) (*zelloTrafficTranscoder, error) {
	decoderBin := strings.TrimSpace(cfg.TrafficDecoderBin)
	if decoderBin == "" {
		return nil, fmt.Errorf("ZELLO_TRAFFIC_DECODER_BIN is required when transcoding traffic")
	}
	ffmpegBin := strings.TrimSpace(cfg.TrafficFFmpegBin)
	if ffmpegBin == "" {
		return nil, fmt.Errorf("ZELLO_TRAFFIC_FFMPEG_BIN is required when transcoding traffic")
	}

	ffmpegArgs := defaultZelloFFmpegArgs(cfg)
	if raw := strings.TrimSpace(cfg.TrafficFFmpegArgs); raw != "" {
		ffmpegArgs = strings.Fields(raw)
	}

	decoderCmd := exec.Command(decoderBin)
	decoderIn, err := decoderCmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("decoder stdin pipe: %w", err)
	}
	decoderOut, err := decoderCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("decoder stdout pipe: %w", err)
	}
	decoderErr, err := decoderCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("decoder stderr pipe: %w", err)
	}

	ffmpegCmd := exec.Command(ffmpegBin, ffmpegArgs...)
	ffmpegIn, err := ffmpegCmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdin pipe: %w", err)
	}
	ffmpegOut, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	ffmpegErr, err := ffmpegCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}

	if err := ffmpegCmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	if err := decoderCmd.Start(); err != nil {
		_ = ffmpegIn.Close()
		_ = ffmpegCmd.Process.Kill()
		_ = ffmpegCmd.Wait()
		return nil, fmt.Errorf("start decoder: %w", err)
	}

	t := &zelloTrafficTranscoder{
		logger:     logger,
		decoderCmd: decoderCmd,
		ffmpegCmd:  ffmpegCmd,
		decoderIn:  decoderIn,
		done:       make(chan struct{}),
	}

	go logCommandOutput(logger, "zello decoder", decoderErr)
	go logCommandOutput(logger, "zello ffmpeg", ffmpegErr)

	go func() {
		_, copyErr := io.Copy(ffmpegIn, decoderOut)
		_ = ffmpegIn.Close()
		if copyErr != nil && !errors.Is(copyErr, io.EOF) {
			t.setAsyncErr(fmt.Errorf("decoder->ffmpeg pipe: %w", copyErr))
		}
	}()

	go func() {
		if err := parseOggOpusPackets(ffmpegOut, onOpusPacket); err != nil && !errors.Is(err, io.EOF) {
			t.setAsyncErr(fmt.Errorf("parse opus packets: %w", err))
		}
	}()

	go func() {
		defer close(t.done)
		decErr := decoderCmd.Wait()
		ffErr := ffmpegCmd.Wait()
		if !t.isStopping() {
			if decErr != nil {
				t.setAsyncErr(fmt.Errorf("decoder exit: %w", decErr))
			}
			if ffErr != nil {
				t.setAsyncErr(fmt.Errorf("ffmpeg exit: %w", ffErr))
			}
		}
	}()

	return t, nil
}

func (t *zelloTrafficTranscoder) WriteCodecFrame(frame []byte) error {
	if len(frame) != 18 {
		return fmt.Errorf("codec frame must be 18 bytes, got=%d", len(frame))
	}
	if err := t.Err(); err != nil {
		return err
	}

	_, err := t.decoderIn.Write(frame)
	if err != nil {
		t.setAsyncErr(fmt.Errorf("write decoder stdin: %w", err))
		return err
	}
	return nil
}

func (t *zelloTrafficTranscoder) Close() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		t.stopping = true
		t.mu.Unlock()

		_ = t.decoderIn.Close()

		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
			if t.decoderCmd.Process != nil {
				_ = t.decoderCmd.Process.Kill()
			}
			if t.ffmpegCmd.Process != nil {
				_ = t.ffmpegCmd.Process.Kill()
			}
			<-t.done
		}
	})
}

func (t *zelloTrafficTranscoder) Err() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.asyncErr
}

func (t *zelloTrafficTranscoder) setAsyncErr(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.asyncErr == nil {
		t.asyncErr = err
	}
}

func (t *zelloTrafficTranscoder) isStopping() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stopping
}

func defaultZelloFFmpegArgs(cfg config.ZelloConfig) []string {
	frameDuration := cfg.CodecDurationMS
	if !isValidOpusFrameDuration(frameDuration) {
		frameDuration = 20
	}
	return []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "s16le",
		"-ac", "1",
		"-ar", "8000",
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-application", "voip",
		"-frame_duration", strconv.Itoa(frameDuration),
		"-f", "opus",
		"pipe:1",
	}
}

func isValidOpusFrameDuration(ms int) bool {
	switch ms {
	case 5, 10, 20, 40, 60:
		return true
	default:
		return false
	}
}

func parseOggOpusPackets(r io.Reader, onPacket func([]byte) error) error {
	reader := bufio.NewReader(r)
	var partial []byte

	for {
		header := make([]byte, 27)
		_, err := io.ReadFull(reader, header)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		if !bytes.Equal(header[:4], []byte("OggS")) {
			return fmt.Errorf("unexpected ogg capture pattern")
		}

		segments := int(header[26])
		lacing := make([]byte, segments)
		if _, err := io.ReadFull(reader, lacing); err != nil {
			return err
		}

		totalPayload := 0
		for _, v := range lacing {
			totalPayload += int(v)
		}
		payload := make([]byte, totalPayload)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}

		offset := 0
		for _, segLen := range lacing {
			partial = append(partial, payload[offset:offset+int(segLen)]...)
			offset += int(segLen)
			if segLen == 255 {
				continue
			}

			packet := append([]byte(nil), partial...)
			partial = partial[:0]
			if len(packet) == 0 || isOggOpusHeaderPacket(packet) {
				continue
			}
			if err := onPacket(packet); err != nil {
				return err
			}
		}
	}
}

func isOggOpusHeaderPacket(packet []byte) bool {
	return bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags"))
}

func logCommandOutput(logger *log.Logger, prefix string, r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		logger.Printf("%s: %s", prefix, line)
	}
}
