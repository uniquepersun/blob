package render

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/abhay/blob/internal/game"
)

// Look and feel. Dark, misty, a little warm: Silksong-ish dusk, with the
// blobs as the brightest things on screen.
var (
	bgTop     = game.Hex(0x15132A)
	bgBottom  = game.Hex(0x1F1930)
	bgVoid    = game.Hex(0x0C0A16)
	moss      = game.Hex(0x2A2340)
	mossLight = game.Hex(0x302A4A)
	edge      = game.Hex(0x3A3258)
	spore     = game.Hex(0xE9C46A)
	shadow    = game.Hex(0x0B0914)
	eye       = game.Hex(0x1B1826)
	hudBg     = game.Hex(0x100E1C)
	hudFg     = game.Hex(0xCFC6E6)
	hudDim    = game.Hex(0x6E6688)
	cardBg    = game.Hex(0x221C37)
	plum      = game.Hex(0x3B3550)
	white     = game.Hex(0xFFFFFF)
	beak      = game.Hex(0xF08A3C)
)

// UI is everything outside the simulation that the frame needs to show.
type UI struct {
	Time float64 // wall-clock seconds, keeps animating while paused

	Paused      bool
	PauseAccent RGB
	PauseTitle  string
	PauseBody   string
	PauseHint   string

	StatusIcon  string
	StatusText  string
	StatusColor RGB

	Hints string
	Dim   bool // pane not focused (wrapper mode)
}

// Renderer turns a World into terminal lines. It caches the background and
// owns purely decorative state (drifting spores) that the sim doesn't need.
type Renderer struct {
	bg     *Canvas
	frame  *Canvas
	spores []sporeDot
	rng    *rand.Rand
	off    game.Vec // screen-shake offset in pixels
	cols   int
	rows   int
}

type sporeDot struct {
	x, y, speed, sway, phase, size float64
}

func NewRenderer() *Renderer { return &Renderer{rng: rand.New(rand.NewSource(7))} }

// ArenaSize is the world size (in pixels) for a pane of cols×rows cells.
// One row is reserved at the top for the HUD and one at the bottom for hints.
func ArenaSize(cols, rows int) (w, h int) {
	return cols, max(2, (rows-2)*2)
}

func (r *Renderer) ensure(cols, rows int) {
	if r.bg != nil && r.cols == cols && r.rows == rows {
		return
	}
	r.cols, r.rows = cols, rows
	w, h := ArenaSize(cols, rows)
	r.bg = NewCanvas(w, h)
	r.frame = NewCanvas(w, h)
	noise := rand.New(rand.NewSource(42))
	cx, cy := float64(w)/2, float64(h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := Mix(bgTop, bgBottom, float64(y)/float64(max(1, h-1)))
			// Soft vignette toward the void at the edges.
			dx, dy := (float64(x)-cx)/cx, (float64(y)-cy)/cy
			v := math.Min(1, (dx*dx+dy*dy)*0.55)
			c = Mix(c, bgVoid, v*v*0.9)
			// Sparse lichen speckles so the floor isn't flat.
			if n := noise.Float64(); n < 0.007 {
				c = Mix(c, moss, 0.35)
			} else if n < 0.01 {
				c = Mix(c, mossLight, 0.25)
			}
			r.bg.Set(x, y, c)
		}
	}
	// Faint arena edge.
	for x := 0; x < w; x++ {
		r.bg.Blend(x, 0, edge, 0.6)
		r.bg.Blend(x, h-1, edge, 0.6)
	}
	for y := 0; y < h; y++ {
		r.bg.Blend(0, y, edge, 0.6)
		r.bg.Blend(w-1, y, edge, 0.6)
	}
	n := max(8, w*h/140)
	r.spores = r.spores[:0]
	for i := 0; i < n; i++ {
		r.spores = append(r.spores, sporeDot{
			x: noise.Float64() * float64(w), y: noise.Float64() * float64(h),
			speed: 0.6 + noise.Float64()*1.6, sway: 0.6 + noise.Float64()*1.4,
			phase: noise.Float64() * 6.28, size: noise.Float64(),
		})
	}
}

