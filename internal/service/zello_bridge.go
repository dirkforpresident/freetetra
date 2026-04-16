package service

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	zellocl "git.cheetah.cat/tetrapack/go-zello-client/client"
	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

type ZelloBridge struct {
	cfg    config.Config
	logger *log.Logger
	plane  InjectionPlane

	channels      []string
	channelToGSSI map[string]uint32
	gssiToChannel map[uint32]string

	mu             sync.Mutex
	running        bool
	stopCh         chan struct{}
	doneCh         chan struct{}
	client         *zellocl.ZelloClient
	inboundByID    map[int64]*zelloInboundStream
	outboundByCall map[uuid.UUID]*zelloOutboundCall
}

type zelloInboundStream struct {
	StreamID    int64
	Channel     string
	From        string
	GSSI        uint32
	PacketMS    int
	CallID      uuid.UUID
	SourceISSI  uint32
	Started     bool
	WaitingSubs bool
	PacketCount uint64
	Transcoder  *zelloInboundTranscoder
}

type zelloOutboundCall struct {
	CallID     uuid.UUID
	Channel    string
	GSSI       uint32
	StreamID   int64
	PacketID   uint32
	Started    bool
	LastWrite  time.Time
	Transcoder *zelloTrafficTranscoder
}

func NewZelloBridge(cfg config.Config, logger *log.Logger, plane InjectionPlane) (*ZelloBridge, error) {
	if cfg.Zello.WSURL == "" {
		return nil, fmt.Errorf("ZELLO_WS_URL is required when ZELLO_ENABLED=true")
	}
	if cfg.Zello.Username == "" {
		return nil, fmt.Errorf("ZELLO_USERNAME is required when ZELLO_ENABLED=true")
	}
	if cfg.Zello.Password == "" {
		return nil, fmt.Errorf("ZELLO_PASSWORD is required when ZELLO_ENABLED=true")
	}

	channelToGSSI, err := parseChannelGSSIMap(cfg.Zello.ChannelGSSIMap)
	if err != nil {
		return nil, err
	}
	if len(channelToGSSI) == 0 {
		return nil, fmt.Errorf("ZELLO_CHANNEL_GSSI_MAP is required (format: channel=10001,other=10002)")
	}

	channels := make([]string, 0, len(cfg.Zello.Channels))
	if len(cfg.Zello.Channels) > 0 {
		channels = append(channels, cfg.Zello.Channels...)
	} else {
		for ch := range channelToGSSI {
			channels = append(channels, ch)
		}
		sort.Strings(channels)
	}
	for _, ch := range channels {
		if _, ok := channelToGSSI[ch]; !ok {
			return nil, fmt.Errorf("channel %q in ZELLO_CHANNELS has no ZELLO_CHANNEL_GSSI_MAP entry", ch)
		}
	}

	gssiToChannel := make(map[uint32]string, len(channelToGSSI))
	for ch, gssi := range channelToGSSI {
		if prev, exists := gssiToChannel[gssi]; exists {
			return nil, fmt.Errorf("duplicate gssi mapping %d for channels %q and %q", gssi, prev, ch)
		}
		gssiToChannel[gssi] = ch
	}

	return &ZelloBridge{
		cfg:            cfg,
		logger:         logger,
		plane:          plane,
		channels:       channels,
		channelToGSSI:  channelToGSSI,
		gssiToChannel:  gssiToChannel,
		inboundByID:    make(map[int64]*zelloInboundStream),
		outboundByCall: make(map[uuid.UUID]*zelloOutboundCall),
	}, nil
}

func (z *ZelloBridge) Start(_ context.Context) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.running {
		return fmt.Errorf("zello bridge already started")
	}
	z.running = true
	z.stopCh = make(chan struct{})
	z.doneCh = make(chan struct{})

	z.logger.Printf(
		"zello bridge enabled ws=%s channels=%v map=%v listen_only=%t transcode_traffic=%t",
		z.cfg.Zello.WSURL,
		z.channels,
		z.channelToGSSI,
		z.cfg.Zello.ListenOnly,
		z.cfg.Zello.TranscodeTraffic,
	)
	go z.loop()
	return nil
}

func (z *ZelloBridge) Stop() {
	z.mu.Lock()
	if !z.running {
		z.mu.Unlock()
		return
	}
	close(z.stopCh)
	done := z.doneCh
	z.mu.Unlock()
	<-done
}

