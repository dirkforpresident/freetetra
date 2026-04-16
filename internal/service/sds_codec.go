package service

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
)

const (
	sdsProtocolTextMessagingTL      byte   = 130 // 0x82
	sdsProtocolWdpTL                byte   = 132 // 0x84
	sdsProtocolImmediateTextTL      byte   = 137 // 0x89
	sdsProtocolMessageWithUDH       byte   = 138 // 0x8a
	sdsProtocolHomeModeDisplay      byte   = 220 // 0xdc
	sdsProtocolCallout              byte   = 195 // 0xc3
	sdsTLMessageTypeSimpleTransfer  byte   = 0x02
	sdsTLCodingSchemeEightBitLatin1 byte   = 0x01
	sdsWapPushDstPort               uint16 = 2948
	sdsWapPushSrcPort               uint16 = 9200
)

var sdsMessageRefCounter atomic.Uint32

// All defined text encoding schemes, according to [AI] table 29.29.
type TextEncoding uint8

const (
	Packed7Bit TextEncoding = iota
	ISO8859_1
	ISO8859_2
	ISO8859_3
	ISO8859_4
	ISO8859_5
	ISO8859_6
	ISO8859_7
	ISO8859_8
	ISO8859_9
	ISO8859_10
	ISO8859_13
	ISO8859_14
	ISO8859_15
	CodePage437
	CodePage737
	CodePage850
	CodePage852
	CodePage855
	CodePage857
	CodePage860
	CodePage861
	CodePage863
	CodePage865
	CodePage866
	CodePage869
	UTF16BE
	VISCII
)

type sdsFrameEnvelope struct {
	Source      uint32
	Destination uint32
	Payload     []byte
	Wrapped     bool
}

type calloutEncodeOptions struct {
	MessageType           uint8
	DeliveryReportRequest uint8
	StorageAllowed        bool
	ExtensionHeader       bool
	TextCodingScheme      uint8
	Function              uint8
	CalloutNumber         uint8
	Severity              uint8
	GroupControl          uint8
	TimestampControl      bool
	UserReceiptControl    bool
	TextIsStatus          bool
	EndCallout            bool
	PTTNotAllowed         bool
}

type calloutMessage struct {
	MessageType           uint8
	DeliveryReportRequest uint8
	ServiceSelection      bool
	Storage               bool
	MessageRef            uint8
	ExtensionHeader       bool
	TextCodingScheme      uint8
	Function              uint8
	CalloutNumber         uint8
	Severity              uint8
	GroupControl          uint8
	TimestampControl      bool
	UserReceiptControl    bool
	TextIsStatus          bool
	EndCallout            bool
	PTTNotAllowed         bool
	Text                  string
}

func nextSDSMessageRef() byte {
	// SDS-TL reference is one octet; wrap-around is expected.
	return byte(sdsMessageRefCounter.Add(1))
}

func defaultCalloutEncodeOptions() calloutEncodeOptions {
	return calloutEncodeOptions{
		MessageType:           0,
		DeliveryReportRequest: 0,
		StorageAllowed:        false,
		// Keep legacy layout by default for interoperability; v2 can be enabled per-message.
		ExtensionHeader:       false,
		TextCodingScheme:      uint8(ISO8859_1),
		Function:              1,
		CalloutNumber:         0,
		Severity:              1,
		GroupControl:          0,
		TimestampControl:      false,
		UserReceiptControl:    false,
		TextIsStatus:          false,
		EndCallout:            false,
		PTTNotAllowed:         false,
	}
}

func detectSDSKind(data []byte) string {
	sdsPayload, _ := unwrapSDSPayload(data)
	if len(sdsPayload) == 0 {
		return "empty"
	}

	switch sdsPayload[0] {
	case sdsProtocolCallout:
		return "callout"
	case sdsProtocolImmediateTextTL:
		return "flash"
	case sdsProtocolTextMessagingTL:
		if strings.HasPrefix(strings.ToUpper(decodeSDSText(sdsPayload)), "HOME:") {
			return "home_indicator"
		}
		return "text"
	case sdsProtocolHomeModeDisplay:
		return "home_indicator"
	case sdsProtocolWdpTL:
		return "wap_push"
	case sdsProtocolMessageWithUDH:
		return "udh"
	default:
		return fmt.Sprintf("raw(0x%02x)", sdsPayload[0])
	}
}

func decodeSDSText(data []byte) string {
	// Common SDS-TL text layout:
	//   [protocol_id][type][msg_ref][coding_scheme][text...]
	// For robustness, only decode when header is present.
	if len(data) < 4 {
		return ""
	}
	return string(data[4:])
}

func buildSDSPayload(sdsType, text, payloadHex string) ([]byte, error) {
	return buildSDSPayloadWithOptions(sdsType, text, payloadHex, defaultCalloutEncodeOptions())
}

