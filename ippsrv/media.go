package ippsrv

// Media size attributes for driverless clients.  CUPS "everywhere" queue
// generation and macOS AirPrint setup query media collections, not just the
// keyword names, so both are derived from the printer's self-describing
// media names (PWG 5101.1, e.g. "om_label-48x100mm_48x100mm").

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/OpenPrinting/goipp"
)

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

// mediaSizeCol returns the media-size collection member.
func mediaSizeCol(x, y int) goipp.Collection {
	return goipp.Collection{
		goipp.MakeAttribute("x-dimension", goipp.TagInteger, goipp.Integer(x)),
		goipp.MakeAttribute("y-dimension", goipp.TagInteger, goipp.Integer(y)),
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

// mediaCollections builds the media-size-supported and media-col-database
// values from the printer's self-describing media names.  Names that cannot
// be parsed are skipped.
func mediaCollections(names []string) (sizes, cols []goipp.Value) {
	for _, name := range names {
		x, y, err := mediaSizeDimensions(name)
		if err != nil {
			continue
		}
		sizes = append(sizes, mediaSizeCol(x, y))
		cols = append(cols, mediaCol(x, y))
	}
	return sizes, cols
}