func (z *ZelloBridge) loop() {
	defer func() {
		z.mu.Lock()
		z.running = false
		close(z.doneCh)
		z.mu.Unlock()
	}()

	for {
		select {
		case <-z.stopCh:
			z.cleanupSession(nil)
			return
		default:
		}

		client := zellocl.NewZelloClient()
		client.Username = z.cfg.Zello.Username
		client.Password = z.cfg.Zello.Password
		client.DefaultTimeout = z.responseTimeout()

		if err := client.Connect(z.cfg.Zello.WSURL); err != nil {
			z.logger.Printf("zello connect error: %v", err)
			if !z.waitReconnect() {
				return
			}
			continue
		}

		go client.Work()
		if err := z.sendLogon(client); err != nil {
			z.logger.Printf("zello logon error: %v", err)
			_ = client.Disconnect()
			if !z.waitReconnect() {
				return
			}
			continue
		}

		z.mu.Lock()
		z.client = client
		z.mu.Unlock()
		z.logger.Printf("zello logon success channels=%v", z.channels)

		sessionErr := z.runSession(client)
		if sessionErr != nil {
			z.logger.Printf("zello session error: %v", sessionErr)
		}
		z.cleanupSession(client)

		if !z.waitReconnect() {
			return
		}
	}
}

func (z *ZelloBridge) sendLogon(client *zellocl.ZelloClient) error {
	command := map[string]any{
		"command":  "logon",
		"username": z.cfg.Zello.Username,
		"password": z.cfg.Zello.Password,
		"channels": z.channels,
	}
	if z.cfg.Zello.ListenOnly {
		command["listen_only"] = true
	}
	if platformType := strings.TrimSpace(z.cfg.Zello.PlatformType); platformType != "" {
		command["platform_type"] = platformType
	}
	if platformName := strings.TrimSpace(z.cfg.Zello.PlatformName); platformName != "" {
		command["platform_name"] = platformName
	}
	return client.SendRaw(command)
}

func (z *ZelloBridge) runSession(client *zellocl.ZelloClient) error {
	for {
		select {
		case <-z.stopCh:
			return nil
		case evt := <-client.GeneralEventChan:
			z.handleGeneralEvent(evt)
		case bin := <-client.BinaryDataChan:
			z.handleBinaryData(bin)
		case err := <-client.ErrorEventChan:
			return err
		}
	}
}

func (z *ZelloBridge) cleanupSession(client *zellocl.ZelloClient) {
	z.mu.Lock()
	if client != nil && z.client == client {
		z.client = nil
	}
	inbound := make([]*zelloInboundStream, 0, len(z.inboundByID))
	for _, s := range z.inboundByID {
		inbound = append(inbound, s)
	}
	outbound := make([]*zelloOutboundCall, 0, len(z.outboundByCall))
	for _, s := range z.outboundByCall {
		outbound = append(outbound, s)
	}
	z.inboundByID = make(map[int64]*zelloInboundStream)
	z.outboundByCall = make(map[uuid.UUID]*zelloOutboundCall)
	z.mu.Unlock()

	for _, s := range inbound {
		if s.Transcoder != nil {
			s.Transcoder.Close()
		}
		if s.Started {
			z.plane.ReleaseInjectedCall("zello", s.CallID, 0)
		}
	}
	if client != nil {
		for _, s := range outbound {
			if s.Transcoder != nil {
				s.Transcoder.Close()
			}
			if s.StreamID > 0 {
				_ = client.StopStream(s.Channel, s.StreamID)
			}
		}
		_ = client.Disconnect()
	}
}

func (z *ZelloBridge) waitReconnect() bool {
	select {
	case <-z.stopCh:
		return false
	case <-time.After(z.reconnectDelay()):
		return true
	}
}

