package ippsrv

import (
	"testing"

	"github.com/OpenPrinting/goipp"
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
		"bogus",            // skipped
		rollCustomMinMedia, // custom range keyword, not a fixed size
		rollCustomMaxMedia,
		"om_label-48x40mm_48x40mm",
	})
	require.Len(t, sizes, 3)
	require.Len(t, cols, 3)

	rangeSize, ok := sizes[2].(goipp.Collection)
	require.True(t, ok)
	assert.Equal(t, goipp.Integer(rollPrintableWidth), mustCollectionValue(t, rangeSize, "x-dimension"))
	assert.Equal(t, goipp.Range{Lower: rollCustomMinHeight, Upper: rollCustomMaxHeight}, mustCollectionValue(t, rangeSize, "y-dimension"))
}

func TestMediaCollectionsSupportsCustomRangeKeywords(t *testing.T) {
	sizes, cols := mediaCollections([]string{
		"om_label-48x40mm_48x40mm",
		"custom_min_48x25mm",
		"custom_max_48x900mm",
	})
	require.Len(t, sizes, 2)
	require.Len(t, cols, 2)

	rangeSize, ok := sizes[1].(goipp.Collection)
	require.True(t, ok)
	assert.Equal(t, goipp.Integer(rollPrintableWidth), mustCollectionValue(t, rangeSize, "x-dimension"))
	assert.Equal(t, goipp.Range{Lower: 2500, Upper: 90000}, mustCollectionValue(t, rangeSize, "y-dimension"))
}

func TestMediaCollectionsPreservesParseableCustomFixedMedia(t *testing.T) {
	sizes, cols := mediaCollections([]string{
		"custom_58x100mm_58x100mm",
	})
	require.Len(t, sizes, 1)
	require.Len(t, cols, 1)

	size, ok := sizes[0].(goipp.Collection)
	require.True(t, ok)
	assert.Equal(t, goipp.Integer(5800), mustCollectionValue(t, size, "x-dimension"))
	assert.Equal(t, goipp.Integer(10000), mustCollectionValue(t, size, "y-dimension"))
}

func TestMediaCustomSizeDimensions(t *testing.T) {
	tests := []struct {
		name    string
		media   string
		wantX   int
		wantY   int
		wantErr bool
	}{
		{"custom name with hundredths suffix", "custom_48x150mm_4800x15000", 4800, 15000, false},
		{"custom name with inches suffix", "custom_1.89x5.91in", 4801, 15011, false},
		{"custom min keyword", rollCustomMinMedia, 4800, 2000, false},
		{"custom max keyword", rollCustomMaxMedia, 4800, 100000, false},
		{"fixed media is not custom", "om_label-48x100mm_48x100mm", 0, 0, true},
		{"bad custom", "custom_roll", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, err := mediaCustomSizeDimensions(tt.media)
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

func TestRequestAllowsTrailingBlankTrim(t *testing.T) {
	p, err := WrapDriver(testDriver{}, "test-printer", "Test Printer")
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*goipp.Message)
		want   bool
	}{
		{
			name: "custom media keyword",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			want: true,
		},
		{
			name: "custom media-col",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(rollPrintableWidth, 15000)))
			},
			want: true,
		},
		{
			name: "custom media-col rounded width",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(rollPrintableWidth+1, 15000)))
			},
			want: true,
		},
		{
			name: "custom inch media keyword",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_1.89x5.91in")))
			},
			want: true,
		},
		{
			name: "fixed media-col height",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(rollPrintableWidth, 10000)))
			},
			want: false,
		},
		{
			name: "fixed media keyword",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("om_label-48x100mm_48x100mm")))
			},
			want: false,
		},
		{
			name: "wrong width",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(5800, 15000)))
			},
			want: false,
		},
		{
			name: "unparsable",
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_roll")))
			},
			want: false,
		},
		{
			name: "missing",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newIPPRequest(goipp.OpPrintJob, testRequestID)
			if tt.mutate != nil {
				tt.mutate(req)
			}
			assert.Equal(t, tt.want, requestAllowsTrailingBlankTrim(req, p))
		})
	}
}

func TestRequestAllowsTrailingBlankTrimUsesAdvertisedCustomRange(t *testing.T) {
	p := customMediaPrinter{
		Printer: testPrinter(t),
		media: []string{
			"om_label-48x100mm_48x100mm",
			"custom_min_48x25mm",
			"custom_max_48x900mm",
		},
	}

	tests := []struct {
		name string
		y    int
		want bool
	}{
		{"inside advertised range", 80000, true},
		{"below advertised range", 2000, false},
		{"above advertised range", 95000, false},
		{"fixed media height still does not trim", 10000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newIPPRequest(goipp.OpPrintJob, testRequestID)
			req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(rollPrintableWidth, tt.y)))
			assert.Equal(t, tt.want, requestAllowsTrailingBlankTrim(req, p))
		})
	}
}

func mustCollectionValue(t *testing.T, col goipp.Collection, name string) goipp.Value {
	t.Helper()

	vv, ok := findAttr(goipp.Attributes(col), name)
	require.True(t, ok, "collection member %q missing", name)
	require.Len(t, vv, 1)
	return vv[0].V
}

func testPrinter(t *testing.T) Printer {
	t.Helper()

	p, err := WrapDriver(testDriver{}, "test-printer", "Test Printer")
	require.NoError(t, err)
	return p
}

type customMediaPrinter struct {
	Printer
	media []string
}

func (p customMediaPrinter) MediaSupported() []string {
	return p.media
}
