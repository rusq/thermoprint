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

type imageMagickFilter struct{}

var _ Filter = &imageMagickFilter{}

func (f *imageMagickFilter) ToRaster(ctx context.Context, dpi int, data []byte) ([]image.Image, error) {
	cmd := exec.CommandContext(ctx, "magick", "-", "-density", strconv.Itoa(dpi), "-background", "white", "-alpha", "remove", "png:-")
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