func (z *ZelloBridge) handleGeneralEvent(evt zellocl.ZelloResponsePacked) {
	switch evt.ZelloResponse.Command {
	case "on_stream_start":
		channel, _ := evt.Raw["channel"].(string)
		streamID, _ := toInt64(evt.Raw["stream_id"])
		from, _ := evt.Raw["from"].(string)
		packetMS := z.cfg.Zello.CodecDurationMS
		if packetMS <= 0 {
			packetMS = 20
		}
		if rawMS, ok := evt.Raw["packet_duration"]; ok {
			switch v := rawMS.(type) {
			case float64:
				if int(v) > 0 {
					packetMS = int(v)
				}
			case int:
				if v > 0 {
					packetMS = v
				}
			}
		}
		if strings.EqualFold(strings.TrimSpace(from), strings.TrimSpace(z.cfg.Zello.Username)) {
			return
		}
		gssi, ok := z.channelToGSSI[channel]
		if !ok {
			z.logger.Printf("zello stream-start stream=%d channel=%q unmapped", streamID, channel)
			return
		}
		if streamID == 0 {
			return
		}

		z.mu.Lock()
		z.inboundByID[streamID] = &zelloInboundStream{
			StreamID:   streamID,
			Channel:    channel,
			From:       from,
			GSSI:       gssi,
			PacketMS:   packetMS,
			CallID:     uuid.New(),
			SourceISSI: z.sourceISSI(channel, from),
		}
		z.mu.Unlock()
		z.logger.Printf("zello stream-start stream=%d channel=%q from=%q gssi=%d", streamID, channel, from, gssi)

	case "on_stream_stop":
		streamID, _ := toInt64(evt.Raw["stream_id"])
		if streamID == 0 {
			return
		}
		z.mu.Lock()
		stream := z.inboundByID[streamID]
		delete(z.inboundByID, streamID)
		z.mu.Unlock()
		if stream == nil {
			return
		}
		if stream.Transcoder != nil {
			stream.Transcoder.Close()
		}
		if stream.Started {
			z.plane.ReleaseInjectedCall("zello", stream.CallID, 0)
		}
		z.logger.Printf("zello stream-stop stream=%d channel=%q packets=%d", streamID, stream.Channel, stream.PacketCount)
	}
}

func (z *ZelloBridge) handleBinaryData(data []byte) {
	streamID, _, payload, ok := parseZelloAudioPacket(data)
	if !ok {
		return
	}

	var (
		callID     uuid.UUID
		gssi       uint32
		sourceISSI uint32
		started    bool
	)
	z.mu.Lock()
	stream := z.inboundByID[streamID]
	if stream != nil {
		callID = stream.CallID
		gssi = stream.GSSI
		sourceISSI = stream.SourceISSI
		started = stream.Started
	}
	z.mu.Unlock()
	if stream == nil {
		return
	}

	if !started {
		if z.plane.GroupSubscriberCount(gssi) == 0 {
			waitingLogged := false
			z.mu.Lock()
			if current := z.inboundByID[streamID]; current != nil && !current.WaitingSubs {
				current.WaitingSubs = true
				waitingLogged = true
			}
			z.mu.Unlock()
			if waitingLogged {
				z.logger.Printf("zello stream=%d waiting subscribers on gssi=%d", streamID, gssi)
			}
			return
		}
		if !z.plane.StartInjectedCall("zello", callID, sourceISSI, gssi) {
			return
		}
		z.mu.Lock()
		if current := z.inboundByID[streamID]; current != nil {
			current.Started = true
			current.WaitingSubs = false
		}
		z.mu.Unlock()
	}

	z.mu.Lock()
	if current := z.inboundByID[streamID]; current != nil {
		current.PacketCount++
	}
	z.mu.Unlock()
	transcoder, err := z.ensureInboundTranscoder(streamID, callID)
	if err != nil {
		z.logger.Printf("zello inbound transcoder unavailable stream=%d call=%s: %v", streamID, callID.String(), err)
		return
	}
	if err := transcoder.Err(); err != nil {
		z.logger.Printf("zello inbound transcoder error stream=%d call=%s: %v", streamID, callID.String(), err)
		return
	}
	if err := transcoder.WriteOpusPacket(payload); err != nil {
		z.logger.Printf("zello inbound packet decode failed stream=%d call=%s: %v", streamID, callID.String(), err)
	}
}

