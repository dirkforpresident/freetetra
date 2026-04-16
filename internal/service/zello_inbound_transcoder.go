package service

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/freetetra/server/internal/config"
)

type zelloInboundTranscoder struct {
	cfg    config.ZelloConfig
	logger *log.Logger

	ffmpegCmd  *exec.Cmd
	encoderCmd *exec.Cmd
	ogg        *oggOpusWriter

	mu       sync.RWMutex
	asyncErr error
	stopping bool

	done     chan struct{}
	stopOnce sync.Once
}

func newZelloInboundTranscoder(
	cfg config.ZelloConfig,
	packetDurationMS int,
	logger *log.Logger,
	onSTEFrame func([]byte) error,
) (*zelloInboundTranscoder, error) {
	ffmpegBin := strings.TrimSpace(cfg.TrafficFFmpegBin)
	if ffmpegBin == "" {
		return nil, fmt.Errorf("ZELLO_TRAFFIC_FFMPEG_BIN is required")
	}
	encoderBin := strings.TrimSpace(cfg.TrafficEncoderBin)
	if encoderBin == "" {
		return nil, fmt.Errorf("ZELLO_TRAFFIC_ENCODER_BIN is required")
	}

	ffmpegCmd := exec.Command(ffmpegBin, zelloInboundFFmpegArgs()...)
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

	encoderArgs := strings.Fields(strings.TrimSpace(cfg.TrafficEncoderArgs))
	encoderCmd := exec.Command(encoderBin, encoderArgs...)
	encoderIn, err := encoderCmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("encoder stdin pipe: %w", err)
	}
	encoderOut, err := encoderCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("encoder stdout pipe: %w", err)
	}
	encoderErr, err := encoderCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("encoder stderr pipe: %w", err)
	}

	if err := encoderCmd.Start(); err != nil {
		return nil, fmt.Errorf("start encoder: %w", err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		_ = encoderIn.Close()
		_ = encoderCmd.Process.Kill()
		_ = encoderCmd.Wait()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	t := &zelloInboundTranscoder{
		cfg:        cfg,
		logger:     logger,
		ffmpegCmd:  ffmpegCmd,
		encoderCmd: encoderCmd,
		done:       make(chan struct{}),
	}

	go logCommandOutput(logger, "zello inbound ffmpeg", ffmpegErr)
	go logCommandOutput(logger, "zello inbound encoder", encoderErr)

	go func() {
		_, copyErr := io.Copy(encoderIn, ffmpegOut)
		_ = encoderIn.Close()
		if copyErr != nil && !errors.Is(copyErr, io.EOF) {
			t.setAsyncErr(fmt.Errorf("ffmpeg->encoder pipe: %w", copyErr))
		}
	}()

	go func() {
		if err := t.readEncoderFrames(encoderOut, onSTEFrame); err != nil && !errors.Is(err, io.EOF) {
			t.setAsyncErr(fmt.Errorf("read inbound encoder frames: %w", err))
		}
	}()

	ogg, err := newOggOpusWriter(ffmpegIn, 8000, packetDurationMS)
	if err != nil {
		_ = ffmpegIn.Close()
		_ = ffmpegCmd.Process.Kill()
		_ = encoderCmd.Process.Kill()
		_ = ffmpegCmd.Wait()
		_ = encoderCmd.Wait()
		return nil, err
	}
	t.ogg = ogg

	go func() {
		defer close(t.done)
		ffErr := ffmpegCmd.Wait()
		encErr := encoderCmd.Wait()
		if !t.isStopping() {
			if ffErr != nil {
				t.setAsyncErr(fmt.Errorf("inbound ffmpeg exit: %w", ffErr))
			}
			if encErr != nil {
				t.setAsyncErr(fmt.Errorf("inbound encoder exit: %w", encErr))
			}
		}
	}()

	return t, nil
}

func (t *zelloInboundTranscoder) WriteOpusPacket(packet []byte) error {
	if len(packet) == 0 {
		return nil
	}
	if err := t.Err(); err != nil {
		return err
	}
	if err := t.ogg.WritePacket(packet); err != nil {
		t.setAsyncErr(fmt.Errorf("write inbound opus packet: %w", err))
		return err
	}
	return nil
}

func (t *zelloInboundTranscoder) Close() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		t.stopping = true
		t.mu.Unlock()

		if t.ogg != nil {
			_ = t.ogg.Close()
		}

		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
			if t.ffmpegCmd.Process != nil {
				_ = t.ffmpegCmd.Process.Kill()
			}
			if t.encoderCmd.Process != nil {
				_ = t.encoderCmd.Process.Kill()
			}
			<-t.done
		}
	})
}

