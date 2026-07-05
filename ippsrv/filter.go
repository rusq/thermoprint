package ippsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os/exec"
	"strconv"

	"golang.org/x/image/draw"

	"github.com/rusq/thermoprint/cupsraster"
)

// filter is a component that can convert the postscript data to a printable
// format. Difference to CUPS is that the output is always a raster image.

type Filter interface {
	// ToRaster converts the postscript data to a printable format.
	// It returns a slice of images, each representing a page.
	ToRaster(ctx context.Context, dpi int, data []byte) ([]image.Image, error)
	// Type returns the type of the filter, e.g. "ImageMagick", "Ghostscript",
	// etc.
	Type() string
}

// rasterSniffFilter decodes pre-rasterised page streams (PWG raster, Apple
// URF) natively and delegates everything else to the fallback filter.  It is
// what allows CUPS/macOS clients to rasterise documents on their side.
type rasterSniffFilter struct {
	fallback Filter
}

var _ Filter = &rasterSniffFilter{}

func (f *rasterSniffFilter) ToRaster(ctx context.Context, dpi int, data []byte) ([]image.Image, error) {
	if format := cupsraster.Detect(data); format != cupsraster.FormatUnknown {
		slog.InfoContext(ctx, "decoding client-rasterised document", "format", format)
		pages, err := cupsraster.DecodePages(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		imgs := make([]image.Image, len(pages))
		for i, pg := range pages {
			imgs[i] = scaleToDPI(ctx, pg, dpi)
		}
		return imgs, nil
	}
	return f.fallback.ToRaster(ctx, dpi, data)
}

// scaleToDPI resizes a decoded page whose declared resolution differs from
// the printer's, preserving the physical print size: a 100dpi page printed
// pixel-for-pixel on a 203dpi head would come out at half size.
func scaleToDPI(ctx context.Context, pg cupsraster.Page, dpi int) image.Image {
	if pg.XDPI <= 0 || pg.YDPI <= 0 || (pg.XDPI == dpi && pg.YDPI == dpi) {
		return pg.Image
	}
	b := pg.Image.Bounds()
	w := (b.Dx()*dpi + pg.XDPI/2) / pg.XDPI
	h := (b.Dy()*dpi + pg.YDPI/2) / pg.YDPI
	if w < 1 || h < 1 {
		return pg.Image
	}
	slog.InfoContext(ctx, "scaling raster page to printer resolution",
		"page_dpi_x", pg.XDPI, "page_dpi_y", pg.YDPI, "printer_dpi", dpi,
		"from", fmt.Sprintf("%dx%d", b.Dx(), b.Dy()), "to", fmt.Sprintf("%dx%d", w, h))
	dst := image.NewGray(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), pg.Image, b, draw.Src, nil)
	return dst
}

func (f *rasterSniffFilter) Type() string {
	return "raster+" + f.fallback.Type()
}

type imageMagickFilter struct{}

var _ Filter = &imageMagickFilter{}

func (f *imageMagickFilter) ToRaster(ctx context.Context, dpi int, data []byte) ([]image.Image, error) {
	cmd := exec.CommandContext(ctx, "magick", "-density", strconv.Itoa(dpi), "-", "-background", "white", "-alpha", "remove", "png:-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	r := bytes.NewReader(out)
	outSz := int64(len(out))

	var images []image.Image
	var eos bool // end of stream flag
	for !eos {
		slog.Info("decoding image from magick output")
		img, err := png.Decode(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// End of the stream, no more images to decode
				break
			}
			return images, fmt.Errorf("failed to decode image: %w", err)
		}
		images = append(images, img)
		currPos, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return images, fmt.Errorf("failed to seek in output stream: %w", err)
		}
		eos = currPos >= outSz //end of output stream flag
	}

	return images, nil
}

func (f *imageMagickFilter) Type() string {
	return "ImageMagick"
}
