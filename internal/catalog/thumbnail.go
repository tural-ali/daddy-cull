package catalog

import (
	"image"
	"image/color"
	_ "image/png"
	"io"
	"os"
	"strconv"
	"time"
)

// gridPixels is the longest edge a gallery tile is ever drawn at, doubled so the
// tile stays sharp on a retina display. A screenshot in the holding area is
// routinely 4000 pixels wide and 13 MB, and a grid of 120 of those is tens of
// gigabytes of transfer for images displayed at a couple of hundred pixels.
const gridPixels = 512

// viewerPixels is the longest edge the full-screen viewer draws a photograph at.
// A HEIC or RAW original cannot be handed to the browser, so the viewer gets a
// decode this large rather than the grid tile, which it would draw postage-stamp
// small or blow up into a blur.
const viewerPixels = 2560

// previewKind names a decoded preview in the cache, so a tile and a viewer-sized
// picture of the same file never collide.
func previewKind(kind string, pixels int) string {
	if pixels == gridPixels {
		return kind
	}
	return kind + "-" + strconv.Itoa(pixels)
}

// thumbnail returns a downscaled JPEG of an image a browser could have decoded
// itself, cached on disk so the decode happens once per file rather than once
// per view. The file streams straight into the decoder: nothing holds the whole
// original in memory, which matters when the original is a 13 MB screenshot.
func thumbnail(file io.ReadSeeker, cacheDir, subject string, size, mtime int64) ([]byte, error) {
	return cachedBytes(cacheDir, "tile", subject, size, mtime, func() ([]byte, error) {
		return shrinkToTile(file)
	})
}

// downscale reduces an image so its longest edge is at most max, averaging every
// source pixel that falls inside a destination pixel. Averaging over the whole
// box is what keeps fine detail such as screenshot text legible; sampling a
// single pixel per destination would alias it into noise.
//
// An image already small enough is returned untouched.
func downscale(source image.Image, max int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || (width <= max && height <= max) {
		return source
	}
	scale := float64(max) / float64(width)
	if height > width {
		scale = float64(max) / float64(height)
	}
	targetWidth := int(float64(width)*scale + 0.5)
	targetHeight := int(float64(height)*scale + 0.5)
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		y0 := bounds.Min.Y + y*height/targetHeight
		y1 := bounds.Min.Y + (y+1)*height/targetHeight
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < targetWidth; x++ {
			x0 := bounds.Min.X + x*width/targetWidth
			x1 := bounds.Min.X + (x+1)*width/targetWidth
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
					count++
				}
			}
			if count == 0 {
				continue
			}
			target.Set(x, y, color.RGBA64{
				R: uint16(red / count), G: uint16(green / count),
				B: uint16(blue / count), A: uint16(alpha / count),
			})
		}
	}
	return target
}

// thumbnailModTime is the timestamp reported for a generated tile. The tile is
// derived from the file, so the file's own time is what a conditional request
// should be validated against.
func thumbnailModTime(info os.FileInfo) time.Time { return info.ModTime() }
