// Package showcase is the mock media server behind Alexandria's store
// screenshots and marketing material: invented titles, generated artwork,
// short synthetic clips, served through the Audiobookshelf, Jellyfin and
// Plex API shapes the apps already speak. Nothing in it is anyone's
// copyrighted work, so every screenshot taken against it can go to the
// stores and the website.
package showcase

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Palette: the brand (Royal Blue, Copper-Gold) and a set of deep hues that
// read well as covers on the dark app surfaces.
var palette = [][2]color.NRGBA{
	{{0x1E, 0x3A, 0x8A, 255}, {0x25, 0x63, 0xEB, 255}}, // royal blue
	{{0x7C, 0x2D, 0x12, 255}, {0xC9, 0x82, 0x2A, 255}}, // copper
	{{0x14, 0x53, 0x2D, 255}, {0x22, 0xC5, 0x5E, 255}}, // forest
	{{0x58, 0x1C, 0x87, 255}, {0xA8, 0x55, 0xF7, 255}}, // violet
	{{0x7F, 0x1D, 0x1D, 255}, {0xEF, 0x44, 0x44, 255}}, // crimson
	{{0x0F, 0x76, 0x6E, 255}, {0x2D, 0xD4, 0xBF, 255}}, // teal
	{{0x3F, 0x3F, 0x46, 255}, {0xA1, 0xA1, 0xAA, 255}}, // slate
	{{0x9A, 0x34, 0x12, 255}, {0xF9, 0x73, 0x16, 255}}, // ember
	{{0x1E, 0x29, 0x3B, 255}, {0x64, 0x74, 0x8B, 255}}, // steel
	{{0x83, 0x18, 0x43, 255}, {0xEC, 0x48, 0x99, 255}}, // rose
}

// Motif: the geometric figure drawn behind the title; the hash of the
// title picks it, so a title always gets the same cover.
type motif int

const (
	motifRings motif = iota
	motifBars
	motifDiamond
	motifWaves
	motifDots
	motifArch
	motifCount
)

type CoverSpec struct {
	Title    string
	Subtitle string // author, artist, year — the second line
	Kind     string // "book", "album", "film", "show", "podcast"
	Width    int
	Height   int
	// Bare leaves the type off (title, second line, kind mark): backdrops,
	// which the apps show behind their own title and buttons. Same colours
	// and figure as the titled art for the same title.
	Bare bool
}

var (
	fontOnce    sync.Once
	boldFace    *opentype.Font
	regularFace *opentype.Font
	fontErr     error
)

