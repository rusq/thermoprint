package ippsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaSizeDimensions(t *testing.T) {
	tests := []struct {
		name    string
		media   string
		wantX   int
		wantY   int
		wantErr bool
	}{
		{"label", "om_label-48x100mm_48x100mm", 4800, 10000, false},
		{"short label", "om_label-48x32mm_48x32mm", 4800, 3200, false},
		{"fractional", "om_thing_21.5x30mm", 2150, 3000, false},
		{"iso a4", "iso_a4_210x297mm", 21000, 29700, false},
		{"not self-describing", "roll_57mm", 0, 0, true},
		{"imperial", "na_letter_8.5x11in", 0, 0, true},
		{"garbage", "whatever", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, err := mediaSizeDimensions(tt.media)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantX, x)
			assert.Equal(t, tt.wantY, y)
		})
	}
}

func TestMediaCollections(t *testing.T) {
	sizes, cols := mediaCollections([]string{
		"om_label-48x100mm_48x100mm",
		"bogus", // skipped
		"om_label-48x40mm_48x40mm",
	})
	assert.Len(t, sizes, 2)
	assert.Len(t, cols, 2)
}
