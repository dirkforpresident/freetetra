package service

import "fmt"

func normalizeTrafficSTE(data []byte) ([]byte, error) {
	switch len(data) {
	case 36:
		return sanitizeSTEFrame(data), nil
	case 35:
		ste := make([]byte, 36)
		copy(ste[1:], data)
		return sanitizeSTEFrame(ste), nil
	case 274:
		if !allBytesAreBits(data) {
			return nil, fmt.Errorf("274-byte traffic frame is not 1-bit-per-byte")
		}
		return packCodecBitsToSTE(data), nil
	default:
		return nil, fmt.Errorf("unsupported traffic frame length=%d", len(data))
	}
}

func steToCodecFrames(ste []byte) ([]byte, []byte) {
	if len(ste) < 36 {
		return make([]byte, 18), make([]byte, 18)
	}
	bits := unpackMSBBits(ste[1:36], 274)
	return packBitsTo18Bytes(bits[:137]), packBitsTo18Bytes(bits[137:274])
}

func packBitsTo18Bytes(bits []byte) []byte {
	out := make([]byte, 18)
	for i := 0; i < 137 && i < len(bits); i++ {
		if bits[i]&1 != 0 {
			out[i/8] |= 1 << (7 - (i % 8))
		}
	}
	return out
}

func sanitizeSTEFrame(in []byte) []byte {
	ste := make([]byte, 36)
	copy(ste, in)
	ste[0] = (ste[0] & 0x7c) | 0x80
	ste[35] = (ste[35] & 0xc0) | 0x3f
	return ste
}