func loadFonts() {
	boldFace, fontErr = opentype.Parse(gobold.TTF)
	if fontErr != nil {
		return
	}
	regularFace, fontErr = opentype.Parse(goregular.TTF)
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// Cover renders the artwork for a spec as JPEG (quality 86). Deterministic
// per title. Posters are 2:3, album art 1:1, film backdrops 16:9 — the
// caller picks Width/Height; zero means a 600×900 poster.
func Cover(spec CoverSpec) ([]byte, error) {
	fontOnce.Do(loadFonts)
	if fontErr != nil {
		return nil, fontErr
	}
	w, h := spec.Width, spec.Height
	if w == 0 || h == 0 {
		w, h = 600, 900
	}
	seed := hash(spec.Title + "|" + spec.Subtitle)
	pal := palette[int(seed)%len(palette)]
	m := motif(int(seed>>8) % int(motifCount))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	paintGradient(img, pal[0], pal[1], float64(int(seed>>16)%360))
	paintMotif(img, m, pal[1], seed)
	// A soft dark band at the bottom keeps the type legible on any hue.
	for y := h * 55 / 100; y < h; y++ {
		a := float64(y-h*55/100) / float64(h-h*55/100)
		shade(img, y, 0.55*a)
	}
	if spec.Bare {
		return encodeJPEG(img)
	}
	// Type scales with the SHORT edge, so a 16:9 backdrop and a square
	// sleeve set their titles as sensibly as a 2:3 poster does.
	unit := math.Min(float64(w), float64(h))
	titleSize := unit / 9
	if len(spec.Title) > 22 {
		titleSize = unit / 11
	}
	perLine := 16
	if w > h {
		perLine = 28
	}
	lines := wrap(spec.Title, perLine)
	if len(lines) > 3 {
		lines = append(lines[:2], lines[2]+"…")
	}
	lineH := int(titleSize * 1.15)
	subSize := unit / 18
	margin := w / 14
	bottom := h - int(float64(h)*0.07)
	y := bottom
	if spec.Subtitle != "" {
		y -= int(subSize * 1.4)
	}
	y -= (len(lines) - 1) * lineH
	for _, line := range lines {
		drawText(img, boldFace, titleSize, line, margin, y, color.NRGBA{255, 255, 255, 255})
		y += lineH
	}
	if spec.Subtitle != "" {
		drawText(img, regularFace, subSize, spec.Subtitle, margin, bottom, color.NRGBA{255, 255, 255, 215})
	}
	drawKindMark(img, spec.Kind, w, h, pal[1])
	return encodeJPEG(img)
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 86}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hairColors: flat tones for the portraits' hair.
var hairColors = []color.NRGBA{
	{0x2B, 0x1E, 0x16, 255}, // dark brown
	{0x18, 0x18, 0x1C, 255}, // near black
	{0x6E, 0x3B, 0x22, 255}, // auburn
	{0xB8, 0x92, 0x5A, 255}, // fair
	{0x8C, 0x8C, 0x94, 255}, // grey
}

// Portrait renders a credited person's picture (cast and directors) as a
// size×size JPEG: a flat head-and-shoulders figure with no face on a
// gradient, an illustration rather than anyone's likeness. The hash of the
// name picks the background, build, hair style and colour, and clothing,
// so people differ from each other and each always looks the same.
func Portrait(name string, size int) ([]byte, error) {
	if size <= 0 {
		size = 400
	}
	seed := hash("person|" + name)
	pi := int(seed % uint32(len(palette)))
	pal := palette[pi]
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	paintGradient(img, pal[0], pal[1], float64(int(seed>>16)%360))

	f := float64(size)
	cx := f / 2
	headR := f * (0.15 + 0.01*float64((seed>>3)%4))      // 0.15–0.18
	headY := f * (0.40 + 0.01*float64((seed>>23)%3))     // 0.40–0.42
	shoulderW := f * (0.33 + 0.025*float64((seed>>5)%4)) // 0.33–0.405
	skin := lerp(color.NRGBA{250, 248, 245, 255}, pal[1], 0.08)
	si := (pi + 3 + int((seed>>20)%5)) % len(palette)
	shirt := lerp(palette[si][0], palette[si][1], 0.45)
	hair := hairColors[int((seed>>9)%uint32(len(hairColors)))]
	style := (seed >> 12) % 6

	circle := func(x0, y0, r float64) func(x, y float64) float64 {
		return func(x, y float64) float64 { return math.Hypot(x-x0, y-y0) - r }
	}
	above := func(y0 float64) func(x, y float64) float64 { return func(x, y float64) float64 { return y - y0 } }
	both := func(a, b func(x, y float64) float64) func(x, y float64) float64 {
		return func(x, y float64) float64 { return math.Max(a(x, y), b(x, y)) }
	}
	ellipse := func(x0, y0, rx, ry float64) func(x, y float64) float64 {
		return func(x, y float64) float64 {
			return (math.Hypot((x-x0)/rx, (y-y0)/ry) - 1) * math.Min(rx, ry)
		}
	}
	roundRect := func(x0, y0, hw, hh, rad float64) func(x, y float64) float64 {
		return func(x, y float64) float64 {
			qx := math.Abs(x-x0) - hw + rad
			qy := math.Abs(y-y0) - hh + rad
			return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - rad
		}
	}
	// The hair over the crown, shared by most styles.
	crown := both(circle(cx, headY-headR*0.06, headR*1.05), above(headY-headR*0.22))

	switch style {
	case 1: // long: falls behind the head to the shoulders
		fillSDF(img, hair, roundRect(cx, headY+headR*0.55, headR*1.2, headR*1.5, headR*0.95))
	case 4: // bob: frames the head down to the jaw
		fillSDF(img, hair, both(circle(cx, headY, headR*1.14), above(headY+headR*0.62)))
	}
	fillSDF(img, skin, roundRect(cx, f*0.62, headR*0.42, f*0.07, headR*0.3)) // neck
	fillSDF(img, shirt, ellipse(cx, f*0.98, shoulderW, f*0.29))              // shoulders
	fillSDF(img, skin, circle(cx, headY, headR))                             // head
	switch style {
	case 0, 1: // cropped; long
		fillSDF(img, hair, crown)
	case 2: // bun
		fillSDF(img, hair, crown)
		fillSDF(img, hair, circle(cx, headY-headR*1.12, headR*0.42))
	case 3: // curls
		for a := -165.0; a <= -15; a += 25 {
			r := a * math.Pi / 180
			fillSDF(img, hair, circle(cx+math.Cos(r)*headR*0.98, headY+math.Sin(r)*headR*0.98, headR*0.36))
		}
		fillSDF(img, hair, crown)
	case 4: // bob: a fringe over the brow
		fillSDF(img, hair, both(circle(cx, headY-headR*0.04, headR*1.06), above(headY-headR*0.12)))
	case 5: // side part: the line slants across the crown
		fillSDF(img, hair, both(circle(cx, headY-headR*0.06, headR*1.06), func(x, y float64) float64 {
			return (y - (headY - headR*0.12)) + (x-cx)*0.38
		}))
	}
	return encodeJPEG(img)
}

// fillSDF paints col wherever the signed distance d (in pixels, negative
// inside the shape) is below zero, with a one-pixel soft edge.
func fillSDF(img *image.NRGBA, col color.NRGBA, d func(x, y float64) float64) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a := 0.5 - d(float64(x)+0.5, float64(y)+0.5)
			if a <= 0 {
				continue
			}
			if a > 1 {
				a = 1
			}
			c := img.NRGBAAt(x, y)
			img.SetNRGBA(x, y, color.NRGBA{
				uint8(float64(c.R)*(1-a) + float64(col.R)*a),
				uint8(float64(c.G)*(1-a) + float64(col.G)*a),
				uint8(float64(c.B)*(1-a) + float64(col.B)*a), 255})
		}
	}
}

