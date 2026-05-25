package service

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestBuildSDSPayload_Flash(t *testing.T) {
	sdsMessageRefCounter.Store(0)

	payload, err := buildSDSPayload("flash", "hello", "")
	if err != nil {
		t.Fatalf("buildSDSPayload error: %v", err)
	}
	if len(payload) < 5 {
		t.Fatalf("payload too short: %d", len(payload))
	}
	if payload[0] != sdsProtocolImmediateTextTL {
		t.Fatalf("unexpected protocol id: got=%d", payload[0])
	}
	if payload[1] != sdsTLMessageTypeSimpleTransfer {
		t.Fatalf("unexpected tl type: got=0x%02x", payload[1])
	}
	if payload[3] != sdsTLCodingSchemeEightBitLatin1 {
		t.Fatalf("unexpected coding scheme: got=0x%02x", payload[3])
	}
	if got := string(payload[4:]); got != "hello" {
		t.Fatalf("unexpected text payload: %q", got)
	}
}

func TestBuildSDSPayload_CalloutStructure(t *testing.T) {
	sdsMessageRefCounter.Store(0)

	payload, err := buildSDSPayload("callout", "Gate 3", "")
	if err != nil {
		t.Fatalf("buildSDSPayload error: %v", err)
	}
	if len(payload) < 8 {
		t.Fatalf("payload too short: %d", len(payload))
	}
	if payload[0] != sdsProtocolCallout {
		t.Fatalf("unexpected protocol id: got=%d", payload[0])
	}
	if payload[1] != 0x00 || payload[3] != 0x01 || payload[4] != 0x10 || payload[5] != 0x01 || payload[6] != 0x00 {
		t.Fatalf("unexpected callout header: %x", payload[:7])
	}
	if got := string(payload[7:]); got != "Gate 3" {
		t.Fatalf("unexpected callout text payload: %q", got)
	}
}

func TestBuildSDSPayload_CalloutWithOptions(t *testing.T) {
	sdsMessageRefCounter.Store(41)
	opts := defaultCalloutEncodeOptions()
	opts.MessageType = 2
	opts.DeliveryReportRequest = 1
	opts.StorageAllowed = true
	opts.Function = 4
	opts.CalloutNumber = 9
	opts.Severity = 0x0f
	opts.GroupControl = 3
	opts.TimestampControl = true
	opts.UserReceiptControl = true
	opts.TextIsStatus = true
	opts.EndCallout = true
	opts.PTTNotAllowed = true
	opts.ExtensionHeader = true

	payload, err := buildSDSPayloadWithOptions("callout", "x", "", opts)
	if err != nil {
		t.Fatalf("buildSDSPayloadWithOptions error: %v", err)
	}
	if payload[1] != 0x25 {
		t.Fatalf("unexpected b1: 0x%02x", payload[1])
	}
	if payload[1]&0x02 != 0 {
		t.Fatalf("service selection bit must be 0, got b1=0x%02x", payload[1])
	}
	if payload[2] != 42 {
		t.Fatalf("unexpected message ref: %d", payload[2])
	}
	if payload[4] != 0x40 {
		t.Fatalf("unexpected b4: 0x%02x", payload[4])
	}
	if payload[5] != 0x09 {
		t.Fatalf("unexpected b5: 0x%02x", payload[5])
	}
	if payload[6] != 0xff {
		t.Fatalf("unexpected b6: 0x%02x", payload[6])
	}
	if payload[7] != 0xe0 {
		t.Fatalf("unexpected b7: 0x%02x", payload[7])
	}
}

func TestBuildSDSPayload_CalloutMessageRefAlwaysIncreasing(t *testing.T) {
	sdsMessageRefCounter.Store(0)
	first, err := buildSDSPayload("callout", "A", "")
	if err != nil {
		t.Fatalf("first callout build error: %v", err)
	}
	second, err := buildSDSPayload("callout", "B", "")
	if err != nil {
		t.Fatalf("second callout build error: %v", err)
	}
	if first[2] != 1 || second[2] != 2 {
		t.Fatalf("unexpected refs first=%d second=%d", first[2], second[2])
	}
}

func TestBuildSDSPayload_HomeIndicatorTextTL(t *testing.T) {
	sdsMessageRefCounter.Store(0)

	payload, err := buildSDSPayload("home_indicator", "Dispatch", "")
	if err != nil {
		t.Fatalf("buildSDSPayload error: %v", err)
	}
	if payload[0] != sdsProtocolHomeModeDisplay {
		t.Fatalf("unexpected protocol id: got=%d", payload[0])
	}
	if got := string(payload[4:]); got != "HOME: Dispatch" {
		t.Fatalf("unexpected home indicator payload: %q", got)
	}
}