// Render draws a full pane of exactly cols×rows cells.
func (r *Renderer) Render(w *game.World, ui UI, cols, rows int) []string {
	if cols < 24 || rows < 10 {
		return tooSmall(cols, rows)
	}
	r.ensure(cols, rows)
	c := r.frame
	c.CopyFrom(r.bg)

	r.off = game.Vec{}
	if w.Shake > 0 && !ui.Paused {
		amp := w.Shake * 4
		r.off = game.V((r.rng.Float64()-0.5)*amp, (r.rng.Float64()-0.5)*amp)
	}

	r.drawSpores(c, ui.Time)
	for _, p := range w.Pickups {
		r.drawPickup(c, p, ui.Time)
	}
	for _, b := range w.Blobs {
		if b.Alive {
			r.drawShadow(c, b)
		}
	}
	for _, bm := range w.Bombs {
		r.drawBomb(c, bm, ui.Time)
	}
	for _, b := range w.Blobs {
		if b.Alive {
			r.drawBlob(c, w, b, ui.Time)
		}
	}
	for _, bl := range w.Bullets {
		r.drawBullet(c, bl)
	}
	for _, rg := range w.Rings {
		r.drawRing(c, rg)
	}
	for _, p := range w.Particles {
		x, y := r.px(p.Pos)
		a := p.Life / p.MaxLife
		c.Blend(x, y, p.Color, 0.35+0.65*a)
		if p.Big {
			c.Blend(x+1, y, p.Color, 0.3+0.6*a)
		}
	}

	if ui.Paused || ui.Dim {
		k := 0.45
		if !ui.Paused {
			k = 0.25
		}
		for i, px := range c.Pix {
			gray := uint8((int(px.R) + int(px.G) + int(px.B)) / 3)
			c.Pix[i] = Mix(Mix(px, RGB{R: gray, G: gray, B: gray}, 0.4), bgVoid, k)
		}
	}

	// The arena occupies rows 1..rows-2; the canvas is drawn into those rows
	// and the HUD and hint rows are pure text.
	ov := NewOverlay(cols, rows-2)
	if !ui.Paused {
		for _, p := range w.Popups {
			a := p.Life / p.MaxLife
			x, y := r.px(p.Pos)
			txt := p.Text
			col := Mix(c.Get(x, y), p.Color, math.Min(1, a*1.6))
			ov.Text(x-Width(txt)/2, y/2, txt, col, nil, true)
		}
		if pl := w.Player(); pl.Alive {
			x, y := r.px(pl.Pos)
			bob := int(math.Round(math.Sin(ui.Time*3) * 0.4))
			ov.Text(x, (y-int(game.BlobRadius)-3)/2+bob, "▾", game.ColCream, nil, false)
		}
	} else {
		r.drawSleepy(ov, w, ui.Time)
		r.drawCard(ov, ui)
	}

	arena := Lines(c, ov)
	out := make([]string, 0, rows)
	out = append(out, r.hud(w, ui, cols))
	out = append(out, arena...)
	out = append(out, r.hints(ui, cols))
	return out
}

func (r *Renderer) px(p game.Vec) (int, int) {
	return int(math.Floor(p.X + r.off.X)), int(math.Floor(p.Y + r.off.Y))
}

func (r *Renderer) drawSpores(c *Canvas, t float64) {
	for i := range r.spores {
		s := &r.spores[i]
		y := math.Mod(s.y-t*s.speed, float64(c.H))
		if y < 0 {
			y += float64(c.H)
		}
		x := s.x + math.Sin(t*0.7+s.phase)*s.sway
		tw := 0.5 + 0.5*math.Sin(t*1.7+s.phase*3)
		c.Blend(int(x), int(y), spore, (0.08+0.22*s.size)*(0.4+0.6*tw))
	}
}

func (r *Renderer) drawShadow(c *Canvas, b *game.Blob) {
	R := game.BlobRadius * b.Scale
	cx, cy := b.Pos.X+r.off.X, b.Pos.Y+r.off.Y+R*0.75
	for y := int(cy - R*0.5 - 1); y <= int(cy+R*0.5+1); y++ {
		for x := int(cx - R - 1); x <= int(cx+R+1); x++ {
			dx := (float64(x) + 0.5 - cx) / (R * 1.05)
			dy := (float64(y) + 0.5 - cy) / (R * 0.45)
			if d := dx*dx + dy*dy; d < 1 {
				c.Blend(x, y, shadow, 0.45*(1-d))
			}
		}
	}
}

