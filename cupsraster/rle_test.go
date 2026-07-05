package cupsraster

import (
	"bufio"
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

// encodeLine RLE-encodes a single row for tests.  It uses repeat runs for
// consecutive equal groups and literal runs otherwise.
func encodeLine(w *bytes.Buffer, row []byte, groupSize int) {
	numGroups := len(row) / groupSize
	group := func(i int) []byte { return row[i*groupSize : (i+1)*groupSize] }
	for i := 0; i < numGroups; {
		// count run of equal groups
		run := 1
		for i+run < numGroups && run < 128 && bytes.Equal(group(i), group(i+run)) {
			run++
		}
		if run > 1 {
			w.WriteByte(byte(run - 1)) // 0..127: group repeated c+1 times
			w.Write(group(i))
			i += run
			continue
		}
		// count literal groups (no two consecutive equal)
		lit := 1
		for i+lit < numGroups && lit < 128 &&
			!(i+lit+1 <= numGroups-1 && bytes.Equal(group(i+lit), group(i+lit+1))) {
			lit++
		}
		if lit == 1 {
			w.WriteByte(0) // single group as a repeat of 1
			w.Write(group(i))
		} else {
			w.WriteByte(byte(257 - lit)) // 129..255: 257-c literal groups
			w.Write(row[i*groupSize : (i+lit)*groupSize])
		}
		i += lit
	}
}

// encodePage RLE-encodes rows, collapsing consecutive identical rows into
// line-repeat counts.
func encodePage(w *bytes.Buffer, rows [][]byte, groupSize int) {
	for y := 0; y < len(rows); {
		repeat := 0
		for y+repeat+1 < len(rows) && repeat < 255 && bytes.Equal(rows[y], rows[y+repeat+1]) {
			repeat++
		}
		w.WriteByte(byte(repeat))
		encodeLine(w, rows[y], groupSize)
		y += repeat + 1
	}
}

func decodeToRows(t *testing.T, data []byte, height, bytesPerLine, groupSize int, fill byte) [][]byte {
	t.Helper()
	rows := make([][]byte, height)
	err := decodeLines(bufio.NewReader(bytes.NewReader(data)), height, bytesPerLine, groupSize, fill,
		func(y int, row []byte) {
			rows[y] = append([]byte(nil), row...)
		})
	if err != nil {
		t.Fatalf("decodeLines: %v", err)
	}
	return rows
}

func TestRLERoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		groupSize int
		rows      [][]byte
	}{
		{"1bit runs", 1, [][]byte{
			{0x00, 0x00, 0x00, 0x00},
			{0xff, 0xff, 0xff, 0xff},
			{0xaa, 0x55, 0xaa, 0x55},
		}},
		{"1bit repeated lines", 1, [][]byte{
			{0x0f, 0x0f, 0x0f, 0x0f},
			{0x0f, 0x0f, 0x0f, 0x0f},
			{0x0f, 0x0f, 0x0f, 0x0f},
			{0xf0, 0xf0, 0xf0, 0xf0},
		}},
		{"8bit literals", 1, [][]byte{
			{1, 2, 3, 4, 5, 6, 7, 8},
			{8, 7, 6, 5, 4, 3, 2, 1},
		}},
		{"24bit groups", 3, [][]byte{
			{1, 2, 3, 1, 2, 3, 9, 9, 9},
			{5, 5, 5, 6, 6, 6, 7, 7, 7},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			encodePage(&buf, tt.rows, tt.groupSize)
			got := decodeToRows(t, buf.Bytes(), len(tt.rows), len(tt.rows[0]), tt.groupSize, 0xff)
			for y := range tt.rows {
				if !bytes.Equal(got[y], tt.rows[y]) {
					t.Errorf("row %d: got % x, want % x", y, got[y], tt.rows[y])
				}
			}
		})
	}
}

func TestRLERoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, groupSize := range []int{1, 3} {
		const height, groups = 64, 32
		rows := make([][]byte, height)
		for y := range rows {
			rows[y] = make([]byte, groups*groupSize)
			for i := range rows[y] {
				rows[y][i] = byte(rng.Intn(4)) // few values → mixed runs/literals
			}
		}
		var buf bytes.Buffer
		encodePage(&buf, rows, groupSize)
		got := decodeToRows(t, buf.Bytes(), height, groups*groupSize, groupSize, 0xff)
		for y := range rows {
			if !bytes.Equal(got[y], rows[y]) {
				t.Fatalf("groupSize %d row %d: got % x, want % x", groupSize, y, got[y], rows[y])
			}
		}
	}
}

func TestRLEFillRemainder(t *testing.T) {
	// control byte 0x80 fills the rest of the line with the blank value,
	// which depends on the colour space of the caller.
	for _, tt := range []struct {
		name string
		fill byte
	}{
		{"white is 0x00 (K space)", 0x00},
		{"white is 0xff (sGray)", 0xff},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// one line: single literal group 0x42, then fill remainder
			data := []byte{0x00 /* lineRepeat */, 0x00 /* repeat 1 group */, 0x42, 0x80 /* fill */}
			rows := decodeToRows(t, data, 1, 8, 1, tt.fill)
			want := []byte{0x42, tt.fill, tt.fill, tt.fill, tt.fill, tt.fill, tt.fill, tt.fill}
			if !bytes.Equal(rows[0], want) {
				t.Errorf("got % x, want % x", rows[0], want)
			}
		})
	}
}

func TestRLEErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated after lineRepeat", []byte{0x00}},
		{"truncated repeat group", []byte{0x00, 0x05}},
		{"truncated literal run", []byte{0x00, 0xfe /* 3 literals */, 0x01}},
		{"repeat overflows line", []byte{0x00, 0x7f /* 128 groups > 8 */, 0xaa}},
		{"literal overflows line", []byte{0x00, 0x81 /* 128 literals > 8 */, 0x01, 0x02}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := decodeLines(bufio.NewReader(bytes.NewReader(tt.data)), 2, 8, 1, 0xff,
				func(int, []byte) {})
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRLEErrorNamesRow(t *testing.T) {
	// error on the second line group must mention the row
	var buf bytes.Buffer
	encodePage(&buf, [][]byte{{0xaa, 0xbb}}, 1)
	buf.WriteByte(0x00) // second lineRepeat, then truncated
	err := decodeLines(bufio.NewReader(bytes.NewReader(buf.Bytes())), 4, 2, 1, 0xff, func(int, []byte) {})
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "row 1"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err, want)
	}
}
