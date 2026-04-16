package brew

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	ClassSubscriber  uint8 = 0xF0
	ClassCallControl uint8 = 0xF1
	ClassFrame       uint8 = 0xF2
	ClassError       uint8 = 0xF3
	ClassService     uint8 = 0xF4
)

const (
	SubscriberDeregister  uint8 = 0
	SubscriberRegister    uint8 = 1
	SubscriberReregister  uint8 = 2
	SubscriberAffiliate   uint8 = 8
	SubscriberDeaffiliate uint8 = 9
)

const (
	CallStateGroupTX        uint8 = 2
	CallStateGroupIdle      uint8 = 3
	CallStateSetupRequest   uint8 = 4
	CallStateSetupAccept    uint8 = 5
	CallStateSetupReject    uint8 = 6
	CallStateCallAlert      uint8 = 7
	CallStateConnectRequest uint8 = 8
	CallStateConnectConfirm uint8 = 9
	CallStateCallRelease    uint8 = 10
	CallStateShortTransfer  uint8 = 11
	CallStateSimplexGranted uint8 = 12
	CallStateSimplexIdle    uint8 = 13
	CallStatePDPRequest     uint8 = 14
	CallStatePDPAccept      uint8 = 15
	CallStatePDPReject      uint8 = 16
	CallStatePDPRelease     uint8 = 17
)

const (
	PDPFlagIPv4 uint8 = 1 << 0
	PDPFlagIPv6 uint8 = 1 << 1
)

const (
	FrameTypeTrafficChannel uint8 = 0
	FrameTypeSDSTransfer    uint8 = 1
	FrameTypeSDSReport      uint8 = 2
	FrameTypeDTMFData       uint8 = 3
	FrameTypePacketData     uint8 = 4
)

const (
	ErrorTypeMalformed  uint8 = 0
	ErrorTypeRestricted uint8 = 1
)

const (
	ServiceTypeQuerySubscribers    uint8 = 1
	ServiceTypeAttachmentControlV1 uint8 = 128
)

type ParsedMessage interface {
	isParsedMessage()
}

type SubscriberMessage struct {
	MsgType  uint8
	Number   uint32
	Time     uint64
	Fraction uint32
	Groups   []uint32
}

func (*SubscriberMessage) isParsedMessage() {}

type GroupTransmissionPayload struct {
	Source      uint32
	Destination uint32
	Priority    uint8
	Access      uint8
	Service     uint16
}

type CircularCallPayload struct {
	Source        uint32
	Destination   uint32
	Number        string
	Priority      uint8
	Service       uint8
	Mode          uint8
	Duplex        uint8
	Method        uint8
	Communication uint8
	Grant         uint8
	Permission    uint8
	Timeout       uint8
	Ownership     uint8
	Queued        uint8
}

type CircularGrantPayload struct {
	Grant      uint8
	Permission uint8
}

type ShortDataPayload struct {
	Source      uint32
	Destination uint32
	Number      string
}

type ShortTransferStatusPayload struct {
	PreCodedStatus uint16
}

type PacketRequestPayload struct {
	Number        uint32
	Flags         uint8
	Profile       uint32
	Authenticator []byte
}

type PacketContextPayload struct {
	Flags   uint8
	IPv4    uint32
	IPv6    [16]byte
	Profile uint32
}

type CausePayload struct {
	Cause uint8
}

type EmptyPayload struct{}

type RawPayload struct {
	Data []byte
}

type CallControlMessage struct {
	CallState  uint8
	Identifier uuid.UUID
	Payload    any
}

func (*CallControlMessage) isParsedMessage() {}

type FrameMessage struct {
	FrameType  uint8
	Identifier uuid.UUID
	LengthBits uint16
	Data       []byte
}

func (*FrameMessage) isParsedMessage() {}

type ErrorMessage struct {
	ErrorType uint8
	Data      []byte
}

func (*ErrorMessage) isParsedMessage() {}

type ServiceMessage struct {
	ServiceType uint8
	JSONData    string
}

func (*ServiceMessage) isParsedMessage() {}

var ErrTooShort = errors.New("brew packet too short")