func buildSDSPayloadWithOptions(sdsType, text, payloadHex string, callout calloutEncodeOptions) ([]byte, error) {
	if raw := strings.TrimSpace(payloadHex); raw != "" {
		b, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(raw), "0x"))
		if err != nil {
			return nil, fmt.Errorf("payload_hex is invalid hex")
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("payload_hex decoded to empty payload")
		}
		return b, nil
	}

	switch sdsType {
	case "", "flash":
		return buildSDSTLTextPayload(sdsProtocolImmediateTextTL, normalizeSDSMessage(text, "FLASH")), nil
	case "callout":
		return buildCalloutPayload(normalizeSDSMessage(text, "CALL OUT"), callout), nil
	case "home_indicator":
		return buildSDSTLTextPayload(sdsProtocolHomeModeDisplay, normalizeSDSMessage(withPrefix(text, "HOME: "), "HOME")), nil
	case "wap_push":
		return buildWapPushPayload(normalizeSDSMessage(text, "WAP PUSH")), nil
	default:
		return nil, fmt.Errorf("unknown sds_type %q", sdsType)
	}
}

func normalizeSDSMessage(text, fallback string) []byte {
	msg := strings.TrimSpace(text)
	if msg == "" {
		msg = fallback
	}
	return []byte(msg)
}

func withPrefix(text, prefix string) string {
	msg := strings.TrimSpace(text)
	if msg == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToUpper(msg), strings.ToUpper(prefix)) {
		return msg
	}
	return prefix + msg
}

func buildSDSTLTextPayload(protocolID byte, userData []byte) []byte {
	out := make([]byte, 0, 4+len(userData))
	out = append(out, protocolID)
	out = append(out, sdsTLMessageTypeSimpleTransfer)
	out = append(out, nextSDSMessageRef())
	out = append(out, sdsTLCodingSchemeEightBitLatin1)
	out = append(out, userData...)
	return out
}

func buildWapPushPayload(userData []byte) []byte {
	out := make([]byte, 0, 1+4+len(userData))
	out = append(out, sdsProtocolWdpTL)
	ports := make([]byte, 4)
	binary.BigEndian.PutUint16(ports[0:2], sdsWapPushDstPort)
	binary.BigEndian.PutUint16(ports[2:4], sdsWapPushSrcPort)
	out = append(out, ports...)
	out = append(out, userData...)
	return out
}

func buildCalloutPayload(userData []byte, o calloutEncodeOptions) []byte {
	// SDS reference must always increase and wrap naturally at 255.
	msgRef := nextSDSMessageRef()
	// Service selection is fixed to 0 for callouts.
	b1 := byte((o.MessageType&0x0f)<<4 | (o.DeliveryReportRequest&0x03)<<2)
	if o.StorageAllowed {
		b1 |= 0x01
	}
	if o.ExtensionHeader {
		// v2: extension header bit set; call_out_number is full 8-bit octet.
		b3 := byte(0x80) | (o.TextCodingScheme & 0x7f)
		b4 := byte((o.Function & 0x0f) << 4)
		b5 := o.CalloutNumber
		b6 := byte((o.GroupControl & 0x03) << 6)
		b6 |= (o.Severity & 0x0f)
		if o.TimestampControl {
			b6 |= 0x20
		}
		if o.UserReceiptControl {
			b6 |= 0x10
		}
		b7 := byte(0)
		if o.TextIsStatus {
			b7 |= 0x80
		}
		if o.EndCallout {
			b7 |= 0x40
		}
		if o.PTTNotAllowed {
			b7 |= 0x20
		}

		out := make([]byte, 0, 8+len(userData))
		out = append(out, sdsProtocolCallout)
		out = append(out, b1)
		out = append(out, msgRef)
		out = append(out, b3)
		out = append(out, b4)
		out = append(out, b5)
		out = append(out, b6)
		out = append(out, b7)
		out = append(out, userData...)
		return out
	}

	// Legacy v1 layout: 4-bit function + 4-bit callout number.
	b3 := o.TextCodingScheme & 0x7f
	b4 := byte((o.Function&0x0f)<<4 | (o.CalloutNumber & 0x0f))
	b5 := byte((o.GroupControl&0x03)<<6 | (o.Severity & 0x0f))
	if o.TimestampControl {
		b5 |= 0x20
	}
	if o.UserReceiptControl {
		b5 |= 0x10
	}
	b6 := byte(0)
	if o.TextIsStatus {
		b6 |= 0x80
	}
	if o.EndCallout {
		b6 |= 0x40
	}
	if o.PTTNotAllowed {
		b6 |= 0x20
	}

	out := make([]byte, 0, 7+len(userData))
	out = append(out, sdsProtocolCallout)
	out = append(out, b1)
	out = append(out, msgRef)
	out = append(out, b3)
	out = append(out, b4)
	out = append(out, b5)
	out = append(out, b6)
	out = append(out, userData...)
	return out
}

