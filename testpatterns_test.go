package thermoprint

import (
	"bytes"
	"testing"
)

func TestBufferFirstPacketRows(t *testing.T) {
	const width = 384
	lineBytes := width / 8
	packetBytes := lineBytes * 2
	controlPacket := 1 + firstPacketRowsSeparatorPackets

	packets := BufferFirstPacketRows(width)
	wantPacketCount := 2 + firstPacketRowsSeparatorPackets + firstPacketRowsTrailingPackets
	if len(packets) != wantPacketCount {
		t.Fatalf("BufferFirstPacketRows() returned %d packets, want %d", len(packets), wantPacketCount)
	}

	for i, packet := range packets {
		if len(packet) != packetBytes {
			t.Errorf("packet %d length = %d, want %d", i, len(packet), packetBytes)
		}
	}

	wantUpper := append(bytes.Repeat([]byte{0xff}, lineBytes/2), bytes.Repeat([]byte{0x00}, lineBytes/2)...)
	wantLower := append(bytes.Repeat([]byte{0x00}, lineBytes/2), bytes.Repeat([]byte{0xff}, lineBytes/2)...)
	wantMarker := append(wantUpper, wantLower...)
	if !bytes.Equal(packets[0], wantMarker) {
		t.Errorf("packet zero = % x, want complementary left/right scanlines % x", packets[0], wantMarker)
	}
	if !bytes.Equal(packets[controlPacket], packets[0]) {
		t.Errorf("control packet %d = % x, want packet zero % x", controlPacket, packets[controlPacket], packets[0])
	}

	for i, packet := range packets {
		if i == 0 || i == controlPacket {
			continue
		}
		if !bytes.Equal(packet, make([]byte, packetBytes)) {
			t.Errorf("blank packet %d contains data: % x", i, packet)
		}
	}
}
