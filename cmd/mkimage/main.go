package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

var (
	outdir = flag.String("d", filepath.Join("..", "..", "media"), "Output `directory` for the image")
	imgSz  = flag.Int("s", 384, "Size of the image in `pixels`")
)

type drawFunc func(img *image.Paletted, dx, dy int)

type job struct {
	filename string
	imgFunc  drawFunc
}

var jobs = []job{
	{filename: "slant.png", imgFunc: slantFunc},
	{filename: "frame.png", imgFunc: frameFunc},
	{filename: "checkers.png", imgFunc: checkersPattern},
}

func main() {
	flag.Parse()

	if err := os.MkdirAll(*outdir, 0755); err != nil {
		panic(err)
	}
	for _, j := range jobs {
		filename := filepath.Join(*outdir, j.filename)
		if err := mkimage(filename, j.imgFunc); err != nil {
			panic(err)
		}
		println("Image created successfully:", filename)
	}
}

func mkimage(filename string, img drawFunc) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	rect := image.Rect(0, 0, *imgSz, *imgSz/2)
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}}

	canvas := image.NewPaletted(rect, pal)
	white := image.NewUniform(color.White)
	draw.Copy(canvas, image.Point{}, white, canvas.Bounds(), draw.Src, nil)

	img(canvas, *imgSz, *imgSz/2)

	return png.Encode(f, canvas)
}

func slantFunc(img *image.Paletted, dx, dy int) {
	// Draw a slanting line from top-left to bottom-right
	for i := 0; i < dy; i++ {
		img.SetColorIndex(i, i, 0)
	}
}

func frameFunc(img *image.Paletted, dx, dy int) {
	// Draw a frame around the image
	for i := 0; i < dy; i++ {
		img.SetColorIndex(i, 0, 0)    // Top edge
		img.SetColorIndex(i, dy-1, 0) // Bottom edge
		img.SetColorIndex(0, i, 0)    // Left edge
		img.SetColorIndex(dy-1, i, 0) // Right edge
	}
}

func checkersPattern(img *image.Paletted, dx, dy int) {
	// Draw a checkerboard pattern
	for y := 0; y < dy; y++ {
		for x := 0; x < dx; x++ {
			if (x+y)%2 == 0 {
				img.SetColorIndex(x, y, 1) // White square
			} else {
				img.SetColorIndex(x, y, 0) // Black square
			}
		}
	}
}