// drawBlob keeps the look flat and readable at 8px: one body color, one
// highlight, a 1px outline (cream for you, darker body color for bots) and
// two tall eyes. Squash/stretch and the springs still drive the shape.
func (r *Renderer) drawBlob(c *Canvas, w *game.World, b *game.Blob, t float64) {
	breath := 1 + 0.03*math.Sin(t*2.4+b.Phase)
	s := math.Max(0.05, b.Scale*breath)
	R := game.BlobRadius * s
	st := math.Max(0.55, b.Stretch)
	cx, cy := b.Pos.X+r.off.X, b.Pos.Y+r.off.Y
	sinA, cosA := math.Sincos(b.StretchAngle)

	inside := func(x, y int) bool {
		dx := float64(x) + 0.5 - cx
		dy := float64(y) + 0.5 - cy
		// Rotate into the stretch frame; stretch is area-preserving.
		u := (dx*cosA + dy*sinA) / (R * st)
		v := (-dx*sinA + dy*cosA) / (R / st)
		return u*u+v*v <= 1
	}

	base := b.Color
	line := Scale(base, 0.5)
	if b.IsPlayer {
		line = game.ColCream
	}
	if b.Shield > 0 && int(t*10)%2 == 0 {
		line = white
	}
	hue := []RGB{game.ColTomato, game.ColMustard, game.ColSage, game.ColPeriwinkle}

	ext := R*1.4 + 1
	for y := int(cy - ext); y <= int(cy+ext); y++ {
		for x := int(cx - ext); x <= int(cx+ext); x++ {
			if !inside(x, y) {
				continue
			}
			nx := (float64(x) + 0.5 - cx) / R
			ny := (float64(y) + 0.5 - cy) / R
			col := base
			edge := !inside(x-1, y) || !inside(x+1, y) || !inside(x, y-1) || !inside(x, y+1)
			switch {
			case edge:
				col = line
				if b.DuckTime > 0 {
					col = hue[int(t*12+math.Atan2(ny, nx)*2+10)%len(hue)]
				}
			case (nx+0.4)*(nx+0.4)+(ny+0.45)*(ny+0.45) < 0.07:
				col = Mix(base, white, 0.5)
			}
			if b.Flash > 0 {
				col = Mix(col, white, 0.75)
			}
			c.Set(x, y, col)
		}
	}

	// Two tall eyes, always side by side, shifted toward where the blob
	// aims; a blink is a one-pixel line.
	if s > 0.45 {
		look := b.Aim.Mul(R * 0.2)
		apart := game.V(R*0.45, 0)
		mid := game.V(cx, cy-R*0.25).Add(look)
		for _, e := range []game.Vec{mid.Add(apart), mid.Sub(apart)} {
			x, y := int(e.X), int(e.Y)
			c.Set(x, y+1, eye)
			if b.Blink <= 0 {
				c.Set(x, y, eye)
			}
			if b.Scale > 1.12 { // happy blush after a pickup or a pop
				c.Blend(x, y+2, game.ColCoral, 0.7)
			}
		}
	}

	if w.Party { // party hat
		hc := []RGB{game.ColTomato, game.ColSage, game.ColMustard, game.ColPeriwinkle}[b.ID%4]
		top := int(cy - R/st - 0.5)
		c.Set(int(cx), top-3, game.ColGold)
		c.Set(int(cx), top-2, hc)
		c.Set(int(cx)-1, top-1, hc)
		c.Set(int(cx), top-1, Mix(hc, white, 0.3))
		c.Set(int(cx)+1, top-1, hc)
	}
}

func (r *Renderer) drawBullet(c *Canvas, bl *game.Bullet) {
	x, y := r.px(bl.Pos)
	tail := bl.Pos.Sub(bl.Vel.Norm().Mul(1.5))
	tx, ty := r.px(tail)
	c.Blend(tx, ty, bl.Color, 0.35)
	c.Set(x, y, Mix(bl.Color, white, 0.3))
	if bl.Heart {
		c.Blend(x+1, y, bl.Color, 0.6)
	}
}

func (r *Renderer) drawBomb(c *Canvas, bm *game.Bomb, t float64) {
	x, y := r.px(bm.Pos)
	if bm.Fuse < 0.55 { // danger zone pulse
		a := 0.12 + 0.12*math.Sin(t*30)
		rad := 13.0
		for yy := y - int(rad); yy <= y+int(rad); yy++ {
			for xx := x - int(rad); xx <= x+int(rad); xx++ {
				d := math.Hypot(float64(xx-x), float64(yy-y))
				if d < rad && d > rad-1.2 {
					c.Blend(xx, yy, game.ColTomato, a*2)
				} else if d < rad {
					c.Blend(xx, yy, game.ColTomato, a*0.4)
				}
			}
		}
	}
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx*dx+dy*dy <= 1 {
				c.Set(x+dx, y+dy, plum)
			}
		}
	}
	c.Set(x-1, y-1, Mix(plum, white, 0.3))
	spark := game.ColGold
	if int(t*(8+20*(1-bm.Fuse)))%2 == 0 {
		spark = game.ColTomato
	}
	c.Set(x+1, y-2, spark)
}

