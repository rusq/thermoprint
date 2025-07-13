package bitmap

import (
	"image"
	"image/color"
	"sort"

	"github.com/disintegration/imaging"
	"github.com/makeworld-the-better-one/dither/v2"
	"golang.org/x/image/draw"
)

type DitherFunc func(img image.Image, gamma float64) image.Image

var ditherFunctions = map[string]func(image.Image, float64) image.Image{
	"floyd-steinberg": DFloydSteinberg,
	"atkinson":        DAtkinson,
	"stucki":          DStucki,
	"bayer":           DBayer,
	"no-dither":       DitherThresholdFn(DefaultThreshold),
}

// DitherFunction returns a registered dither function by name.
func DitherFunction(name string) (DitherFunc, bool) {
	if name == "" {
		return DitherDefault, true
	}
	fn, ok := ditherFunctions[name]
	if !ok {
		return nil, false // function not found
	}
	return fn, true
}

// RegisterDitherFunction allows to register a new dither function by name.
func RegisterDitherFunction(name string, fn DitherFunc) {
	if name == "" {
		panic("dither function name cannot be empty")
	}
	if fn == nil {
		panic("dither function cannot be nil")
	}
	if _, exists := ditherFunctions[name]; exists {
		panic("dither function already registered: " + name)
	}
	ditherFunctions[name] = fn
}

// AllDitherFunctions returns a sorted list of all available dither function
// names.
func AllDitherFunctions() []string {
	keys := make([]string, 0, len(ditherFunctions))
	for k := range ditherFunctions {
		keys = append(keys, k)
	}
	sort.Strings(keys) // sort for consistent order
	return keys
}

// DitherDefault is the default dither function used in the rasteriser.
func DitherDefault(img image.Image, gamma float64) image.Image {
	return DFloydSteinberg(img, gamma) // default dithering function
}

// diffusionDither returns a dither function that applies error diffusion dithering
// using the specified matrix and gamma value, with default gamma fallback if gamma
// is 0.0. The resulting function will return a dithered black-and-white image.
func diffusionDither(matrix dither.ErrorDiffusionMatrix, defaultGamma float64) DitherFunc {
	return func(img image.Image, gamma float64) image.Image {
		if gamma == DefaultGamma {
			gamma = defaultGamma
		}
		dithered := image.NewRGBA(img.Bounds())
		d := dither.NewDitherer([]color.Color{color.Black, color.White})
		d.Matrix = matrix
		d.Draw(dithered, dithered.Bounds(), imaging.AdjustGamma(img, gamma), image.Point{})
		return dithered
	}
}

// PatternDither returns a dither function that applies pattern dithering
// using the specified pixel mapper and gamma value. The resulting function
// will return a dithered black-and-white image.
func patternDither(matrix dither.PixelMapper, defaultGamma float64) DitherFunc {
	return func(img image.Image, gamma float64) image.Image {
		if gamma == DefaultGamma {
			gamma = defaultGamma // default gamma for ordered dithering
		}
		dithered := image.NewRGBA(img.Bounds())
		d := dither.NewDitherer([]color.Color{color.Black, color.White})
		d.Mapper = matrix
		d.Draw(dithered, dithered.Bounds(), imaging.AdjustGamma(img, gamma), image.Point{})
		return dithered
	}
}

var (
	// DAtkinson applies Atkinson error diffusion dithering with a gamma value of 3.0.
	DAtkinson = diffusionDither(dither.Atkinson, 3.0)
	// DStucki applies Stucki error diffusion dithering with a gamma value of 3.5.
	DStucki = diffusionDither(dither.Stucki, 3.5)
	// DBayer applies Bayer ordered dithering with a gamma value of 3.5.
	DBayer = patternDither(dither.Bayer(8, 8, 1.0), 3.5) // 8x8 Bayer matrix
)

// DFloydSteinberg is a dither function that applies Floyd-Steinberg dithering,
// it uses standard library, so it is defined as a function instead of a
// variable like the others.
func DFloydSteinberg(img image.Image, gamma float64) image.Image {
	const defaultGamma = 1.5
	if gamma == DefaultGamma {
		gamma = defaultGamma
	}
	dstImage := imaging.AdjustGamma(img, gamma)
	dithered := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
	// increase brightness of the image to make it more suitable for dithering
	draw.FloydSteinberg.Draw(dithered, dithered.Bounds(), dstImage, image.Point{})
	return dithered
}

func DitherThresholdFn(threshold uint8) DitherFunc {
	return func(img image.Image, _ float64) image.Image {
		if threshold == 0 {
			threshold = DefaultThreshold // default threshold for dark pixels
		}
		trg := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				if PixelBit(img, x, y, threshold) {
					trg.SetColorIndex(x, y, 0) // black
				} else {
					trg.SetColorIndex(x, y, 1) // white
				}
			}
		}
		return trg
	}
}