func ParseMessage(data []byte) (ParsedMessage, error) {
	if len(data) < 2 {
		return nil, ErrTooShort
	}

	class := data[0]
	msgType := data[1]

	switch class {
	case ClassSubscriber:
		return parseSubscriber(msgType, data)
	case ClassCallControl:
		return parseCallControl(msgType, data)
	case ClassFrame:
		return parseFrame(msgType, data)
	case ClassError:
		return &ErrorMessage{ErrorType: msgType, Data: append([]byte(nil), data[2:]...)}, nil
	case ClassService:
		end := len(data)
		for i := 2; i < len(data); i++ {
			if data[i] == 0 {
				end = i
				break
			}
		}
		return &ServiceMessage{ServiceType: msgType, JSONData: string(data[2:end])}, nil
	default:
		return nil, fmt.Errorf("unknown brew class: 0x%02x", class)
	}
}

func parseSubscriber(msgType uint8, data []byte) (*SubscriberMessage, error) {
	if len(data) < 18 {
		return nil, ErrTooShort
	}
	msg := &SubscriberMessage{
		MsgType:  msgType,
		Number:   binary.LittleEndian.Uint32(data[2:6]),
		Time:     binary.LittleEndian.Uint64(data[6:14]),
		Fraction: binary.LittleEndian.Uint32(data[14:18]),
		Groups:   make([]uint32, 0, (len(data)-18)/4),
	}
	for i := 18; i+4 <= len(data); i += 4 {
		msg.Groups = append(msg.Groups, binary.LittleEndian.Uint32(data[i:i+4]))
	}
	return msg, nil
}

func parseCallControl(state uint8, data []byte) (*CallControlMessage, error) {
	if len(data) < 18 {
		return nil, ErrTooShort
	}
	id, err := uuid.FromBytes(data[2:18])
	if err != nil {
		return nil, fmt.Errorf("invalid call uuid: %w", err)
	}

	payload := data[18:]
	msg := &CallControlMessage{CallState: state, Identifier: id}

	switch state {
	case CallStateGroupTX:
		if len(payload) < 12 {
			return nil, ErrTooShort
		}
		msg.Payload = GroupTransmissionPayload{
			Source:      binary.LittleEndian.Uint32(payload[0:4]),
			Destination: binary.LittleEndian.Uint32(payload[4:8]),
			Priority:    payload[8],
			Access:      payload[9],
			Service:     binary.LittleEndian.Uint16(payload[10:12]),
		}
	case CallStateGroupIdle, CallStateSetupReject, CallStateCallRelease, CallStatePDPReject, CallStatePDPRelease:
		if len(payload) < 1 {
			return nil, ErrTooShort
		}
		msg.Payload = CausePayload{Cause: payload[0]}
	case CallStateSetupAccept, CallStateCallAlert:
		msg.Payload = EmptyPayload{}
	case CallStateSetupRequest, CallStateConnectRequest:
		if len(payload) < 51 {
			return nil, ErrTooShort
		}
		msg.Payload = CircularCallPayload{
			Source:        binary.LittleEndian.Uint32(payload[0:4]),
			Destination:   binary.LittleEndian.Uint32(payload[4:8]),
			Number:        parseFixedString(payload[8:40]),
			Priority:      payload[40],
			Service:       payload[41],
			Mode:          payload[42],
			Duplex:        payload[43],
			Method:        payload[44],
			Communication: payload[45],
			Grant:         payload[46],
			Permission:    payload[47],
			Timeout:       payload[48],
			Ownership:     payload[49],
			Queued:        payload[50],
		}
	case CallStateConnectConfirm, CallStateSimplexGranted, CallStateSimplexIdle:
		if len(payload) < 2 {
			return nil, ErrTooShort
		}
		msg.Payload = CircularGrantPayload{Grant: payload[0], Permission: payload[1]}
	case CallStateShortTransfer:
		// Two forms are used in the wild:
		// 1) 40-byte BrewShortData structure (source, destination, number)
		// 2) 2-byte pre-coded status preamble (used before SDS frames)
		if len(payload) >= 40 {
			msg.Payload = ShortDataPayload{
				Source:      binary.LittleEndian.Uint32(payload[0:4]),
				Destination: binary.LittleEndian.Uint32(payload[4:8]),
				Number:      parseFixedString(payload[8:40]),
			}
		} else if len(payload) >= 2 {
			msg.Payload = ShortTransferStatusPayload{
				PreCodedStatus: binary.LittleEndian.Uint16(payload[0:2]),
			}
		} else {
			return nil, ErrTooShort
		}
	case CallStatePDPRequest:
		if len(payload) < 9 {
			return nil, ErrTooShort
		}
		msg.Payload = PacketRequestPayload{
			Number:        binary.LittleEndian.Uint32(payload[0:4]),
			Flags:         payload[4],
			Profile:       binary.LittleEndian.Uint32(payload[5:9]),
			Authenticator: append([]byte(nil), payload[9:]...),
		}
	case CallStatePDPAccept:
		if len(payload) < 25 {
			return nil, ErrTooShort
		}
		ctx := PacketContextPayload{
			Flags: payload[0],
			IPv4:  binary.LittleEndian.Uint32(payload[1:5]),
		}
		copy(ctx.IPv6[:], payload[5:21])
		ctx.Profile = binary.LittleEndian.Uint32(payload[21:25])
		msg.Payload = ctx
	default:
		msg.Payload = RawPayload{Data: append([]byte(nil), payload...)}
	}

	return msg, nil
}

