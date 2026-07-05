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

	formats := attrStrings(t, resp.Operation, "document-format-supported")
	assert.ElementsMatch(t, []string{"image/pwg-raster", "image/urf"}, formats,
		"only raster formats may be advertised; PDF would make clients skip client-side rasterisation")
	assert.Equal(t, []string{"image/pwg-raster"}, attrStrings(t, resp.Operation, "document-format-default"))

	assert.Equal(t, []string{"black_1", "sgray_8"}, attrStrings(t, resp.Operation, "pwg-raster-document-type-supported"))
	assert.Equal(t, []string{"normal"}, attrStrings(t, resp.Operation, "pwg-raster-document-sheet-back"))
	assert.Equal(t, urfSupported(203), attrStrings(t, resp.Operation, "urf-supported"),
		"urf-supported must match the URF TXT record key")

	res, ok := findAttr(resp.Operation, "pwg-raster-document-resolution-supported")
	require.True(t, ok, "pwg-raster-document-resolution-supported missing")
	require.IsType(t, goipp.Resolution{}, res[0].V)
	assert.Equal(t, goipp.Resolution{Xres: 203, Yres: 203, Units: goipp.UnitsDpi}, res[0].V)

	media := attrStrings(t, resp.Operation, "media-supported")
	assert.Contains(t, media, "om_label-48x100mm_48x100mm")

	cols, ok := findAttr(resp.Operation, "media-col-database")
	require.True(t, ok, "media-col-database missing")
	assert.Len(t, cols, 4, "one media-col per label size")
	_, ok = findAttr(resp.Operation, "media-size-supported")
	assert.True(t, ok, "media-size-supported missing")
	_, ok = findAttr(resp.Operation, "media-col-default")
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
