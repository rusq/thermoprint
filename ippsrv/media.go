package ippsrv

// Media size attributes for driverless clients.  CUPS "everywhere" queue
// generation and macOS AirPrint setup query media collections, not just the
// keyword names, so both are derived from the printer's self-describing
// media names (PWG 5101.1, e.g. "om_label-48x100mm_48x100mm").

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/OpenPrinting/goipp"
)

const (
	rollPrintableWidth  = 4800
	rollWidthTolerance  = 1
	rollCustomMinHeight = 2000
	rollCustomMaxHeight = 100000

	rollCustomMinMedia = "custom_min_48x20mm"
	rollCustomMaxMedia = "custom_max_48x1000mm"
)

type rollMediaRange struct {
	width     int
	minHeight int
	maxHeight int
}

func defaultRollMediaRange() rollMediaRange {
	return rollMediaRange{
		width:     rollPrintableWidth,
		minHeight: rollCustomMinHeight,
		maxHeight: rollCustomMaxHeight,
	}
}

func (r rollMediaRange) contains(x, y int) bool {
	return abs(x-r.width) <= rollWidthTolerance && y >= r.minHeight && y <= r.maxHeight
}

// mediaSizeDimensions parses the trailing dimension segment of a PWG 5101.1
// self-describing metric media size name and returns width and height in
// hundredths of a millimetre.
func mediaSizeDimensions(name string) (x, y int, err error) {
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return 0, 0, fmt.Errorf("media name %q is not self-describing", name)
	}
	dims, ok := strings.CutSuffix(name[i+1:], "mm")
	if !ok {
		return 0, 0, fmt.Errorf("media name %q does not have metric dimensions", name)
	}
	w, h, ok := strings.Cut(dims, "x")
	if !ok {
		return 0, 0, fmt.Errorf("media name %q dimensions are not WxH", name)
	}
	fw, err := strconv.ParseFloat(w, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("media name %q width: %w", name, err)
	}
	fh, err := strconv.ParseFloat(h, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("media name %q height: %w", name, err)
	}
	return int(fw * 100), int(fh * 100), nil
}

func mediaCustomSizeDimensions(name string) (x, y int, err error) {
	if !strings.HasPrefix(name, "custom_") {
		return 0, 0, fmt.Errorf("media name %q is not custom media", name)
	}
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return 0, 0, fmt.Errorf("media name %q is not self-describing", name)
	}
	dims := name[i+1:]
	if strings.HasSuffix(dims, "mm") {
		return mediaSizeDimensions(name)
	}
	w, h, ok := strings.Cut(dims, "x")
	if !ok {
		return 0, 0, fmt.Errorf("media name %q dimensions are not WxH", name)
	}
	if hIn, ok := strings.CutSuffix(h, "in"); ok {
		x, err = inchesToHundredthsMM(w)
		if err != nil {
			return 0, 0, fmt.Errorf("media name %q width: %w", name, err)
		}
		y, err = inchesToHundredthsMM(hIn)
		if err != nil {
			return 0, 0, fmt.Errorf("media name %q height: %w", name, err)
		}
		return x, y, nil
	}
	x, err = strconv.Atoi(w)
	if err != nil {
		return 0, 0, fmt.Errorf("media name %q width: %w", name, err)
	}
	y, err = strconv.Atoi(h)
	if err != nil {
		return 0, 0, fmt.Errorf("media name %q height: %w", name, err)
	}
	return x, y, nil
}

