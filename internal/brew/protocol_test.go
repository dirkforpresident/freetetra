package brew

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseGroupTX(t *testing.T) {
	callID := uuid.New()
	wire := BuildGroupTX(callID, 1001, 26, 3, 0)

	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}

	cc, ok := msg.(*CallControlMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if cc.CallState != CallStateGroupTX {
		t.Fatalf("call state = %d", cc.CallState)
	}
	if cc.Identifier != callID {
		t.Fatalf("uuid mismatch: %s != %s", cc.Identifier, callID)
	}
	payload, ok := cc.Payload.(GroupTransmissionPayload)
	if !ok {
		t.Fatalf("payload type = %T", cc.Payload)
	}
	if payload.Source != 1001 || payload.Destination != 26 || payload.Priority != 3 {
		t.Fatalf("unexpected payload = %#v", payload)
	}
}

func TestParseVoiceFrame(t *testing.T) {
	callID := uuid.New()
	payload := make([]byte, 36)
	payload[0] = 0x80
	wire := BuildVoiceFrame(callID, 288, payload)

	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	frame, ok := msg.(*FrameMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if frame.Identifier != callID {
		t.Fatalf("uuid mismatch: %s != %s", frame.Identifier, callID)
	}
	if frame.LengthBits != 288 {
		t.Fatalf("length bits = %d", frame.LengthBits)
	}
	if len(frame.Data) != 36 {
		t.Fatalf("frame bytes = %d", len(frame.Data))
	}
}

func TestParseSetupRequest(t *testing.T) {
	callID := uuid.New()
	wire := BuildSetupRequest(callID, CircularCallPayload{
		Source:        1001,
		Destination:   2002,
		Number:        "N0CALL",
		Priority:      3,
		Service:       1,
		Mode:          2,
		Duplex:        1,
		Method:        1,
		Communication: 1,
		Grant:         1,
		Permission:    1,
		Timeout:       10,
		Ownership:     0,
		Queued:        0,
	})

	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	cc, ok := msg.(*CallControlMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if cc.CallState != CallStateSetupRequest {
		t.Fatalf("call state = %d", cc.CallState)
	}
	payload, ok := cc.Payload.(CircularCallPayload)
	if !ok {
		t.Fatalf("payload type = %T", cc.Payload)
	}
	if payload.Source != 1001 || payload.Destination != 2002 || payload.Number != "N0CALL" {
		t.Fatalf("unexpected payload = %#v", payload)
	}
}

func TestBuildCallControlFromMessage(t *testing.T) {
	callID := uuid.New()
	in := &CallControlMessage{
		CallState:  CallStateConnectConfirm,
		Identifier: callID,
		Payload: CircularGrantPayload{
			Grant:      2,
			Permission: 1,
		},
	}

	wire, err := BuildCallControlFromMessage(in)
	if err != nil {
		t.Fatalf("BuildCallControlFromMessage() error = %v", err)
	}
	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	cc, ok := msg.(*CallControlMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	payload, ok := cc.Payload.(CircularGrantPayload)
	if !ok {
		t.Fatalf("payload type = %T", cc.Payload)
	}
	if payload.Grant != 2 || payload.Permission != 1 {
		t.Fatalf("unexpected payload = %#v", payload)
	}
}

func TestBuildQuerySubscribers(t *testing.T) {
	wire := BuildQuerySubscribers([]uint32{1234567})
	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	svc, ok := msg.(*ServiceMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if svc.ServiceType != ServiceTypeQuerySubscribers {
		t.Fatalf("service type = %d", svc.ServiceType)
	}
	if svc.JSONData != "[1234567]" {
		t.Fatalf("json data = %q", svc.JSONData)
	}
}

func TestBuildAttachmentControl(t *testing.T) {
	wire := BuildAttachmentControl(map[string]any{
		"event": "subscriber_update",
		"gssi":  10001,
	})
	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	svc, ok := msg.(*ServiceMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if svc.ServiceType != ServiceTypeAttachmentControlV1 {
		t.Fatalf("service type = %d", svc.ServiceType)
	}
	if svc.JSONData == "" {
		t.Fatalf("json data should not be empty")
	}
}

func TestParseSDSReportFrameRequiresSingleByte(t *testing.T) {
	callID := uuid.New()
	wire := BuildSDSReportFrame(callID, 16, []byte{0x00, 0x01})

	if _, err := ParseMessage(wire); err == nil {
		t.Fatalf("expected ParseMessage error for invalid SDS report payload length")
	}
}

func TestParseDTMFFrameRequiresValidASCII(t *testing.T) {
	callID := uuid.New()
	wire := BuildDTMFFrame(callID, 8, []byte{'Z'})

	if _, err := ParseMessage(wire); err == nil {
		t.Fatalf("expected ParseMessage error for invalid DTMF byte")
	}
}

func TestParsePacketFrameRequiresIPv4OrIPv6(t *testing.T) {
	callID := uuid.New()
	wire := BuildPacketDataFrame(callID, 8, []byte{0x10})

	if _, err := ParseMessage(wire); err == nil {
		t.Fatalf("expected ParseMessage error for non-IP packet payload")
	}
}

func TestBuildFrameNormalizesPayloadToLengthBits(t *testing.T) {
	callID := uuid.New()
	wire := BuildFrame(FrameTypePacketData, callID, 12, []byte{0x45, 0x11, 0x22})

	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	frame, ok := msg.(*FrameMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if frame.LengthBits != 12 {
		t.Fatalf("length bits = %d", frame.LengthBits)
	}
	if len(frame.Data) != 2 {
		t.Fatalf("frame data len = %d", len(frame.Data))
	}
	if frame.Data[0] != 0x45 || frame.Data[1] != 0x11 {
		t.Fatalf("unexpected frame data = %x", frame.Data)
	}
}

func TestParseShortTransferStatusPreamble(t *testing.T) {
	callID := uuid.New()
	wire := BuildShortTransferStatus(callID, 0x0000)

	msg, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	cc, ok := msg.(*CallControlMessage)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if cc.CallState != CallStateShortTransfer {
		t.Fatalf("call state = %d", cc.CallState)
	}
	p, ok := cc.Payload.(ShortTransferStatusPayload)
	if !ok {
		t.Fatalf("payload type = %T", cc.Payload)
	}
	if p.PreCodedStatus != 0 {
		t.Fatalf("unexpected pre-coded status = 0x%04x", p.PreCodedStatus)
	}
}