// CoverPNG is Cover as PNG, for the few places that want it (icons, tests).
func CoverPNG(spec CoverSpec) ([]byte, error) {
	data, err := Cover(spec)
	if err != nil {
		return nil, err
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	return buf.Bytes(), err
}

func paintGradient(img *image.NRGBA, a, b color.NRGBA, angleDeg float64) {
	bounds := img.Bounds()
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	ang := angleDeg * math.Pi / 180
	dx, dy := math.Cos(ang), math.Sin(ang)
	length := math.Abs(dx)*w + math.Abs(dy)*h
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			t := ((float64(x)-w/2)*dx + (float64(y)-h/2)*dy + length/2) / length
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			img.SetNRGBA(x, y, lerp(a, b, t))
		}
	}
}

func lerp(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		255,
	}
}

func shade(img *image.NRGBA, y int, amount float64) {
	for x := 0; x < img.Bounds().Dx(); x++ {
		c := img.NRGBAAt(x, y)
		img.SetNRGBA(x, y, color.NRGBA{uint8(float64(c.R) * (1 - amount)), uint8(float64(c.G) * (1 - amount)), uint8(float64(c.B) * (1 - amount)), 255})
	}
}

// paintMotif draws a translucent figure in the lighter palette colour.
func paintMotif(img *image.NRGBA, m motif, tint color.NRGBA, seed uint32) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	light := color.NRGBA{255, 255, 255, 255}
	blend := func(x, y int, alpha float64) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return
		}
		c := img.NRGBAAt(x, y)
		img.SetNRGBA(x, y, color.NRGBA{
			uint8(float64(c.R)*(1-alpha) + float64(light.R)*alpha),
			uint8(float64(c.G)*(1-alpha) + float64(light.G)*alpha),
			uint8(float64(c.B)*(1-alpha) + float64(light.B)*alpha), 255})
	}
	cx, cy := float64(w)/2, float64(h)*0.38
	switch m {
	case motifRings:
		for ring := 0; ring < 4; ring++ {
			r := float64(w) * (0.12 + 0.11*float64(ring))
			for a := 0.0; a < 2*math.Pi; a += 0.0015 {
				for t := -3.0; t <= 3.0; t++ {
					blend(int(cx+(r+t)*math.Cos(a)), int(cy+(r+t)*math.Sin(a)), 0.16)
				}
			}
		}
	case motifBars:
		n := 5 + int(seed%4)
		for i := 0; i < n; i++ {
			bx := w*(i+1)/(n+1) - w/40
			bh := int(float64(h) * (0.18 + 0.25*math.Abs(math.Sin(float64(i)*1.3+float64(seed%7)))))
			for y := int(cy) - bh; y < int(cy)+bh; y++ {
				for x := bx; x < bx+w/20; x++ {
					blend(x, y, 0.14)
				}
			}
		}
	case motifDiamond:
		s := float64(w) * 0.34
		for y := -int(s); y <= int(s); y++ {
			span := int(s - math.Abs(float64(y)))
			for x := -span; x <= span; x++ {
				if math.Abs(float64(x))+math.Abs(float64(y)) > s-float64(w)/40 {
					blend(int(cx)+x, int(cy)+y, 0.2)
				}
			}
		}
	case motifWaves:
		for k := 0; k < 4; k++ {
			for x := 0; x < w; x++ {
				y := cy + float64(k-2)*float64(h)*0.06 + math.Sin(float64(x)/float64(w)*math.Pi*2+float64(k))*float64(h)*0.05
				for t := -2; t <= 2; t++ {
					blend(x, int(y)+t, 0.16)
				}
			}
		}
	case motifDots:
		for gy := 0; gy < 6; gy++ {
			for gx := 0; gx < 5; gx++ {
				px := float64(w)*(0.18+0.16*float64(gx)) + float64(gy%2)*float64(w)*0.08
				py := float64(h) * (0.1 + 0.09*float64(gy))
				r := float64(w) / 36
				for y := -int(r); y <= int(r); y++ {
					for x := -int(r); x <= int(r); x++ {
						if float64(x*x+y*y) <= r*r {
							blend(int(px)+x, int(py)+y, 0.18)
						}
					}
				}
			}
		}
	case motifArch:
		r := float64(w) * 0.36
		for a := math.Pi; a <= 2*math.Pi; a += 0.001 {
			for t := -float64(w) / 30; t <= 0; t++ {
				blend(int(cx+(r+t)*math.Cos(a)), int(cy+float64(h)*0.12+(r+t)*math.Sin(a)), 0.15)
			}
		}
	}
	_ = tint
}

