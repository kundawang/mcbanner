package mcbanner

// A small, pure Go renderer for the banner patterns.
//
// The original code renders SVG to PNG by shelling out to rsvg-convert. That
// keeps the genetic algorithm depending on an external program, which makes
// reproducible searches (and tests) fragile. The renderer in this file draws
// the exact same pattern geometry that DrawPattern sends to SVG, directly into
// an RGBA image. It is used for the preview and for fitness calculations, so a
// recipe can be reproduced without any external tools.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"
)

// renderScale is the number of samples per pixel (in each direction) when
// rasterizing. 4x4 coverage sampling gives edges that are close to a regular
// SVG rasterizer while keeping rendering fast enough for a GA.
const renderScale = 4

type rgbaValue struct {
	r, g, b, a float64 // all in 0..1
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// parseColorString understands the two color notations that tinysvg emits:
// "#rrggbb" and "rgba(r, g, b, a)".
func parseColorString(s string) rgbaValue {
	if strings.HasPrefix(s, "#") && len(s) >= 7 {
		if v, err := strconv.ParseUint(s[1:7], 16, 32); err == nil {
			return rgbaValue{
				r: float64((v>>16)&0xff) / 255.0,
				g: float64((v>>8)&0xff) / 255.0,
				b: float64(v&0xff) / 255.0,
				a: 1.0,
			}
		}
	}
	if strings.HasPrefix(s, "rgba(") {
		var r, g, b int
		var a float64
		if _, err := fmt.Sscanf(s, "rgba(%d, %d, %d, %f)", &r, &g, &b, &a); err == nil {
			return rgbaValue{
				r: clamp01(float64(r) / 255.0),
				g: clamp01(float64(g) / 255.0),
				b: clamp01(float64(b) / 255.0),
				a: clamp01(a),
			}
		}
	}
	// Unknown colors are treated as opaque black. Callers normally only pass
	// values from the colors map or values produced by tinysvg.
	return rgbaValue{0, 0, 0, 1}
}

// maskCanvas records the alpha coverage of one pattern, independent of the
// dye color. The color only comes into play when composing the layers.
type maskCanvas struct {
	w, h  int // high resolution dimensions
	scale int
	alpha []float64
}

func newMaskCanvas(scale int) *maskCanvas {
	w, h := BannerW*scale, BannerH*scale
	return &maskCanvas{w: w, h: h, scale: scale, alpha: make([]float64, w*h)}
}

func (m *maskCanvas) add(idx int, a float64) {
	if idx < 0 || idx >= len(m.alpha) || a <= 0 {
		return
	}
	// Source-over for coverage.
	m.alpha[idx] = a + m.alpha[idx]*(1.0-a)
}

// fillRect fills the scaled rectangle [x, x+w) x [y, y+h) with the given alpha.
func (m *maskCanvas) fillRect(x, y, w, h int, a float64) {
	s := float64(m.scale)
	x0, y0 := float64(x)*s, float64(y)*s
	x1, y1 := float64(x+w)*s, float64(y+h)*s
	for py := int(math.Floor(y0)); py < int(math.Ceil(y1)); py++ {
		cy := float64(py) + 0.5
		if cy < y0 || cy >= y1 {
			continue
		}
		for px := int(math.Floor(x0)); px < int(math.Ceil(x1)); px++ {
			cx := float64(px) + 0.5
			if cx < x0 || cx >= x1 {
				continue
			}
			m.add(py*m.w+px, a)
		}
	}
}

func (m *maskCanvas) Box(x, y, w, h int, col string) {
	m.fillRect(x, y, w, h, parseColorString(col).a)
}

func (m *maskCanvas) Line(x1, y1, x2, y2, thickness int, col string) {
	s := float64(m.scale)
	ax, ay := float64(x1)*s, float64(y1)*s
	bx, by := float64(x2)*s, float64(y2)*s
	radius := float64(thickness) * s / 2.0
	if radius <= 0 {
		return
	}
	a := parseColorString(col).a
	minX := int(math.Floor(math.Min(ax, bx) - radius))
	maxX := int(math.Ceil(math.Max(ax, bx) + radius))
	minY := int(math.Floor(math.Min(ay, by) - radius))
	maxY := int(math.Ceil(math.Max(ay, by) + radius))
	dx, dy := bx-ax, by-ay
	lengthSq := dx*dx + dy*dy
	for py := minY; py <= maxY; py++ {
		cy := float64(py) + 0.5
		if cy < 0 || cy >= float64(m.h) {
			continue
		}
		for px := minX; px <= maxX; px++ {
			cx := float64(px) + 0.5
			if cx < 0 || cx >= float64(m.w) {
				continue
			}
			var t float64
			if lengthSq > 0 {
				t = ((cx-ax)*dx + (cy-ay)*dy) / lengthSq
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
			}
			nx, ny := ax+t*dx, ay+t*dy
			ddx, ddy := cx-nx, cy-ny
			if ddx*ddx+ddy*ddy <= radius*radius {
				m.add(py*m.w+px, a)
			}
		}
	}
}

func pointInTriangle(px, py, x1, y1, x2, y2, x3, y3 float64) bool {
	d1 := (px-x2)*(y1-y2) - (x1-x2)*(py-y2)
	d2 := (px-x3)*(y2-y3) - (x2-x3)*(py-y3)
	d3 := (px-x1)*(y3-y1) - (x3-x1)*(py-y1)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func (m *maskCanvas) Triangle(x1, y1, x2, y2, x3, y3 int, col string) {
	s := float64(m.scale)
	ax, ay := float64(x1)*s, float64(y1)*s
	bx, by := float64(x2)*s, float64(y2)*s
	cx, cy := float64(x3)*s, float64(y3)*s
	a := parseColorString(col).a
	minX := int(math.Floor(math.Min(ax, math.Min(bx, cx))))
	maxX := int(math.Ceil(math.Max(ax, math.Max(bx, cx))))
	minY := int(math.Floor(math.Min(ay, math.Min(by, cy))))
	maxY := int(math.Ceil(math.Max(ay, math.Max(by, cy))))
	for py := minY; py <= maxY; py++ {
		sy := float64(py) + 0.5
		if sy < 0 || sy >= float64(m.h) {
			continue
		}
		for px := minX; px <= maxX; px++ {
			sx := float64(px) + 0.5
			if sx < 0 || sx >= float64(m.w) {
				continue
			}
			if pointInTriangle(sx, sy, ax, ay, bx, by, cx, cy) {
				m.add(py*m.w+px, a)
			}
		}
	}
}

func (m *maskCanvas) Circle(x, y, radius int, col string) {
	m.Ellipse(x, y, radius, radius, col)
}

func (m *maskCanvas) Ellipse(x, y, xr, yr int, col string) {
	s := float64(m.scale)
	cx, cy := float64(x)*s, float64(y)*s
	rx, ry := float64(xr)*s, float64(yr)*s
	if rx <= 0 || ry <= 0 {
		return
	}
	a := parseColorString(col).a
	minX := int(math.Floor(cx - rx))
	maxX := int(math.Ceil(cx + rx))
	minY := int(math.Floor(cy - ry))
	maxY := int(math.Ceil(cy + ry))
	for py := minY; py <= maxY; py++ {
		sy := float64(py) + 0.5
		if sy < 0 || sy >= float64(m.h) {
			continue
		}
		for px := minX; px <= maxX; px++ {
			sx := float64(px) + 0.5
			if sx < 0 || sx >= float64(m.w) {
				continue
			}
			dx := (sx - cx) / rx
			dy := (sy - cy) / ry
			if dx*dx+dy*dy <= 1.0 {
				m.add(py*m.w+px, a)
			}
		}
	}
}

func pointInPolygon(px, py float64, xs, ys []float64) bool {
	inside := false
	n := len(xs)
	j := n - 1
	for i := 0; i < n; i++ {
		if (ys[i] > py) != (ys[j] > py) {
			x := (xs[j]-xs[i])*(py-ys[i])/(ys[j]-ys[i]) + xs[i]
			if px < x {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func (m *maskCanvas) Poly4(x1, y1, x2, y2, x3, y3, x4, y4 int, col string) {
	s := float64(m.scale)
	xs := []float64{float64(x1) * s, float64(x2) * s, float64(x3) * s, float64(x4) * s}
	ys := []float64{float64(y1) * s, float64(y2) * s, float64(y3) * s, float64(y4) * s}
	a := parseColorString(col).a
	minX := int(math.Floor(math.Min(math.Min(xs[0], xs[1]), math.Min(xs[2], xs[3]))))
	maxX := int(math.Ceil(math.Max(math.Max(xs[0], xs[1]), math.Max(xs[2], xs[3]))))
	minY := int(math.Floor(math.Min(math.Min(ys[0], ys[1]), math.Min(ys[2], ys[3]))))
	maxY := int(math.Ceil(math.Max(math.Max(ys[0], ys[1]), math.Max(ys[2], ys[3]))))
	for py := minY; py <= maxY; py++ {
		sy := float64(py) + 0.5
		if sy < 0 || sy >= float64(m.h) {
			continue
		}
		for px := minX; px <= maxX; px++ {
			sx := float64(px) + 0.5
			if sx < 0 || sx >= float64(m.w) {
				continue
			}
			if pointInPolygon(sx, sy, xs, ys) {
				m.add(py*m.w+px, a)
			}
		}
	}
}

type maskKey struct {
	pattern int
	scale   int
}

var (
	maskCacheMu sync.RWMutex
	maskCache   = make(map[maskKey][]float64)
)

// patternMask returns the alpha coverage mask for a pattern. Masks only depend
// on the pattern geometry and a single color placeholder, so they can be
// cached and reused for every dye and every candidate.
func patternMask(pattern, scale int) []float64 {
	key := maskKey{pattern, scale}
	maskCacheMu.RLock()
	mask, ok := maskCache[key]
	maskCacheMu.RUnlock()
	if ok {
		return mask
	}
	canvas := newMaskCanvas(scale)
	// The placeholder color is opaque; DrawPattern only uses the alpha channel
	// of the color for gradient patterns, which is generated internally.
	drawPattern(canvas, pattern, "#000000")
	maskCacheMu.Lock()
	if existing, ok := maskCache[key]; ok {
		mask = existing
	} else {
		maskCache[key] = canvas.alpha
		mask = canvas.alpha
	}
	maskCacheMu.Unlock()
	return mask
}

// RenderPixels renders the banner into a BannerW x BannerH NRGBA image. Layers
// are composited in order, over an opaque white background (matching the
// "-b white" option that was previously passed to rsvg-convert).
func RenderPixels(b *Banner) *image.NRGBA {
	return renderPixels(b, renderScale)
}

func renderPixels(b *Banner, scale int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	if b == nil {
		fillWhite(out)
		return out
	}
	hs, vs := BannerW*scale, BannerH*scale
	buf := make([]float64, hs*vs*4) // premultiplied RGBA, 0..1
	for _, pat := range b.Patterns {
		if pat == nil {
			continue
		}
		colString, ok := colors[pat.color]
		if !ok {
			continue
		}
		col := parseColorString(colString)
		mask := patternMask(pat.pattern, scale)
		for i, coverage := range mask {
			cov := coverage * col.a
			if cov <= 0 {
				continue
			}
			j := i * 4
			inv := 1.0 - cov
			buf[j] = col.r*cov + buf[j]*inv
			buf[j+1] = col.g*cov + buf[j+1]*inv
			buf[j+2] = col.b*cov + buf[j+2]*inv
			buf[j+3] = cov + buf[j+3]*inv
		}
	}
	// Downsample the coverage buffer and composite over white.
	samples := float64(scale * scale)
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			var r, g, bl, a float64
			for sy := 0; sy < scale; sy++ {
				row := (y*scale + sy) * hs
				for sx := 0; sx < scale; sx++ {
					j := (row + x*scale + sx) * 4
					r += buf[j]
					g += buf[j+1]
					bl += buf[j+2]
					a += buf[j+3]
				}
			}
			r /= samples
			g /= samples
			bl /= samples
			a /= samples
			// Composite the premultiplied color over opaque white.
			r += 1.0 - a
			g += 1.0 - a
			bl += 1.0 - a
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(clamp01(r)*255.0 + 0.5),
				G: uint8(clamp01(g)*255.0 + 0.5),
				B: uint8(clamp01(bl)*255.0 + 0.5),
				A: 255,
			})
		}
	}
	return out
}

func fillWhite(img *image.NRGBA) {
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
}

// RenderPNG renders the banner as a PNG image without using external tools.
func RenderPNG(b *Banner) []byte {
	return EncodePNG(RenderPixels(b))
}

// EncodePNG encodes an image as PNG.
func EncodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// RenderRecipe renders a recipe and returns the annotation, or an error if the
// recipe contains invalid layers. This is the "reproduce the preview" path.
func RenderRecipe(r Recipe) (*image.NRGBA, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return RenderPixels(r.Banner()), nil
}

// RenderRecipePNG renders a recipe to PNG bytes.
func RenderRecipePNG(r Recipe) ([]byte, error) {
	img, err := RenderRecipe(r)
	if err != nil {
		return nil, err
	}
	return EncodePNG(img), nil
}