func (z *ZelloBridge) OnBrewCallControl(m *brew.CallControlMessage) {
	if m == nil {
		return
	}
	if z.cfg.Zello.ListenOnly {
		return
	}

	switch m.CallState {
	case brew.CallStateCallRelease, brew.CallStateGroupIdle, brew.CallStatePDPRelease, brew.CallStatePDPReject:
		z.stopOutboundCall(m.Identifier)
	default:
		destinationGSSI, _, hasRoute := callRoutingHint(m.Payload)
		if !hasRoute || destinationGSSI == 0 {
			z.mu.Lock()
			state := z.outboundByCall[m.Identifier]
			z.mu.Unlock()
			if state == nil {
				return
			}
			destinationGSSI = state.GSSI
		}
		channel, ok := z.gssiToChannel[destinationGSSI]
		if !ok {
			return
		}

		z.mu.Lock()
		if current, exists := z.outboundByCall[m.Identifier]; !exists {
			z.outboundByCall[m.Identifier] = &zelloOutboundCall{
				CallID:  m.Identifier,
				Channel: channel,
				GSSI:    destinationGSSI,
			}
		} else {
			current.Channel = channel
			current.GSSI = destinationGSSI
		}
		z.mu.Unlock()
	}
}

func (z *ZelloBridge) OnBrewFrame(callID uuid.UUID, frameType uint8, data []byte) {
	if z.cfg.Zello.ListenOnly {
		return
	}
	if len(data) == 0 {
		return
	}
	z.mu.Lock()
	outbound := z.outboundByCall[callID]
	z.mu.Unlock()
	if outbound == nil || outbound.GSSI == 0 {
		return
	}
	destinationGSSI := outbound.GSSI

	switch frameType {
	case brew.FrameTypePacketData:
		z.forwardOutboundPacket(callID, destinationGSSI, data)
	case brew.FrameTypeTrafficChannel:
		z.forwardOutboundTraffic(callID, destinationGSSI, data)
	default:
		return
	}
}

func (z *ZelloBridge) forwardOutboundPacket(callID uuid.UUID, destinationGSSI uint32, data []byte) {
	channel, ok := z.gssiToChannel[destinationGSSI]
	if !ok {
		return
	}

	if !z.ensureOutboundStream(callID, channel, destinationGSSI) {
		return
	}
	if err := z.sendOutboundPayload(callID, data); err != nil {
		z.logger.Printf("zello send-packet failed call=%s channel=%q: %v", callID.String(), channel, err)
	}
}

func (z *ZelloBridge) forwardOutboundTraffic(callID uuid.UUID, destinationGSSI uint32, data []byte) {
	if !z.cfg.Zello.TranscodeTraffic {
		z.forwardOutboundPacket(callID, destinationGSSI, data)
		return
	}

	z.mu.Lock()
	channel, ok := z.gssiToChannel[destinationGSSI]
	if !ok {
		z.mu.Unlock()
		return
	}
	if _, exists := z.outboundByCall[callID]; !exists {
		z.outboundByCall[callID] = &zelloOutboundCall{
			CallID:  callID,
			Channel: channel,
			GSSI:    destinationGSSI,
		}
	}
	z.mu.Unlock()

	if !z.ensureOutboundStream(callID, channel, destinationGSSI) {
		return
	}

	transcoder, err := z.ensureTrafficTranscoder(callID)
	if err != nil {
		z.logger.Printf("zello traffic transcoder unavailable call=%s channel=%q: %v", callID.String(), channel, err)
		return
	}
	if err := transcoder.Err(); err != nil {
		z.logger.Printf("zello traffic transcoder error call=%s channel=%q: %v", callID.String(), channel, err)
		return
	}

	ste, err := normalizeTrafficSTE(data)
	if err != nil {
		z.logger.Printf("zello drop traffic frame call=%s channel=%q: %v", callID.String(), channel, err)
		return
	}
	a, b := steToCodecFrames(ste)
	if err := transcoder.WriteCodecFrame(a); err != nil {
		z.logger.Printf("zello traffic write failed call=%s channel=%q: %v", callID.String(), channel, err)
		return
	}
	if err := transcoder.WriteCodecFrame(b); err != nil {
		z.logger.Printf("zello traffic write failed call=%s channel=%q: %v", callID.String(), channel, err)
		return
	}
}