// drawKindMark: a small glyph in the corner naming the kind of thing.
func drawKindMark(img *image.NRGBA, kind string, w, h int, tint color.NRGBA) {
	label := map[string]string{"book": "AUDIOBOOK", "album": "ALBUM", "film": "FILM", "show": "SERIES", "podcast": "PODCAST", "ebook": "EBOOK"}[kind]
	if label == "" {
		return
	}
	unit := math.Min(float64(w), float64(h))
	drawText(img, boldFace, unit/30, label, w/14, int(unit/12)+int(unit/30), color.NRGBA{255, 255, 255, 200})
}

func drawText(img *image.NRGBA, f *opentype.Font, size float64, text string, x, y int, col color.NRGBA) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(col), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(text)
}

// wrap breaks a title into lines of at most n characters at word
// boundaries; a single long word is cut.
func wrap(s string, n int) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, wd := range words {
		if len(wd) > n {
			wd = wd[:n-1] + "…"
		}
		if cur == "" {
			cur = wd
		} else if len(cur)+1+len(wd) <= n {
			cur += " " + wd
		} else {
			lines = append(lines, cur)
			cur = wd
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// Backdrop is a 16:9 variant for film/series art and hero banners.
func Backdrop(title, subtitle, kind string) ([]byte, error) {
	return Cover(CoverSpec{Title: title, Subtitle: subtitle, Kind: kind, Width: 1280, Height: 720})
}

// Square is album art.
func Square(title, subtitle string) ([]byte, error) {
	return Cover(CoverSpec{Title: title, Subtitle: subtitle, Kind: "album", Width: 800, Height: 800})
}

// Describe is a one-line label for logs.
func Describe(spec CoverSpec) string {
	return fmt.Sprintf("%s — %s (%s)", spec.Title, spec.Subtitle, spec.Kind)
}
