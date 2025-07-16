package ippsrv

import (
	"context"
	"image"
)

// filter is a component that can convert the postscript data to a printable
// format. Difference to CUPS is that the output is always a raster image.

type imageMagickFilter struct{}

func (f *imageMagickFilter) Filter(ctx context.Context, data []byte) ([]image.Image, error) {
	// convert files to image files
	// combine image files to a single image
	return nil, nil
}

// runMagick runs the ImageMagick command to convert the data
// return the list of image file paths
func (f *imageMagickFilter) runMagick(ctx context.Context, data []byte) ([]string, error) {
	return nil, nil
}