func TestBuildSDSPayload_WapPushPorts(t *testing.T) {
	sdsMessageRefCounter.Store(0)

	payload, err := buildSDSPayload("wap_push", "http://example.invalid", "")
	if err != nil {
		t.Fatalf("buildSDSPayload error: %v", err)
	}
	if len(payload) < 6 {
		t.Fatalf("payload too short: %d", len(payload))
	}
	if payload[0] != sdsProtocolWdpTL {
		t.Fatalf("unexpected protocol id: got=%d", payload[0])
	}
	dst := uint16(payload[1])<<8 | uint16(payload[2])
	src := uint16(payload[3])<<8 | uint16(payload[4])
	if dst != sdsWapPushDstPort || src != sdsWapPushSrcPort {
		t.Fatalf("unexpected wap ports dst=%d src=%d", dst, src)
	}
	if got := string(payload[5:]); got != "http://example.invalid" {
		t.Fatalf("unexpected wap payload: %q", got)
	}
}

func TestBuildSDSPayload_RawHexOverride(t *testing.T) {
	sdsMessageRefCounter.Store(0)

	payload, err := buildSDSPayload("flash", "", "0x8202010154657374")
	if err != nil {
		t.Fatalf("buildSDSPayload error: %v", err)
	}
	if got := hex.EncodeToString(payload); got != "8202010154657374" {
		t.Fatalf("unexpected payload_hex decoding: %s", got)
	}
}