func parseFrame(frameType uint8, data []byte) (*FrameMessage, error) {
	if len(data) < 20 {
		return nil, ErrTooShort
	}
	id, err := uuid.FromBytes(data[2:18])
	if err != nil {
		return nil, fmt.Errorf("invalid frame uuid: %w", err)
	}
	lengthBits := binary.LittleEndian.Uint16(data[18:20])
	payload := append([]byte(nil), data[20:]...)
	expectedBytes := payloadBytesForBits(lengthBits)
	if len(payload) < expectedBytes {
		return nil, fmt.Errorf("frame payload too short: need=%d have=%d", expectedBytes, len(payload))
	}
	if len(payload) > expectedBytes {
		if (frameType == FrameTypeSDSTransfer || frameType == FrameTypeSDSReport) && len(payload) >= expectedBytes+8 {
			// Legacy SDS framing may prepend source/destination metadata while length_bits
			// still describes only SDS payload size.
			payload = payload[:expectedBytes+8]
		} else {
			payload = payload[:expectedBytes]
		}
	}
	if err := validateFramePayload(frameType, payload); err != nil {
		return nil, err
	}

	return &FrameMessage{
		FrameType:  frameType,
		Identifier: id,
		LengthBits: lengthBits,
		Data:       payload,
	}, nil
}

func payloadBytesForBits(lengthBits uint16) int {
	return int((uint32(lengthBits) + 7) / 8)
}

func normalizePayloadForBits(lengthBits uint16, payload []byte) []byte {
	expected := payloadBytesForBits(lengthBits)
	if expected <= 0 {
		return nil
	}
	if len(payload) >= expected {
		return append([]byte(nil), payload[:expected]...)
	}
	out := make([]byte, expected)
	copy(out, payload)
	return out
}

func validateFramePayload(frameType uint8, payload []byte) error {
	switch frameType {
	case FrameTypeTrafficChannel:
		if len(payload) == 0 {
			return fmt.Errorf("traffic frame payload is empty")
		}
	case FrameTypeSDSTransfer:
		if len(payload) == 0 {
			return fmt.Errorf("sds transfer payload is empty")
		}
	case FrameTypeSDSReport:
		// 1-byte status code (modern) or 8-byte route header + 1-byte status (legacy).
		if len(payload) != 1 && len(payload) < 9 {
			return fmt.Errorf("sds report payload must be 1 or >=9 bytes, got=%d", len(payload))
		}
	case FrameTypeDTMFData:
		if len(payload) != 1 {
			return fmt.Errorf("dtmf payload must be exactly 1 byte, got=%d", len(payload))
		}
		if !isDTMFASCII(payload[0]) {
			return fmt.Errorf("invalid dtmf payload byte 0x%02x", payload[0])
		}
	case FrameTypePacketData:
		if len(payload) == 0 {
			return fmt.Errorf("packet payload is empty")
		}
		version := payload[0] >> 4
		if version != 4 && version != 6 {
			return fmt.Errorf("packet payload is not IPv4/IPv6 (version nibble=%d)", version)
		}
	}
	return nil
}

