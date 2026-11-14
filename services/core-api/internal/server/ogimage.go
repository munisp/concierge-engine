package server

// Phase 28, lever #4: dynamic OG images. Social shares (WhatsApp, Twitter/
// X, LinkedIn) render a bare text card without og:image — the branded card
// below is generated deterministically from real tenant data (name,
// service, price, consented rating) so every shared link carries the
// tenant's brand. Pure Go rendering (x/image basicfont, nearest-neighbor
// upscale) — no font files, no cgo, no network.
//
// Deterministic output for identical inputs → safe behind long cache
// headers and ETag.

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	ogBg     = color.RGBA{0x0f, 0x1b, 0x2d, 0xff} // matches site header
	ogAccent = color.RGBA{0x0b, 0x5f, 0xff, 0xff} // matches site CTA
	ogMuted  = color.RGBA{0x9f, 0xb3, 0xc8, 0xff}
	ogWhite  = color.RGBA{0xff, 0xff, 0xff, 0xff}
	ogGold   = color.RGBA{0xf5, 0xb5, 0x0a, 0xff}
)

// drawTextScaled renders s with the built-in 7x13 face onto a scratch
// image, then nearest-neighbor-upscales into dst at (x, y). Scale is
// integer — 6 ≈ 78px cap height for titles, 3 ≈ 39px for captions.
// maxWidth truncates with an ellipsis so text never bleeds off-canvas.
func drawTextScaled(dst *image.RGBA, x, y, scale int, c color.Color, s string, maxWidth int) {
	if s == "" || scale <= 0 {
		return
	}
	face := basicfont.Face7x13
	maxChars := maxWidth / (7 * scale)
	if maxChars > 3 && len([]rune(s)) > maxChars {
		r := []rune(s)
		s = string(r[:maxChars-1]) + "…"
	}
	w := font.MeasureString(face, s).Ceil()
	if w <= 0 {
		return
	}
	tmp := image.NewRGBA(image.Rect(0, 0, w, 13))
	d := &font.Drawer{
		Dst:  tmp,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(0, 11),
	}
	d.DrawString(s)
	for py := 0; py < 13; py++ {
		for px := 0; px < w; px++ {
			_, _, _, a := tmp.At(px, py).RGBA()
			if a == 0 {
				continue
			}
			r := image.Rect(x+px*scale, y+py*scale, x+(px+1)*scale, y+(py+1)*scale)
			draw.Draw(dst, r, &image.Uniform{c}, image.Point{}, draw.Src)
		}
	}
}

// renderOG writes a 1200x630 branded social card. All three lines come
// from real tenant data assembled by the caller — nothing is invented.
func renderOG(w http.ResponseWriter, title, subtitle, rating, footer string) {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 630))
	draw.Draw(img, img.Bounds(), &image.Uniform{ogBg}, image.Point{}, draw.Src)
	// accent frame: top hairline + bottom band
	draw.Draw(img, image.Rect(0, 0, 1200, 8), &image.Uniform{ogAccent}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 566, 1200, 630), &image.Uniform{ogAccent}, image.Point{}, draw.Src)

	drawTextScaled(img, 64, 180, 6, ogWhite, title, 1200-128)
	drawTextScaled(img, 64, 300, 4, ogMuted, subtitle, 1200-128)
	if rating != "" {
		drawTextScaled(img, 64, 420, 4, ogGold, rating, 1200-128)
	}
	drawTextScaled(img, 64, 590, 3, ogWhite, footer, 1200-128)

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = png.Encode(w, img)
}

// ogRatingLine formats the consented aggregate for the card, e.g.
// "★ 4.8 · 37 client reviews". Empty when no consented reviews exist
// (honest: no stars without evidence).
func ogRatingLine(avg string, count int) string {
	if count <= 0 || avg == "" {
		return ""
	}
	label := "reviews"
	if count == 1 {
		label = "review"
	}
	return "★ " + avg + " · " + strconv.Itoa(count) + " client " + label
}
