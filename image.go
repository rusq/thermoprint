package thermoprint

import (
	"image"
	"image/color"
	"sort"

	"github.com/disintegration/imaging"
	"github.com/makeworld-the-better-one/dither/v2"
	"golang.org/x/image/draw"
)

const (
	DefaultThreshold = 128 // Default threshold for dark pixels
	DefaultGamma     = 0.0
)

var ditherFunctions = map[string]func(image.Image, float64) image.Image{
	"floyd-steinberg": dFloydSteinberg,
	"atkinson":        dAtkinson,
	"stucki":          dStucki,
	"bayer":           dBayer,
	"no-dither":       DitherThresholdFn(DefaultThreshold),
}

func AllDitherFunctions() []string {
	keys := make([]string, 0, len(ditherFunctions))
	for k := range ditherFunctions {
		keys = append(keys, k)
	}
	sort.Strings(keys) // sort for consistent order
	return keys
}

func resize(img image.Image, targetWidth int) image.Image {
	var resized draw.Image
	if img.Bounds().Dx() <= targetWidth {
		// We don't upscale, but place the image on a white canvas in the upper
		// left corner
		targetHeight := img.Bounds().Dy()
		resized = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		// fill canvas with white
		white := image.NewUniform(color.White)
		draw.Draw(resized, resized.Bounds(), white, image.Point{}, draw.Src)
		// Copy the original image onto the resized canvas in left upper corner
		draw.Copy(resized, image.Point{0, 0}, img, img.Bounds(), draw.Src, nil)
	} else {
		// Resize the image to the target width while maintaining aspect ratio
		targetHeight := (img.Bounds().Dy() * targetWidth) / img.Bounds().Dx()
		resized = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
	}
	return resized
}

// ditherimg is the default dither function used in the rasteriser.
func ditherimg(img image.Image, gamma float64) image.Image {
	return dAtkinson(img, gamma) // default dithering function
}

func dFloydSteinberg(img image.Image, gamma float64) image.Image {
	const defaultGamma = 1.5
	if gamma == 0.0 {
		gamma = defaultGamma
	}
	dstImage := imaging.AdjustGamma(img, gamma)
	dithered := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
	// increase brightness of the image to make it more suitable for dithering
	draw.FloydSteinberg.Draw(dithered, dithered.Bounds(), dstImage, image.Point{})
	return dithered
}

func dAtkinson(img image.Image, gamma float64) image.Image {
	const defaultGamma = 3.0
	if gamma == 0.0 {
		gamma = defaultGamma
	}
	dithered := image.NewRGBA(img.Bounds())
	d := dither.NewDitherer([]color.Color{color.Black, color.White})
	d.Matrix = dither.Atkinson
	d.Draw(dithered, dithered.Bounds(), imaging.AdjustGamma(img, gamma), image.Point{})
	return dithered
}

func dStucki(img image.Image, gamma float64) image.Image {
	const defaultGamma = 3.5
	if gamma == 0.0 {
		gamma = defaultGamma
	}
	dithered := image.NewRGBA(img.Bounds())
	d := dither.NewDitherer([]color.Color{color.Black, color.White})
	d.Matrix = dither.Stucki
	d.Draw(dithered, dithered.Bounds(), imaging.AdjustGamma(img, gamma), image.Point{})
	return dithered
}

func dBayer(img image.Image, gamma float64) image.Image {
	const defaultGamma = 3.5
	if gamma == 0.0 {
		gamma = defaultGamma
	}
	dithered := image.NewRGBA(img.Bounds())
	d := dither.NewDitherer([]color.Color{color.Black, color.White})
	d.Mapper = dither.Bayer(8, 8, 1.0) // 8x8 Bayer matrix
	d.Draw(dithered, dithered.Bounds(), imaging.AdjustGamma(img, gamma), image.Point{})
	return dithered
}

func resizeY(dst *image.RGBA, newHeight int) *image.RGBA {
	// Resize the destination image to the new height
	if newHeight <= dst.Bounds().Dy() {
		return dst // no need to resize
	}
	newRect := image.Rect(0, 0, dst.Bounds().Dx(), newHeight)
	newImg := image.NewRGBA(newRect)
	draw.Draw(newImg, newRect, image.White, image.Point{}, draw.Src) // fill with white
	// fill the new image with the current destination image
	draw.Draw(newImg, dst.Bounds(), dst, image.Point{}, draw.Src)
	return newImg
}