func (z *ZelloBridge) ensureOutboundStream(callID uuid.UUID, channel string, destinationGSSI uint32) bool {
	z.mu.Lock()
	client := z.client
	state, exists := z.outboundByCall[callID]
	if !exists {
		state = &zelloOutboundCall{CallID: callID, Channel: channel, GSSI: destinationGSSI}
		z.outboundByCall[callID] = state
	}
	started := state.Started && state.StreamID > 0
	z.mu.Unlock()
	if client == nil {
		return false
	}
	if started {
		return true
	}

	streamID, err := client.StartStream(channel, z.codecHeader(), "")
	if err != nil {
		z.logger.Printf("zello start-stream failed call=%s channel=%q: %v", callID.String(), channel, err)
		return false
	}

	z.mu.Lock()
	current := z.outboundByCall[callID]
	if current == nil {
		_ = client.StopStream(channel, streamID)
		z.mu.Unlock()
		return false
	}
	current.StreamID = streamID
	current.Started = true
	current.PacketID = 0
	z.mu.Unlock()
	z.logger.Printf("zello start-stream call=%s channel=%q stream=%d", callID.String(), channel, streamID)
	return true
}

func (z *ZelloBridge) ensureTrafficTranscoder(callID uuid.UUID) (*zelloTrafficTranscoder, error) {
	z.mu.Lock()
	state := z.outboundByCall[callID]
	if state == nil {
		z.mu.Unlock()
		return nil, fmt.Errorf("outbound call state missing")
	}
	if state.Transcoder != nil {
		t := state.Transcoder
		z.mu.Unlock()
		return t, nil
	}
	z.mu.Unlock()

	transcoder, err := newZelloTrafficTranscoder(z.cfg.Zello, z.logger, func(packet []byte) error {
		return z.sendOutboundPayload(callID, packet)
	})
	if err != nil {
		return nil, err
	}

	z.mu.Lock()
	defer z.mu.Unlock()
	current := z.outboundByCall[callID]
	if current == nil {
		transcoder.Close()
		return nil, fmt.Errorf("outbound call state disappeared")
	}
	if current.Transcoder != nil {
		transcoder.Close()
		return current.Transcoder, nil
	}
	current.Transcoder = transcoder
	return transcoder, nil
}

func (z *ZelloBridge) sendOutboundPayload(callID uuid.UUID, payload []byte) error {
	z.mu.Lock()
	client := z.client
	state := z.outboundByCall[callID]
	if client == nil || state == nil || !state.Started || state.StreamID == 0 {
		z.mu.Unlock()
		return fmt.Errorf("no active zello stream for call")
	}
	streamID := state.StreamID
	packetID := state.PacketID
	state.PacketID++
	z.mu.Unlock()

	packet := buildZelloAudioPacket(uint32(streamID), packetID, payload)
	if err := client.SendBinary(packet); err != nil {
		return err
	}
	z.mu.Lock()
	if current := z.outboundByCall[callID]; current != nil && current.StreamID == streamID {
		current.LastWrite = time.Now()
	}
	z.mu.Unlock()
	return nil
}

func (z *ZelloBridge) stopOutboundCall(callID uuid.UUID) {
	z.mu.Lock()
	client := z.client
	state := z.outboundByCall[callID]
	z.mu.Unlock()

	if state == nil {
		return
	}
	if state.Transcoder != nil {
		state.Transcoder.Close()
	}
	if client != nil && state.StreamID != 0 {
		if err := client.StopStream(state.Channel, state.StreamID); err != nil {
			z.logger.Printf("zello stop-stream failed call=%s stream=%d: %v", callID.String(), state.StreamID, err)
		}
	}
	z.mu.Lock()
	delete(z.outboundByCall, callID)
	z.mu.Unlock()
}

func (z *ZelloBridge) ensureInboundTranscoder(streamID int64, callID uuid.UUID) (*zelloInboundTranscoder, error) {
	z.mu.Lock()
	stream := z.inboundByID[streamID]
	if stream == nil {
		z.mu.Unlock()
		return nil, fmt.Errorf("missing inbound stream")
	}
	if stream.Transcoder != nil {
		t := stream.Transcoder
		z.mu.Unlock()
		return t, nil
	}
	gssi := stream.GSSI
	packetMS := stream.PacketMS
	z.mu.Unlock()

	transcoder, err := newZelloInboundTranscoder(z.cfg.Zello, packetMS, z.logger, func(ste []byte) error {
		z.plane.InjectedVoiceFrame("zello", callID, ste)
		return nil
	})
	if err != nil {
		return nil, err
	}

	z.mu.Lock()
	defer z.mu.Unlock()
	current := z.inboundByID[streamID]
	if current == nil {
		transcoder.Close()
		return nil, fmt.Errorf("inbound stream disappeared")
	}
	if current.Transcoder != nil {
		transcoder.Close()
		return current.Transcoder, nil
	}
	current.Transcoder = transcoder
	z.logger.Printf("zello inbound transcoder attached stream=%d tg=%d call=%s", streamID, gssi, callID.String())
	return transcoder, nil
}