func (t *zelloInboundTranscoder) Err() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.asyncErr
}

func (t *zelloInboundTranscoder) setAsyncErr(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.asyncErr == nil {
		t.asyncErr = err
	}
}

func (t *zelloInboundTranscoder) isStopping() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stopping
}

func (t *zelloInboundTranscoder) readEncoderFrames(r io.Reader, onSTEFrame func([]byte) error) error {
	reader := bufio.NewReader(r)
	frameSize := t.cfg.TrafficEncoderFrameSize
	if frameSize < 1 {
		frameSize = 18
	}
	frame := make([]byte, frameSize)
	var pendingCodec18 []byte

	for {
		_, err := io.ReadFull(reader, frame)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}

		ste, ready, err := normalizeRadioFrame(frame, &pendingCodec18)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		if err := onSTEFrame(ste); err != nil {
			return err
		}
	}
}

func zelloInboundFFmpegArgs() []string {
	return []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "ogg",
		"-i", "pipe:0",
		"-f", "s16le",
		"-ac", "1",
		"-ar", "8000",
		"pipe:1",
	}
}

type oggOpusWriter struct {
	w             io.WriteCloser
	serial        uint32
	seq           uint32
	granule       uint64
	packetSamples uint32
}

func newOggOpusWriter(w io.WriteCloser, sampleRate int, packetDurationMS int) (*oggOpusWriter, error) {
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	if !isValidOpusFrameDuration(packetDurationMS) {
		packetDurationMS = 20
	}
	packetSamples := uint32(sampleRate * packetDurationMS / 1000)
	ow := &oggOpusWriter{
		w:             w,
		serial:        rand.Uint32(),
		packetSamples: packetSamples,
	}
	if err := ow.writeHeaders(sampleRate); err != nil {
		_ = w.Close()
		return nil, err
	}
	return ow, nil
}

func (ow *oggOpusWriter) WritePacket(packet []byte) error {
	if len(packet) == 0 {
		return nil
	}
	ow.granule += uint64(ow.packetSamples)
	return ow.writePage(packet, 0x00, ow.granule)
}

func (ow *oggOpusWriter) Close() error {
	return ow.w.Close()
}

func (ow *oggOpusWriter) writeHeaders(sampleRate int) error {
	head := make([]byte, 0, 19)
	head = append(head, []byte("OpusHead")...)
	head = append(head, 1) // version
	head = append(head, 1) // mono
	head = binary.LittleEndian.AppendUint16(head, 0)
	head = binary.LittleEndian.AppendUint32(head, uint32(sampleRate))
	head = binary.LittleEndian.AppendUint16(head, 0)
	head = append(head, 0)
	if err := ow.writePage(head, 0x02, 0); err != nil {
		return err
	}

	vendor := []byte("github.com/freetetra/server")
	tags := make([]byte, 0, 8+4+len(vendor)+4)
	tags = append(tags, []byte("OpusTags")...)
	tags = binary.LittleEndian.AppendUint32(tags, uint32(len(vendor)))
	tags = append(tags, vendor...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	return ow.writePage(tags, 0x00, 0)
}

func (ow *oggOpusWriter) writePage(packet []byte, headerType byte, granule uint64) error {
	segments := make([]byte, 0, (len(packet)+254)/255)
	remaining := len(packet)
	for remaining >= 255 {
		segments = append(segments, 255)
		remaining -= 255
	}
	segments = append(segments, byte(remaining))

	header := make([]byte, 27+len(segments))
	copy(header[:4], []byte("OggS"))
	header[4] = 0
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granule)
	binary.LittleEndian.PutUint32(header[14:18], ow.serial)
	binary.LittleEndian.PutUint32(header[18:22], ow.seq)
	// crc [22:26] stays zero for calculation
	header[26] = byte(len(segments))
	copy(header[27:], segments)

	page := append(header, packet...)
	crc := oggCRC32(page)
	binary.LittleEndian.PutUint32(page[22:26], crc)

	if _, err := ow.w.Write(page); err != nil {
		return err
	}
	ow.seq++
	return nil
}

var oggCRCTable = initOggCRCTable()

func initOggCRCTable() [256]uint32 {
	var table [256]uint32
	for i := 0; i < 256; i++ {
		r := uint32(i << 24)
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		table[i] = r
	}
	return table
}

func oggCRC32(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[(byte(crc>>24)^b)&0xFF]
	}
	return crc
}