func inchesToHundredthsMM(s string) (int, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int(math.Round(f * 2540)), nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// mediaSizeCol returns the media-size collection member.
func mediaSizeCol(x, y int) goipp.Collection {
	return goipp.Collection{
		goipp.MakeAttribute("x-dimension", goipp.TagInteger, goipp.Integer(x)),
		goipp.MakeAttribute("y-dimension", goipp.TagInteger, goipp.Integer(y)),
	}
}

func rollMediaSizeRangeCol(r rollMediaRange) goipp.Collection {
	return goipp.Collection{
		goipp.MakeAttribute("x-dimension", goipp.TagInteger, goipp.Integer(r.width)),
		goipp.MakeAttribute("y-dimension", goipp.TagRange, goipp.Range{Lower: r.minHeight, Upper: r.maxHeight}),
	}
}

// mediaCol returns a media-col collection for a borderless size.  The
// margins are siblings of media-size within media-col, per RFC 8011 and PWG
// 5100.3; media-size itself holds only the dimensions.
func mediaCol(x, y int) goipp.Collection {
	return goipp.Collection{
		goipp.MakeAttribute("media-size", goipp.TagBeginCollection, mediaSizeCol(x, y)),
		goipp.MakeAttribute("media-top-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-bottom-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-left-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-right-margin", goipp.TagInteger, goipp.Integer(0)),
	}
}

func rollMediaColRange(r rollMediaRange) goipp.Collection {
	return goipp.Collection{
		goipp.MakeAttribute("media-size", goipp.TagBeginCollection, rollMediaSizeRangeCol(r)),
		goipp.MakeAttribute("media-top-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-bottom-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-left-margin", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-right-margin", goipp.TagInteger, goipp.Integer(0)),
	}
}

// mediaCollections builds the media-size-supported and media-col-database
// values from the printer's self-describing fixed media names, then appends
// the variable-height roll range.  The LX-D02 custom min/max media names are
// capability keywords rather than fixed sizes, so they are not parsed as
// fixed entries.
func mediaCollections(names []string) (sizes, cols []goipp.Value) {
	for _, name := range names {
		if isRollCustomRangeKeyword(name) {
			continue
		}
		x, y, err := mediaSizeDimensions(name)
		if err != nil {
			continue
		}
		sizes = append(sizes, mediaSizeCol(x, y))
		cols = append(cols, mediaCol(x, y))
	}
	if r, ok := supportedRollCustomRange(names); ok {
		sizes = append(sizes, rollMediaSizeRangeCol(r))
		cols = append(cols, rollMediaColRange(r))
	}
	return sizes, cols
}

func supportedRollCustomRange(names []string) (rollMediaRange, bool) {
	var (
		r            = defaultRollMediaRange()
		hasMinHeight bool
		hasMaxHeight bool
	)
	for _, name := range names {
		endpoint, x, y, ok := rollCustomRangeEndpoint(name)
		if !ok || abs(x-r.width) > rollWidthTolerance {
			continue
		}
		switch endpoint {
		case "min":
			r.minHeight = y
			hasMinHeight = true
		case "max":
			r.maxHeight = y
			hasMaxHeight = true
		}
	}
	return r, hasMinHeight && hasMaxHeight && r.minHeight <= r.maxHeight
}

func isRollCustomRangeKeyword(name string) bool {
	_, _, _, ok := rollCustomRangeEndpoint(name)
	return ok
}

func rollCustomRangeEndpoint(name string) (endpoint string, x, y int, ok bool) {
	switch {
	case strings.HasPrefix(name, "custom_min_"):
		endpoint = "min"
	case strings.HasPrefix(name, "custom_max_"):
		endpoint = "max"
	default:
		return "", 0, 0, false
	}
	x, y, err := mediaCustomSizeDimensions(name)
	if err != nil {
		return "", 0, 0, false
	}
	return endpoint, x, y, true
}

func fixedMediaHeights(names []string) map[int]bool {
	heights := make(map[int]bool)
	for _, name := range names {
		if isRollCustomRangeKeyword(name) {
			continue
		}
		x, y, err := mediaSizeDimensions(name)
		if err != nil || x != rollPrintableWidth {
			continue
		}
		heights[y] = true
	}
	return heights
}

func mediaAttrAllowsTrim(attrs goipp.Attributes, fixedHeights map[int]bool, customRange rollMediaRange) bool {
	for _, attr := range attrs {
		switch attr.Name {
		case "media":
			for _, v := range attr.Values {
				name, ok := v.V.(goipp.String)
				if !ok {
					continue
				}
				x, y, err := mediaCustomSizeDimensions(name.String())
				if err == nil && customRange.contains(x, y) {
					return true
				}
			}
		case "media-col":
			for _, v := range attr.Values {
				col, ok := v.V.(goipp.Collection)
				if !ok {
					continue
				}
				x, y, ok := mediaColDimensions(col)
				if ok && customRange.contains(x, y) && !fixedHeights[y] {
					return true
				}
			}
		}
	}
	return false
}

func mediaColDimensions(col goipp.Collection) (x, y int, ok bool) {
	vv, found := findAttr(goipp.Attributes(col), "media-size")
	if !found {
		return 0, 0, false
	}
	for _, v := range vv {
		mediaSize, ok := v.V.(goipp.Collection)
		if !ok {
			continue
		}
		return dimensionsFromMediaSize(mediaSize)
	}
	return 0, 0, false
}

func dimensionsFromMediaSize(mediaSize goipp.Collection) (x, y int, ok bool) {
	xv, xok := integerMember(mediaSize, "x-dimension")
	yv, yok := integerMember(mediaSize, "y-dimension")
	if !xok || !yok {
		return 0, 0, false
	}
	return xv, yv, true
}

func integerMember(col goipp.Collection, name string) (int, bool) {
	vv, ok := findAttr(goipp.Attributes(col), name)
	if !ok {
		return 0, false
	}
	for _, v := range vv {
		n, ok := v.V.(goipp.Integer)
		if ok {
			return int(n), true
		}
	}
	return 0, false
}

func requestAllowsTrailingBlankTrim(req *goipp.Message, p PrinterInformer) bool {
	media := p.MediaSupported()
	customRange, ok := supportedRollCustomRange(media)
	if !ok {
		customRange = defaultRollMediaRange()
	}
	fixedHeights := fixedMediaHeights(media)
	return mediaAttrAllowsTrim(req.Operation, fixedHeights, customRange) || mediaAttrAllowsTrim(req.Job, fixedHeights, customRange)
}