func TestDetectSDSKind(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "empty", raw: nil, want: "empty"},
		{name: "flash", raw: []byte{0x89, 0x02, 0x01, 0x01, 'H'}, want: "flash"},
		{name: "callout", raw: []byte{0xC3, 0x00, 0x01, 0x01, 0x10, 0x01, 0x00, 't', 'e', 's', 't'}, want: "callout"},
		{name: "home", raw: []byte{0x82, 0x02, 0x01, 0x01, 'H', 'O', 'M', 'E', ':'}, want: "home_indicator"},
		{name: "home-hmd", raw: []byte{0xdc, 0x02, 0x01, 0x01, 'H', 'O', 'M', 'E'}, want: "home_indicator"},
		{name: "text", raw: []byte{0x82, 0x02, 0x01, 0x01, 'T'}, want: "text"},
		{name: "wap", raw: []byte{0x84, 0x0b, 0x84, 0x23, 0xf0}, want: "wap_push"},
		{name: "udh", raw: []byte{0x8a, 0x00}, want: "udh"},
		{name: "raw", raw: []byte{0x01}, want: "raw(0x01)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectSDSKind(tc.raw); got != tc.want {
				t.Fatalf("detectSDSKind(%x)=%q want=%q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestBuildSDSFramePayload_ValenceCompatHeader(t *testing.T) {
	sdsPayload := []byte{0x89, 0x02, 0x01, 0x01, 'A'}
	framePayload := buildSDSFramePayload(900000, 10001, sdsPayload)

	if len(framePayload) != 8+len(sdsPayload) {
		t.Fatalf("unexpected frame payload length: %d", len(framePayload))
	}
	if got := binary.LittleEndian.Uint32(framePayload[0:4]); got != 900000 {
		t.Fatalf("unexpected source in frame payload: %d", got)
	}
	if got := binary.LittleEndian.Uint32(framePayload[4:8]); got != 10001 {
		t.Fatalf("unexpected destination in frame payload: %d", got)
	}
	if got := framePayload[8:]; hex.EncodeToString(got) != hex.EncodeToString(sdsPayload) {
		t.Fatalf("unexpected wrapped sds payload: %x", got)
	}
}

func TestDetectSDSKind_WithFrameHeader(t *testing.T) {
	wrapped := buildSDSFramePayload(900000, 10001, []byte{0xC3, 0x00, 0x01, 0x01, 0x10, 0x01, 0x00, 't', 'e', 's', 't'})
	if got := detectSDSKind(wrapped); got != "callout" {
		t.Fatalf("detectSDSKind(wrapped)=%q want=%q", got, "callout")
	}
}

func TestParseCalloutPayloadAndEnvelope(t *testing.T) {
	raw := []byte{0xC3, 0x24, 0x11, 0x81, 0x10, 0x4d, 0xa0, 0x80, 'o', 'k'}
	msg, ok := parseCalloutPayload(raw)
	if !ok {
		t.Fatalf("expected parseCalloutPayload success")
	}
	if msg.MessageType != 2 || msg.DeliveryReportRequest != 1 || msg.MessageRef != 0x11 {
		t.Fatalf("unexpected parsed callout header: %+v", msg)
	}
	if msg.CalloutNumber != 0x4d {
		t.Fatalf("callout_number=%d want=%d", msg.CalloutNumber, 0x4d)
	}
	env := parseSDSFrameEnvelope(buildSDSFramePayload(101, 202, raw))
	if !env.Wrapped || env.Source != 101 || env.Destination != 202 {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if got := string(env.Payload[len(env.Payload)-2:]); got != "ok" {
		t.Fatalf("unexpected envelope payload text: %q", got)
	}
}

func TestParseCalloutPayload_LegacyMissingControlByte(t *testing.T) {
	// Field sample: callout text starts right after octet 5.
	raw := []byte{0xC3, 0x00, 0x0A, 0x01, 0x38, 0x00, 'R', 'e', 'j', 'e', 'c', 't'}
	msg, ok := parseCalloutPayload(raw)
	if !ok {
		t.Fatalf("expected parseCalloutPayload success")
	}
	if msg.CalloutNumber != 8 {
		t.Fatalf("callout_number=%d want=8", msg.CalloutNumber)
	}
	if msg.Function != 3 {
		t.Fatalf("function=%d want=3", msg.Function)
	}
	if msg.EndCallout {
		t.Fatalf("end_callout must be false for legacy short-header sample")
	}
	if msg.Text != "Reject" {
		t.Fatalf("text=%q want=%q", msg.Text, "Reject")
	}
}

func TestBuildCalloutPayload_UsesFull8BitCalloutNumber(t *testing.T) {
	sdsMessageRefCounter.Store(0)
	opts := defaultCalloutEncodeOptions()
	opts.CalloutNumber = 200
	opts.ExtensionHeader = true
	payload, err := buildSDSPayloadWithOptions("callout", "x", "", opts)
	if err != nil {
		t.Fatalf("buildSDSPayloadWithOptions error: %v", err)
	}
	msg, ok := parseCalloutPayload(payload)
	if !ok {
		t.Fatalf("parseCalloutPayload failed")
	}
	if msg.CalloutNumber != 200 {
		t.Fatalf("callout_number=%d want=200", msg.CalloutNumber)
	}
}

// TestParseSDSFrameEnvelope_UnwrappedHasZeroSource pins down the behaviour
// that triggered the LIP-position regression: BlueStations typically send
// SDS frames starting directly with the SDS-TL protocol ID, without the
// 8-byte (source, destination) wrapper that parseSDSFrameEnvelope looks for.
// In that case env.Source must come back as 0, which is why the SDS handler
// in service.go has to fall back to the call context BEFORE invoking the
// LIP hook.
func TestParseSDSFrameEnvelope_UnwrappedHasZeroSource(t *testing.T) {
	// A LIP short-report SDS frame starts with protocol ID 0x0A.
	unwrapped := []byte{0x0A, 0x00, 0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE}
	env := parseSDSFrameEnvelope(unwrapped)
	if env.Wrapped {
		t.Fatalf("expected unwrapped SDS to be detected as Wrapped=false, got Wrapped=true")
	}
	if env.Source != 0 || env.Destination != 0 {
		t.Fatalf("unwrapped SDS must yield zero Source/Destination, got src=%d dst=%d", env.Source, env.Destination)
	}
	if len(env.Payload) != len(unwrapped) {
		t.Fatalf("unwrapped payload must equal input, got %d want %d bytes", len(env.Payload), len(unwrapped))
	}
}

// TestParseSDSFrameEnvelope_WrappedExtractsSource verifies the happy-path
// wrapper detection still works (8 bytes of LE source/dest followed by a
// recognisable SDS protocol ID).
func TestParseSDSFrameEnvelope_WrappedExtractsSource(t *testing.T) {
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, 2623563) // source
	data = binary.LittleEndian.AppendUint32(data, 262999)  // destination
	data = append(data, 0x0A, 0x00, 0x12, 0x34)            // LIP protocol ID + filler
	env := parseSDSFrameEnvelope(data)
	if !env.Wrapped {
		t.Fatalf("expected wrapped SDS to be detected as Wrapped=true")
	}
	if env.Source != 2623563 {
		t.Fatalf("source=%d want=2623563", env.Source)
	}
	if env.Destination != 262999 {
		t.Fatalf("destination=%d want=262999", env.Destination)
	}
	if len(env.Payload) != 4 || env.Payload[0] != 0x0A {
		t.Fatalf("payload incorrect, got % x", env.Payload)
	}
}
