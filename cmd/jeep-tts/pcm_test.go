package main

import (
	"encoding/binary"
	"testing"
)

func TestPcmToWAV(t *testing.T) {
	pcm := make([]byte, 100)
	for i := range pcm {
		pcm[i] = byte(i)
	}

	wav := pcmToWAV(pcm, 24000, 1, 16)

	if len(wav) != 44+len(pcm) {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+len(pcm))
	}

	if string(wav[0:4]) != "RIFF" {
		t.Error("missing RIFF header")
	}
	if string(wav[8:12]) != "WAVE" {
		t.Error("missing WAVE marker")
	}
	if string(wav[12:16]) != "fmt " {
		t.Error("missing fmt chunk")
	}
	if string(wav[36:40]) != "data" {
		t.Error("missing data chunk")
	}

	riffSize := binary.LittleEndian.Uint32(wav[4:8])
	if riffSize != uint32(36+len(pcm)) {
		t.Errorf("RIFF size = %d, want %d", riffSize, 36+len(pcm))
	}

	audioFormat := binary.LittleEndian.Uint16(wav[20:22])
	if audioFormat != 1 {
		t.Errorf("audio format = %d, want 1 (PCM)", audioFormat)
	}

	channels := binary.LittleEndian.Uint16(wav[22:24])
	if channels != 1 {
		t.Errorf("channels = %d, want 1", channels)
	}

	sampleRate := binary.LittleEndian.Uint32(wav[24:28])
	if sampleRate != 24000 {
		t.Errorf("sample rate = %d, want 24000", sampleRate)
	}

	byteRate := binary.LittleEndian.Uint32(wav[28:32])
	if byteRate != 48000 {
		t.Errorf("byte rate = %d, want 48000", byteRate)
	}

	bitsPerSample := binary.LittleEndian.Uint16(wav[34:36])
	if bitsPerSample != 16 {
		t.Errorf("bits per sample = %d, want 16", bitsPerSample)
	}

	dataSize := binary.LittleEndian.Uint32(wav[40:44])
	if dataSize != uint32(len(pcm)) {
		t.Errorf("data size = %d, want %d", dataSize, len(pcm))
	}

	for i := range pcm {
		if wav[44+i] != pcm[i] {
			t.Errorf("pcm data mismatch at byte %d", i)
			break
		}
	}
}

func TestPcmToWAV_Empty(t *testing.T) {
	wav := pcmToWAV(nil, 24000, 1, 16)
	if len(wav) != 44 {
		t.Fatalf("empty pcm wav length = %d, want 44", len(wav))
	}
	dataSize := binary.LittleEndian.Uint32(wav[40:44])
	if dataSize != 0 {
		t.Errorf("data size = %d, want 0", dataSize)
	}
}
