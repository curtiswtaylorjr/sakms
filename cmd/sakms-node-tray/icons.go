package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// Tray status icons, hand-ported from frontend/public/favicon.svg.
//
// The favicon is a 100×100 film-clip brand mark: a rounded cream square, a
// navy clip body, five cream sprocket notches cut into each side of the body,
// and a cream play triangle. Here that palette is repurposed to signal the
// node's state: the rounded-square BACKGROUND carries the state colour
// (green / amber / red) in place of the favicon's cream, while the clip body
// stays fixed navy for contrast; the sprocket notches and play triangle are
// cut back to the state-coloured background — the same background-cut-into-
// foreground relationship the favicon itself uses, just with the state colour
// standing in for cream.
//
// Two deliberate small-size adaptations from the source SVG:
//   - rendered at 32×32 (systray icons commonly display at up to ~22–24px, so
//     32 gives headroom without being excessive);
//   - sprocket notches reduced from 5 per side to 2 — at this size five notches
//     read as illegible clutter, two still read as "film strip" texture.
//
// These shapes are a hand-ported, intentionally simplified copy of the
// favicon's coordinates (scaled from its 100×100 viewBox), NOT a live SVG
// render — no SVG-rendering dependency by design. A future favicon redesign
// should re-port the coordinates here.

const (
	iconSize   = 32 // output PNG dimension (px)
	iconSupers = 4  // supersample factor for edge anti-aliasing
)

// navy is the favicon's clip-body colour (#1B2A4A); fixed across all states.
var navy = color.RGBA{R: 0x1b, G: 0x2a, B: 0x4a, A: 0xff}

func iconGreen() []byte { return brandPNG(color.RGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}) } // Tailwind green-500
func iconAmber() []byte { return brandPNG(color.RGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff}) } // Tailwind amber-500
func iconRed() []byte   { return brandPNG(color.RGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff}) } // Tailwind red-500

// brandPNG renders the film-clip silhouette at iconSize×iconSize with the
// given state colour as the background, supersampled iconSupers× for smooth
// edges on the rounded corners and play triangle.
func brandPNG(state color.RGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	samples := iconSupers * iconSupers
	for oy := 0; oy < iconSize; oy++ {
		for ox := 0; ox < iconSize; ox++ {
			var rSum, gSum, bSum, aSum int
			for sy := 0; sy < iconSupers; sy++ {
				for sx := 0; sx < iconSupers; sx++ {
					// Sub-pixel centre mapped into the favicon's 100-unit viewBox.
					fx := (float64(ox) + (float64(sx)+0.5)/iconSupers) / iconSize * 100
					fy := (float64(oy) + (float64(sy)+0.5)/iconSupers) / iconSize * 100
					c := sampleFavicon(fx, fy, state)
					// Accumulate premultiplied so transparent margin blends correctly.
					rSum += int(c.R) * int(c.A)
					gSum += int(c.G) * int(c.A)
					bSum += int(c.B) * int(c.A)
					aSum += int(c.A)
				}
			}
			var out color.NRGBA
			if aSum > 0 {
				out = color.NRGBA{
					R: uint8(rSum / aSum),
					G: uint8(gSum / aSum),
					B: uint8(bSum / aSum),
					A: uint8(aSum / samples),
				}
			}
			img.SetNRGBA(ox, oy, out)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// sampleFavicon returns the opaque colour at point (x,y) in 100-unit viewBox
// space, or transparent for the margin outside the rounded square.
func sampleFavicon(x, y float64, state color.RGBA) color.RGBA {
	if !inRoundedRect(x, y) {
		return color.RGBA{} // transparent margin outside the rounded square
	}
	// The clip body is navy except where a sprocket notch or the play triangle
	// cuts back through to the state-coloured background.
	if inClipBody(x, y) && !inNotch(x, y) && !inTriangle(x, y) {
		return navy
	}
	return state
}

// inRoundedRect matches the favicon's background: rect x8 y8 84×84, rx20.
func inRoundedRect(x, y float64) bool {
	const x0, y0, x1, y1, r = 8.0, 8.0, 92.0, 92.0, 20.0
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	var cx, cy float64
	corner := true
	switch {
	case x < x0+r && y < y0+r:
		cx, cy = x0+r, y0+r
	case x > x1-r && y < y0+r:
		cx, cy = x1-r, y0+r
	case x < x0+r && y > y1-r:
		cx, cy = x0+r, y1-r
	case x > x1-r && y > y1-r:
		cx, cy = x1-r, y1-r
	default:
		corner = false
	}
	if corner {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
	return true
}

// inClipBody matches the favicon's clip-body rect x26 y16 48×68.
func inClipBody(x, y float64) bool {
	return x >= 26 && x <= 74 && y >= 16 && y <= 84
}

// inNotch matches 2 sprocket notches per side (reduced from the favicon's 5):
// 8×8 squares at x22 (left) / x70 (right), centred on y32 and y68 so they sit
// symmetrically about the clip body's midline and cut into its edges.
func inNotch(x, y float64) bool {
	leftX := x >= 22 && x <= 30
	rightX := x >= 70 && x <= 78
	if !leftX && !rightX {
		return false
	}
	return (y >= 28 && y <= 36) || (y >= 64 && y <= 72)
}

// inTriangle matches the favicon's play triangle: (43,37) (43,63) (65,50).
func inTriangle(x, y float64) bool {
	const ax, ay = 43.0, 37.0
	const bx, by = 43.0, 63.0
	const cx, cy = 65.0, 50.0
	d1 := edge(x, y, ax, ay, bx, by)
	d2 := edge(x, y, bx, by, cx, cy)
	d3 := edge(x, y, cx, cy, ax, ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

// edge returns the signed area of the triangle (p, a, b); its sign tells which
// side of line a→b the point p lies on.
func edge(px, py, ax, ay, bx, by float64) float64 {
	return (px-bx)*(ay-by) - (ax-bx)*(py-by)
}
