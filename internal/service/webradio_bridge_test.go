package service

import "testing"

func TestNormalizeRadioFramePairs18ByteCodecFrames(t *testing.T) {
	var pending []byte

	frameA := make([]byte, 18)
	frameB := make([]byte, 18)
	frameA[0] = 0x80 // first codec bit = 1

	ste, ready, err := normalizeRadioFrame(frameA, &pending)
	if err != nil {
		t.Fatalf("normalizeRadioFrame(frameA) error: %v", err)
	}
	if ready || ste != nil {
		t.Fatalf("first 18-byte frame should not emit output yet")
	}

	ste, ready, err = normalizeRadioFrame(frameB, &pending)
	if err != nil {
		t.Fatalf("normalizeRadioFrame(frameB) error: %v", err)
	}
	if !ready {
		t.Fatalf("second 18-byte frame should emit a STE frame")
	}
	if len(ste) != 36 {
		t.Fatalf("unexpected STE length: %d", len(ste))
	}
	if ste[0] != 0x80 {
		t.Fatalf("unexpected STE header: 0x%02x", ste[0])
	}
	if ste[1] != 0x80 {
		t.Fatalf("unexpected first payload byte: 0x%02x", ste[1])
	}
	if ste[35]&0x3f != 0x3f {
		t.Fatalf("unexpected STE tail fill bits: 0x%02x", ste[35])
	}
}

func TestNormalizeRadioFrame35ByteCodecPayload(t *testing.T) {
	var pending []byte
	frame := make([]byte, 35)
	frame[0] = 0xAB

	ste, ready, err := normalizeRadioFrame(frame, &pending)
	if err != nil {
		t.Fatalf("normalizeRadioFrame error: %v", err)
	}
	if !ready {
		t.Fatalf("35-byte frame should emit output")
	}
	if len(ste) != 36 {
		t.Fatalf("unexpected STE length: %d", len(ste))
	}
	if ste[0] != 0x80 || ste[1] != 0xAB {
		t.Fatalf("unexpected STE conversion: %02x %02x", ste[0], ste[1])
	}
	if ste[35]&0x3f != 0x3f {
		t.Fatalf("unexpected STE tail fill bits: 0x%02x", ste[35])
	}
}

func TestNormalizeRadioFrameRejectsUnknownFrameSize(t *testing.T) {
	var pending []byte
	_, ready, err := normalizeRadioFrame(make([]byte, 19), &pending)
	if err == nil {
		t.Fatalf("expected error for unsupported frame size")
	}
	if ready {
		t.Fatalf("unsupported frame should not be ready")
	}
}
