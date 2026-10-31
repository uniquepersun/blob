// Package render draws the world into the terminal using half-block
// characters: every terminal cell shows two vertical "pixels" (▀ with the
// top pixel as foreground and the bottom one as background), which gives
// roughly square pixels and twice the vertical resolution.
package render

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/abhay/blob/internal/game"
)

type RGB = game.RGB

// Canvas is a pixel buffer. H is always 2× the number of terminal rows.
type Canvas struct {
	W, H int
	Pix  []RGB
}

func NewCanvas(w, h int) *Canvas {
	return &Canvas{W: w, H: h, Pix: make([]RGB, w*h)}
}

func (c *Canvas) in(x, y int) bool { return x >= 0 && y >= 0 && x < c.W && y < c.H }

func (c *Canvas) Get(x, y int) RGB {
	if !c.in(x, y) {
		return RGB{}
	}
	return c.Pix[y*c.W+x]
}

func (c *Canvas) Set(x, y int, col RGB) {
	if c.in(x, y) {
		c.Pix[y*c.W+x] = col
	}
}

// Blend paints col over the pixel with opacity a (0..1).
func (c *Canvas) Blend(x, y int, col RGB, a float64) {
	if c.in(x, y) {
		i := y*c.W + x
		c.Pix[i] = Mix(c.Pix[i], col, a)
	}
}

func (c *Canvas) CopyFrom(o *Canvas) { copy(c.Pix, o.Pix) }

// Mix linearly interpolates two colors.
func Mix(a, b RGB, t float64) RGB {
	t = math.Max(0, math.Min(1, t))
	return RGB{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
	}
}

// Scale multiplies brightness.
func Scale(a RGB, k float64) RGB {
	f := func(v uint8) uint8 { return uint8(math.Max(0, math.Min(255, float64(v)*k))) }
	return RGB{R: f(a.R), G: f(a.G), B: f(a.B)}
}

// Overlay holds text drawn on top of the pixels, one entry per cell.
type Overlay struct {
	Cols, Rows int
	cells      []ovCell
}

type ovCell struct {
	r     rune
	fg    RGB
	bg    RGB
	hasBg bool
	bold  bool
	set   bool
}

func NewOverlay(cols, rows int) *Overlay {
	return &Overlay{Cols: cols, Rows: rows, cells: make([]ovCell, cols*rows)}
}

// Text writes s at (x, y). A nil bg keeps whatever pixels are underneath.
func (o *Overlay) Text(x, y int, s string, fg RGB, bg *RGB, bold bool) {
	if y < 0 || y >= o.Rows {
		return
	}
	for _, r := range s {
		if x >= 0 && x < o.Cols {
			c := ovCell{r: r, fg: fg, bold: bold, set: true}
			if bg != nil {
				c.bg, c.hasBg = *bg, true
			}
			o.cells[y*o.Cols+x] = c
		}
		x++
	}
}

// Fill sets the background of a rectangle of cells to a solid color.
func (o *Overlay) Fill(x, y, w, h int, bg RGB) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			o.Text(xx, yy, " ", bg, &bg, false)
		}
	}
}

// Lines turns pixels + overlay into exactly Rows strings of exactly Cols
// visible columns each, emitting SGR codes only when colors change.
func Lines(c *Canvas, o *Overlay) []string {
	rows := c.H / 2
	out := make([]string, rows)
	var sb strings.Builder
	for row := 0; row < rows; row++ {
		sb.Reset()
		var lastFg, lastBg RGB
		haveFg, haveBg, lastBold := false, false, false
		setFg := func(col RGB) {
			if !haveFg || col != lastFg {
				sb.WriteString(fgSeq(col))
				lastFg, haveFg = col, true
			}
		}
		setBg := func(col RGB) {
			if !haveBg || col != lastBg {
				sb.WriteString(bgSeq(col))
				lastBg, haveBg = col, true
			}
		}
		for x := 0; x < c.W; x++ {
			top := c.Get(x, row*2)
			bot := c.Get(x, row*2+1)
			var oc ovCell
			if o != nil && row < o.Rows && x < o.Cols {
				oc = o.cells[row*o.Cols+x]
			}
			if oc.set {
				bg := Mix(top, bot, 0.5)
				if oc.hasBg {
					bg = oc.bg
				}
				if oc.bold != lastBold {
					if oc.bold {
						sb.WriteString("\x1b[1m")
					} else {
						sb.WriteString("\x1b[22m")
					}
					lastBold = oc.bold
				}
				setBg(bg)
				setFg(oc.fg)
				sb.WriteRune(oc.r)
				continue
			}
			if lastBold {
				sb.WriteString("\x1b[22m")
				lastBold = false
			}
			if top == bot {
				setBg(top)
				sb.WriteByte(' ')
				continue
			}
			setFg(top)
			setBg(bot)
			sb.WriteString("▀")
		}
		sb.WriteString("\x1b[0m")
		out[row] = sb.String()
	}
	return out
}

func fgSeq(c RGB) string {
	return "\x1b[38;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B)) + "m"
}

func bgSeq(c RGB) string {
	return "\x1b[48;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B)) + "m"
}

// Trunc shortens s to at most n runes.
func Trunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}

// Width is the visible width of plain (unstyled) text, assuming every
// rune we use is single-width. The UI sticks to narrow glyphs on purpose.
func Width(s string) int { return utf8.RuneCountInString(s) }