func (r *Renderer) drawRing(c *Canvas, rg *game.Ring) {
	k := 1 - rg.Life/rg.MaxLife
	rad := rg.MaxR * (0.2 + 0.8*k)
	a := rg.Life / rg.MaxLife
	cx, cy := rg.Pos.X+r.off.X, rg.Pos.Y+r.off.Y
	steps := int(rad*7) + 8
	for i := 0; i < steps; i++ {
		ang := float64(i) / float64(steps) * math.Pi * 2
		c.Blend(int(cx+math.Cos(ang)*rad), int(cy+math.Sin(ang)*rad), rg.Color, 0.25+0.6*a)
	}
}

// Pickup sprites. Each rune maps to a color below.
var sprites = map[game.PickupKind][]string{
	game.PickBurst:   {"####.", "#####", "#...."},
	game.PickScatter: {"#...#", ".#.#.", "..#.."},
	game.PickBomb:    {"...*", "..#.", ".###", "####", ".###"},
	game.PickHeart:   {".#.#.", "#####", ".###.", "..#.."},
	game.PickDuck:    {".##..", ".#ob.", "####.", ".###."},
}

func (r *Renderer) drawPickup(c *Canvas, p *game.Pickup, t float64) {
	if left := 18 - p.Age; left < 3 && int(t*8)%2 == 0 {
		return // blink before despawning
	}
	main := map[game.PickupKind]RGB{
		game.PickBurst: game.ColGold, game.PickScatter: game.ColCoral,
		game.PickBomb: plum, game.PickHeart: game.ColTomato, game.PickDuck: game.ColGold,
	}[p.Kind]
	bob := math.Sin(t*3+p.Pos.X) * 0.8
	x0, y0 := r.px(p.Pos.Add(game.V(-2, -2+bob)))
	// soft glow
	gx, gy := r.px(p.Pos)
	for yy := gy - 4; yy <= gy+4; yy++ {
		for xx := gx - 5; xx <= gx+5; xx++ {
			d := math.Hypot(float64(xx-gx), float64(yy-gy)*1.1)
			if d < 5 {
				c.Blend(xx, yy, main, 0.10*(1-d/5)+0.02)
			}
		}
	}
	if p.Kind == game.PickBomb || p.Kind == game.PickDuck {
		main = map[game.PickupKind]RGB{game.PickBomb: Mix(plum, white, 0.25), game.PickDuck: game.ColGold}[p.Kind]
	}
	for j, row := range sprites[p.Kind] {
		for i, ch := range row {
			var col RGB
			switch ch {
			case '#':
				col = main
			case '*':
				col = game.ColGold
				if int(t*6)%2 == 0 {
					col = game.ColTomato
				}
			case 'o':
				col = eye
			case 'b':
				col = beak
			default:
				continue
			}
			c.Set(x0+i, y0+j, col)
		}
	}
}

func (r *Renderer) drawSleepy(ov *Overlay, w *game.World, t float64) {
	for _, b := range w.Blobs {
		if !b.Alive {
			continue
		}
		x, y := int(b.Pos.X), int(b.Pos.Y)
		ph := math.Mod(t*0.8+b.Phase, 1.0)
		row := (y-int(game.BlobRadius))/2 - 1 - int(ph*2)
		col := x + 2 + int(ph*2)
		ov.Text(col, row, "z", Mix(hudDim, hudFg, 1-ph), nil, false)
	}
}