func buildSDSFramePayload(source, destination uint32, sdsPayload []byte) []byte {
	out := make([]byte, 0, 8+len(sdsPayload))
	out = binary.LittleEndian.AppendUint32(out, source)
	out = binary.LittleEndian.AppendUint32(out, destination)
	out = append(out, sdsPayload...)
	return out
}

func parseSDSFrameEnvelope(data []byte) sdsFrameEnvelope {
	out := sdsFrameEnvelope{
		Payload: data,
		Wrapped: false,
	}
	if len(data) >= 9 {
		source := binary.LittleEndian.Uint32(data[0:4])
		destination := binary.LittleEndian.Uint32(data[4:8])
		if (source != 0 || destination != 0) && !isLikelySDSProtocolID(data[0]) && isLikelySDSProtocolID(data[8]) {
			out.Source = source
			out.Destination = destination
			out.Payload = data[8:]
			out.Wrapped = true
		}
	}
	return out
}

func unwrapSDSPayload(data []byte) ([]byte, bool) {
	env := parseSDSFrameEnvelope(data)
	if len(env.Payload) == 0 {
		return env.Payload, env.Wrapped
	}
	if isLikelySDSProtocolID(env.Payload[0]) {
		return env.Payload, env.Wrapped
	}
	return data, false
}

func parseCalloutPayload(payload []byte) (calloutMessage, bool) {
	if len(payload) < 6 || payload[0] != sdsProtocolCallout {
		return calloutMessage{}, false
	}
	// v2 canonical decode: extension header bit is set and header has dedicated
	// octets for call_out_number and control flags.
	if len(payload) >= 8 && payload[3]&0x80 != 0 {
		m := calloutMessage{
			MessageType:           (payload[1] >> 4) & 0x0f,
			DeliveryReportRequest: (payload[1] >> 2) & 0x03,
			ServiceSelection:      payload[1]&0x02 != 0,
			Storage:               payload[1]&0x01 != 0,
			MessageRef:            payload[2],
			ExtensionHeader:       true,
			TextCodingScheme:      payload[3] & 0x7f,
			Function:              (payload[4] >> 4) & 0x0f,
			CalloutNumber:         payload[5],
			Severity:              payload[6] & 0x0f,
			GroupControl:          (payload[6] >> 6) & 0x03,
			TimestampControl:      payload[6]&0x20 != 0,
			UserReceiptControl:    payload[6]&0x10 != 0,
			TextIsStatus:          payload[7]&0x80 != 0,
			EndCallout:            payload[7]&0x40 != 0,
			PTTNotAllowed:         payload[7]&0x20 != 0,
		}
		if len(payload) > 8 {
			m.Text = string(payload[8:])
		}
		return m, true
	}

	// Legacy v1 compatibility decode.
	controlByte := byte(0)
	textStart := 6
	if len(payload) >= 7 {
		controlByte = payload[6]
		textStart = 7
		// Some endpoints send callout text right after octet 5 (missing octet 6 flags).
		if (payload[6]&0x1f) != 0 && looksLikeCalloutText(payload[6:]) {
			controlByte = 0
			textStart = 6
		}
	}
	m := calloutMessage{
		MessageType:           (payload[1] >> 4) & 0x0f,
		DeliveryReportRequest: (payload[1] >> 2) & 0x03,
		ServiceSelection:      payload[1]&0x02 != 0,
		Storage:               payload[1]&0x01 != 0,
		MessageRef:            payload[2],
		ExtensionHeader:       payload[3]&0x80 != 0,
		TextCodingScheme:      payload[3] & 0x7f,
		Function:              (payload[4] >> 4) & 0x0f,
		CalloutNumber:         payload[4] & 0x0f,
		Severity:              payload[5] & 0x0f,
		GroupControl:          (payload[5] >> 6) & 0x03,
		TimestampControl:      payload[5]&0x20 != 0,
		UserReceiptControl:    payload[5]&0x10 != 0,
		TextIsStatus:          controlByte&0x80 != 0,
		EndCallout:            controlByte&0x40 != 0,
		PTTNotAllowed:         controlByte&0x20 != 0,
	}
	if len(payload) > textStart {
		m.Text = string(payload[textStart:])
	}
	return m, true
}

func looksLikeCalloutText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	printable := 0
	n := len(data)
	if n > 8 {
		n = 8
	}
	for i := 0; i < n; i++ {
		b := data[i]
		if b >= 0x20 && b <= 0x7e {
			printable++
		}
	}
	return printable >= 2
}

func isLikelySDSProtocolID(v byte) bool {
	if v >= 1 && v <= 14 {
		return true
	}
	if v == 24 || v == 25 {
		return true
	}
	if v == sdsProtocolHomeModeDisplay {
		return true
	}
	if v == sdsProtocolCallout {
		return true
	}
	return v >= 130 && v <= 141
}