func isDTMFASCII(v byte) bool {
	switch v {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '*', '#', 'A', 'B', 'C', 'D':
		return true
	default:
		return false
	}
}

func parseFixedString(raw []byte) string {
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	return string(raw)
}

func appendFixedString(dst []byte, value string, size int) []byte {
	buf := make([]byte, size)
	copy(buf, []byte(value))
	return append(dst, buf...)
}

func BuildSubscriber(action uint8, issi uint32, groups []uint32) []byte {
	now := time.Now().UTC()
	out := make([]byte, 0, 18+4*len(groups))
	out = append(out, ClassSubscriber, action)
	out = binary.LittleEndian.AppendUint32(out, issi)
	out = binary.LittleEndian.AppendUint64(out, uint64(now.Unix()))
	out = binary.LittleEndian.AppendUint32(out, uint32(now.Nanosecond()))
	for _, g := range groups {
		out = binary.LittleEndian.AppendUint32(out, g)
	}
	return out
}

func BuildSubscriberRegister(issi uint32, groups []uint32) []byte {
	return BuildSubscriber(SubscriberRegister, issi, groups)
}

func BuildSubscriberReregister(issi uint32, groups []uint32) []byte {
	return BuildSubscriber(SubscriberReregister, issi, groups)
}

func BuildSubscriberAffiliate(issi uint32, groups []uint32) []byte {
	return BuildSubscriber(SubscriberAffiliate, issi, groups)
}

func BuildSubscriberDeaffiliate(issi uint32, groups []uint32) []byte {
	return BuildSubscriber(SubscriberDeaffiliate, issi, groups)
}

func BuildSubscriberDeregister(issi uint32) []byte {
	return BuildSubscriber(SubscriberDeregister, issi, nil)
}

func BuildCallControl(state uint8, callID uuid.UUID, payload []byte) []byte {
	out := make([]byte, 0, 18+len(payload))
	out = append(out, ClassCallControl, state)
	out = append(out, callID[:]...)
	out = append(out, payload...)
	return out
}

func BuildCallControlNoPayload(state uint8, callID uuid.UUID) []byte {
	return BuildCallControl(state, callID, nil)
}

func BuildCallControlWithCause(state uint8, callID uuid.UUID, cause uint8) []byte {
	return BuildCallControl(state, callID, []byte{cause})
}

func BuildCallControlFromMessage(m *CallControlMessage) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("nil call control message")
	}

	switch p := m.Payload.(type) {
	case GroupTransmissionPayload:
		if m.CallState != CallStateGroupTX {
			return nil, fmt.Errorf("group payload with call state=%d", m.CallState)
		}
		return BuildGroupTXWithAccess(m.Identifier, p.Source, p.Destination, p.Priority, p.Access, p.Service), nil
	case CircularCallPayload:
		return BuildCircularCall(m.CallState, m.Identifier, p), nil
	case CircularGrantPayload:
		return BuildCircularGrant(m.CallState, m.Identifier, p), nil
	case ShortDataPayload:
		return BuildShortData(m.Identifier, p), nil
	case ShortTransferStatusPayload:
		if m.CallState != CallStateShortTransfer {
			return nil, fmt.Errorf("short-transfer status payload with call state=%d", m.CallState)
		}
		return BuildShortTransferStatus(m.Identifier, p.PreCodedStatus), nil
	case PacketRequestPayload:
		return BuildPacketRequest(m.Identifier, p), nil
	case PacketContextPayload:
		return BuildPacketContext(m.Identifier, p), nil
	case CausePayload:
		return BuildCallControlWithCause(m.CallState, m.Identifier, p.Cause), nil
	case EmptyPayload:
		return BuildCallControlNoPayload(m.CallState, m.Identifier), nil
	case RawPayload:
		return BuildCallControl(m.CallState, m.Identifier, p.Data), nil
	case []byte:
		return BuildCallControl(m.CallState, m.Identifier, p), nil
	case nil:
		return BuildCallControlNoPayload(m.CallState, m.Identifier), nil
	default:
		return nil, fmt.Errorf("unsupported call payload type %T", m.Payload)
	}
}

