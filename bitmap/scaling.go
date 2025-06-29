package bitmap

import (
	"image"

	"golang.org/x/image/draw"
)

// ResizeToFit resizes the image to the target width while maintaining aspect
// ratio. If the image is smaller than or equal to the target width, it places
// the image on a white canvas in the upper left corner, filling the rest with
// white.
func ResizeToFit(img image.Image, targetWidth int) image.Image {
	var resized draw.Image
	if img.Bounds().Dx() <= targetWidth {
		// We don't upscale, but place the image on a white canvas in the upper
		// left corner
		targetHeight := img.Bounds().Dy()
		resized = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		// fill canvas with white
		draw.Draw(resized, resized.Bounds(), image.White, image.Point{}, draw.Src)
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

// ResizeCanvasY resizes the destination image to the new height, filling with white
// if the new height is larger than the current height. If the new height is
// smaller or equal to the current height, it returns the original image.
func ResizeCanvasY(dst *image.RGBA, newHeight int) *image.RGBA {
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