func (r *Renderer) drawCard(ov *Overlay, ui UI) {
	maxInner := max(10, ov.Cols-6)
	textW := maxInner - 2
	type line struct {
		s    string
		kind int // 0 title, 1 body, 2 hint
	}
	var lines []line
	add := func(s string, kind int) {
		for _, l := range wrap(s, textW) {
			lines = append(lines, line{l, kind})
		}
	}
	add(ui.PauseTitle, 0)
	lines = append(lines, line{"", 1})
	for _, l := range strings.Split(ui.PauseBody, "\n") {
		add(l, 1)
	}
	if ui.PauseHint != "" {
		lines = append(lines, line{"", 1})
		add(ui.PauseHint, 2)
	}
	inner := 0
	for _, l := range lines {
		inner = max(inner, Width(l.s))
	}
	inner = min(inner+4, maxInner)
	h := len(lines) + 2
	x0 := (ov.Cols - inner - 2) / 2
	y0 := max(0, (ov.Rows-h)/2)
	pulse := 0.5 + 0.5*math.Sin(ui.Time*4)
	border := Mix(Scale(ui.PauseAccent, 0.6), ui.PauseAccent, pulse)
	bg := cardBg
	ov.Fill(x0, y0, inner+2, h, bg)
	ov.Text(x0, y0, "╭"+strings.Repeat("─", inner)+"╮", border, &bg, false)
	ov.Text(x0, y0+h-1, "╰"+strings.Repeat("─", inner)+"╯", border, &bg, false)
	for i := 1; i < h-1; i++ {
		ov.Text(x0, y0+i, "│", border, &bg, false)
		ov.Text(x0+inner+1, y0+i, "│", border, &bg, false)
	}
	for i, l := range lines {
		fg, bold := hudFg, false
		switch l.kind {
		case 0:
			fg, bold = ui.PauseAccent, true
		case 2:
			fg = hudDim
		}
		ov.Text(x0+1+(inner-Width(l.s))/2, y0+1+i, l.s, fg, &bg, bold)
	}
}

// wrap breaks s into lines of at most w runes on spaces.
func wrap(s string, w int) []string {
	if Width(s) <= w {
		return []string{s}
	}
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case Width(cur)+1+Width(word) <= w:
			cur += " " + word
		default:
			out = append(out, cur)
			cur = word
		}
		for Width(cur) > w {
			r := []rune(cur)
			out = append(out, string(r[:w]))
			cur = string(r[w:])
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (r *Renderer) hud(w *game.World, ui UI, cols int) string {
	ov := NewOverlay(cols, 1)
	ov.Fill(0, 0, cols, 1, hudBg)
	bg := hudBg
	x := 1
	put := func(s string, fg RGB, bold bool) {
		ov.Text(x, 0, s, fg, &bg, bold)
		x += Width(s)
	}
	for _, col := range []RGB{game.ColPeriwinkle, game.ColMustard, game.ColCoral} {
		put("●", col, false)
	}
	put(" blob  ", hudFg, true)

	p := w.Player()
	hearts := ""
	for i := 0; i < p.MaxHP; i++ {
		if i < p.HP && p.Alive {
			hearts += "♥"
		} else {
			hearts += "♡"
		}
	}
	put(hearts, game.ColCoral, false)
	put("  ", hudFg, false)
	switch {
	case p.DuckTime > 0:
		put(fmt.Sprintf("quack %.0fs", math.Ceil(p.DuckTime)), game.ColGold, true)
	case p.Weapon == game.Pea:
		put("pea", hudFg, false)
	default:
		put(fmt.Sprintf("%s %d", p.Weapon, p.Ammo), game.ColGold, false)
	}
	if p.Bombs > 0 {
		put(fmt.Sprintf("  bombs %d", p.Bombs), game.ColTomato, false)
	}
	put(fmt.Sprintf("  pops %d", p.Score), hudDim, false)

	if ui.StatusText != "" {
		s := ui.StatusIcon + " " + ui.StatusText + " "
		ov.Text(cols-Width(s), 0, s, ui.StatusColor, &bg, true)
	}
	return Lines(blankCanvas(cols), ov)[0]
}

func (r *Renderer) hints(ui UI, cols int) string {
	ov := NewOverlay(cols, 1)
	ov.Fill(0, 0, cols, 1, hudBg)
	bg := hudBg
	ov.Text(1, 0, Trunc(ui.Hints, cols-2), hudDim, &bg, false)
	return Lines(blankCanvas(cols), ov)[0]
}

func blankCanvas(cols int) *Canvas { return NewCanvas(cols, 2) }

func tooSmall(cols, rows int) []string {
	out := make([]string, rows)
	msg := "make me bigger"
	for i := range out {
		line := strings.Repeat(" ", max(0, cols))
		if i == rows/2 && cols >= Width(msg) {
			pad := (cols - Width(msg)) / 2
			line = strings.Repeat(" ", pad) + msg + strings.Repeat(" ", cols-pad-Width(msg))
		}
		out[i] = line
	}
	return out
}