func BuildGroupTX(callID uuid.UUID, sourceISSI, destGSSI uint32, priority uint8, service uint16) []byte {
	return BuildGroupTXWithAccess(callID, sourceISSI, destGSSI, priority, 0, service)
}

func BuildGroupTXWithAccess(callID uuid.UUID, sourceISSI, destGSSI uint32, priority uint8, access uint8, service uint16) []byte {
	payload := make([]byte, 0, 12)
	payload = binary.LittleEndian.AppendUint32(payload, sourceISSI)
	payload = binary.LittleEndian.AppendUint32(payload, destGSSI)
	payload = append(payload, priority, access)
	payload = binary.LittleEndian.AppendUint16(payload, service)
	return BuildCallControl(CallStateGroupTX, callID, payload)
}

func BuildCircularCall(state uint8, callID uuid.UUID, p CircularCallPayload) []byte {
	payload := make([]byte, 0, 51)
	payload = binary.LittleEndian.AppendUint32(payload, p.Source)
	payload = binary.LittleEndian.AppendUint32(payload, p.Destination)
	payload = appendFixedString(payload, p.Number, 32)
	payload = append(payload, p.Priority, p.Service, p.Mode, p.Duplex, p.Method, p.Communication)
	payload = append(payload, p.Grant, p.Permission, p.Timeout, p.Ownership, p.Queued)
	return BuildCallControl(state, callID, payload)
}

func BuildCircularGrant(state uint8, callID uuid.UUID, p CircularGrantPayload) []byte {
	return BuildCallControl(state, callID, []byte{p.Grant, p.Permission})
}

func BuildShortData(callID uuid.UUID, p ShortDataPayload) []byte {
	payload := make([]byte, 0, 40)
	payload = binary.LittleEndian.AppendUint32(payload, p.Source)
	payload = binary.LittleEndian.AppendUint32(payload, p.Destination)
	payload = appendFixedString(payload, p.Number, 32)
	return BuildCallControl(CallStateShortTransfer, callID, payload)
}

func BuildShortTransferStatus(callID uuid.UUID, preCodedStatus uint16) []byte {
	payload := make([]byte, 0, 2)
	payload = binary.LittleEndian.AppendUint16(payload, preCodedStatus)
	return BuildCallControl(CallStateShortTransfer, callID, payload)
}

func BuildPacketRequest(callID uuid.UUID, p PacketRequestPayload) []byte {
	payload := make([]byte, 0, 9+len(p.Authenticator))
	payload = binary.LittleEndian.AppendUint32(payload, p.Number)
	payload = append(payload, p.Flags)
	payload = binary.LittleEndian.AppendUint32(payload, p.Profile)
	payload = append(payload, p.Authenticator...)
	return BuildCallControl(CallStatePDPRequest, callID, payload)
}

func BuildPacketContext(callID uuid.UUID, p PacketContextPayload) []byte {
	payload := make([]byte, 0, 25)
	payload = append(payload, p.Flags)
	payload = binary.LittleEndian.AppendUint32(payload, p.IPv4)
	payload = append(payload, p.IPv6[:]...)
	payload = binary.LittleEndian.AppendUint32(payload, p.Profile)
	return BuildCallControl(CallStatePDPAccept, callID, payload)
}

func BuildGroupIdle(callID uuid.UUID, cause uint8) []byte {
	return BuildCallControlWithCause(CallStateGroupIdle, callID, cause)
}

func BuildSetupReject(callID uuid.UUID, cause uint8) []byte {
	return BuildCallControlWithCause(CallStateSetupReject, callID, cause)
}

func BuildCallRelease(callID uuid.UUID, cause uint8) []byte {
	return BuildCallControlWithCause(CallStateCallRelease, callID, cause)
}

