package ippsrv

import (
	"bytes"
	"context"
	"testing"

	"github.com/OpenPrinting/goipp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attrStrings returns all values of the named operation attribute as strings.
func attrStrings(t *testing.T, attrs goipp.Attributes, name string) []string {
	t.Helper()
	vv, ok := findAttr(attrs, name)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range vv {
		out = append(out, v.V.String())
	}
	return out
}

func TestPrinterAttributes_RasterAdvertisement(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.OpGetPrinterAttributes, 7)

	resp, err := s.handleGetPrinterAttributes(context.Background(), req, nil)
	require.NoError(t, err)

	formats := attrStrings(t, resp.Printer, "document-format-supported")
	assert.ElementsMatch(t, []string{"image/pwg-raster", "image/urf"}, formats,
		"only raster formats may be advertised; PDF would make clients skip client-side rasterisation")
	assert.Equal(t, []string{"image/pwg-raster"}, attrStrings(t, resp.Printer, "document-format-default"))

	assert.Equal(t, []string{"black_1", "sgray_8"}, attrStrings(t, resp.Printer, "pwg-raster-document-type-supported"))
	assert.Equal(t, []string{"normal"}, attrStrings(t, resp.Printer, "pwg-raster-document-sheet-back"))
	assert.Equal(t, urfSupported(203), attrStrings(t, resp.Printer, "urf-supported"),
		"urf-supported must match the URF TXT record key")

	res, ok := findAttr(resp.Printer, "pwg-raster-document-resolution-supported")
	require.True(t, ok, "pwg-raster-document-resolution-supported missing")
	require.IsType(t, goipp.Resolution{}, res[0].V)
	assert.Equal(t, goipp.Resolution{Xres: 203, Yres: 203, Units: goipp.UnitsDpi}, res[0].V)

	media := attrStrings(t, resp.Printer, "media-supported")
	assert.Contains(t, media, "om_label-48x100mm_48x100mm")
	assert.Contains(t, media, rollCustomMinMedia)
	assert.Contains(t, media, rollCustomMaxMedia)
	assert.Equal(t, []string{"om_label-48x100mm_48x100mm"}, attrStrings(t, resp.Printer, "media-default"))

	cols, ok := findAttr(resp.Printer, "media-col-database")
	require.True(t, ok, "media-col-database missing")
	assert.Len(t, cols, 5, "one media-col per fixed label size plus custom roll range")
	_, ok = findAttr(resp.Printer, "media-size-supported")
	assert.True(t, ok, "media-size-supported missing")
	assert.ElementsMatch(t,
		[]string{"media-size", "media-top-margin", "media-bottom-margin", "media-left-margin", "media-right-margin"},
		attrStrings(t, resp.Printer, "media-col-supported"))
	_, ok = findAttr(resp.Printer, "media-col-default")
	assert.True(t, ok, "media-col-default missing")
}

// TestPrinterAttributes_Encodes encodes the complete Get-Printer-Attributes
// response to wire format.  goipp validates collections only at encode time
// (e.g. members with empty names are rejected), so inspecting the attribute
// list alone would miss malformed collections.
func TestPrinterAttributes_Encodes(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.OpGetPrinterAttributes, 8)

	resp, err := s.handleGetPrinterAttributes(context.Background(), req, nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, resp.Encode(&buf), "response must encode to wire format")
	assert.NotZero(t, buf.Len())

	// and it must decode back
	var m goipp.Message
	require.NoError(t, m.Decode(bytes.NewReader(buf.Bytes())))
}