func (z *ZelloBridge) sourceISSI(channel, from string) uint32 {
	base := z.cfg.Zello.SourceISSIBase
	if base == 0 {
		base = 800000
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(channel))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(from))
	return base + (h.Sum32() % 100000)
}

func (z *ZelloBridge) codecHeader() zellocl.ZelloCodecHeader {
	sampleRate := z.cfg.Zello.CodecSampleRate
	if sampleRate == 0 {
		sampleRate = 8000
	}
	frames := z.cfg.Zello.CodecFrames
	if frames <= 0 {
		frames = 1
	}
	frameSize := z.cfg.Zello.CodecFrameSize
	if frameSize <= 0 {
		frameSize = 60
	}
	duration := z.cfg.Zello.CodecDurationMS
	if duration <= 0 {
		duration = 50
	}
	return zellocl.ZelloCodecHeader{
		SampleRate:     sampleRate,
		FramesPerPaket: frames,
		FrameSize:      frameSize,
		PacketDuration: duration,
	}
}

func parseChannelGSSIMap(raw string) (map[string]uint32, error) {
	out := make(map[string]uint32)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	entries := strings.Split(raw, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		i := strings.LastIndex(entry, "=")
		if i <= 0 || i == len(entry)-1 {
			return nil, fmt.Errorf("invalid ZELLO_CHANNEL_GSSI_MAP entry %q (expected channel=gssi)", entry)
		}
		channel := strings.TrimSpace(entry[:i])
		gssiRaw := strings.TrimSpace(entry[i+1:])
		if channel == "" {
			return nil, fmt.Errorf("empty channel in ZELLO_CHANNEL_GSSI_MAP entry %q", entry)
		}
		gssi64, err := strconv.ParseUint(gssiRaw, 10, 32)
		if err != nil || gssi64 == 0 {
			return nil, fmt.Errorf("invalid gssi %q in ZELLO_CHANNEL_GSSI_MAP entry %q", gssiRaw, entry)
		}
		out[channel] = uint32(gssi64)
	}
	return out, nil
}

func ParseChannelGSSIMap(raw string) (map[string]uint32, error) {
	return parseChannelGSSIMap(raw)
}

func ChannelMapGSSIs(mapping map[string]uint32) []uint32 {
	out := make([]uint32, 0, len(mapping))
	seen := make(map[uint32]struct{}, len(mapping))
	for _, gssi := range mapping {
		if _, ok := seen[gssi]; ok {
			continue
		}
		seen[gssi] = struct{}{}
		out = append(out, gssi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func buildZelloAudioPacket(streamID, packetID uint32, payload []byte) []byte {
	out := make([]byte, 0, 9+len(payload))
	out = append(out, 0x01)
	out = binary.BigEndian.AppendUint32(out, streamID)
	out = binary.BigEndian.AppendUint32(out, packetID)
	out = append(out, payload...)
	return out
}

func parseZelloAudioPacket(data []byte) (streamID int64, packetID uint32, payload []byte, ok bool) {
	if len(data) < 9 || data[0] != 0x01 {
		return 0, 0, nil, false
	}
	streamID = int64(binary.BigEndian.Uint32(data[1:5]))
	packetID = binary.BigEndian.Uint32(data[5:9])
	payload = append([]byte(nil), data[9:]...)
	return streamID, packetID, payload, true
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), true
	default:
		return 0, false
	}
}

func (z *ZelloBridge) reconnectDelay() time.Duration {
	if z.cfg.Zello.ReconnectDelay <= 0 {
		return 5 * time.Second
	}
	return z.cfg.Zello.ReconnectDelay
}

func (z *ZelloBridge) responseTimeout() time.Duration {
	if z.cfg.Zello.ResponseTimeout <= 0 {
		return 10 * time.Second
	}
	return z.cfg.Zello.ResponseTimeout
}