func BuildPDPReject(callID uuid.UUID, cause uint8) []byte {
	return BuildCallControlWithCause(CallStatePDPReject, callID, cause)
}

func BuildPDPRelease(callID uuid.UUID, cause uint8) []byte {
	return BuildCallControlWithCause(CallStatePDPRelease, callID, cause)
}

func BuildSetupAccept(callID uuid.UUID) []byte {
	return BuildCallControlNoPayload(CallStateSetupAccept, callID)
}

func BuildCallAlert(callID uuid.UUID) []byte {
	return BuildCallControlNoPayload(CallStateCallAlert, callID)
}

func BuildSetupRequest(callID uuid.UUID, p CircularCallPayload) []byte {
	return BuildCircularCall(CallStateSetupRequest, callID, p)
}

func BuildConnectRequest(callID uuid.UUID, p CircularCallPayload) []byte {
	return BuildCircularCall(CallStateConnectRequest, callID, p)
}

func BuildConnectConfirm(callID uuid.UUID, p CircularGrantPayload) []byte {
	return BuildCircularGrant(CallStateConnectConfirm, callID, p)
}

func BuildSimplexGranted(callID uuid.UUID, p CircularGrantPayload) []byte {
	return BuildCircularGrant(CallStateSimplexGranted, callID, p)
}

func BuildSimplexIdle(callID uuid.UUID, p CircularGrantPayload) []byte {
	return BuildCircularGrant(CallStateSimplexIdle, callID, p)
}

func BuildFrame(frameType uint8, callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	if lengthBits == 0 && len(payload) > 0 {
		bits := len(payload) * 8
		if bits > 0xFFFF {
			bits = 0xFFFF
		}
		lengthBits = uint16(bits)
	}
	normalizedPayload := normalizeFramePayload(frameType, lengthBits, payload)
	out := make([]byte, 0, 20+len(normalizedPayload))
	out = append(out, ClassFrame, frameType)
	out = append(out, callID[:]...)
	out = binary.LittleEndian.AppendUint16(out, lengthBits)
	out = append(out, normalizedPayload...)
	return out
}

func normalizeFramePayload(frameType uint8, lengthBits uint16, payload []byte) []byte {
	expected := payloadBytesForBits(lengthBits)
	if expected <= 0 {
		return nil
	}
	if (frameType == FrameTypeSDSTransfer || frameType == FrameTypeSDSReport) && len(payload) >= expected+8 {
		return append([]byte(nil), payload[:expected+8]...)
	}
	return normalizePayloadForBits(lengthBits, payload)
}

func BuildVoiceFrame(callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	return BuildFrame(FrameTypeTrafficChannel, callID, lengthBits, payload)
}

func BuildSDSFrame(callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	return BuildFrame(FrameTypeSDSTransfer, callID, lengthBits, payload)
}

func BuildSDSReportFrame(callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	return BuildFrame(FrameTypeSDSReport, callID, lengthBits, payload)
}

func BuildDTMFFrame(callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	return BuildFrame(FrameTypeDTMFData, callID, lengthBits, payload)
}

func BuildPacketDataFrame(callID uuid.UUID, lengthBits uint16, payload []byte) []byte {
	return BuildFrame(FrameTypePacketData, callID, lengthBits, payload)
}

func BuildError(errorType uint8, payload []byte) []byte {
	out := make([]byte, 0, 2+len(payload))
	out = append(out, ClassError, errorType)
	out = append(out, payload...)
	return out
}

func BuildService(serviceType uint8, jsonData string) []byte {
	out := make([]byte, 0, 3+len(jsonData))
	out = append(out, ClassService, serviceType)
	out = append(out, jsonData...)
	out = append(out, 0)
	return out
}

func BuildQuerySubscribers(issis []uint32) []byte {
	b, err := json.Marshal(issis)
	if err != nil {
		return BuildService(ServiceTypeQuerySubscribers, "[]")
	}
	return BuildService(ServiceTypeQuerySubscribers, string(b))
}

func BuildAttachmentControl(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return BuildService(ServiceTypeAttachmentControlV1, "{}")
	}
	return BuildService(ServiceTypeAttachmentControlV1, string(b))
}
