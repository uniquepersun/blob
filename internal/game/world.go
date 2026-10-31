package game

import (
	"math"
	"math/rand"

	"github.com/charmbracelet/harmonica"
)

// StepDT is the fixed simulation timestep. The app ticks the renderer at
// ~30fps and advances the world in fixed 1/60s steps so physics and springs
// behave the same on every machine.
const StepDT = 1.0 / 60.0

// BlobRadius is the resting radius of every blob, in pixels.
const BlobRadius = 4.0

// spawnShield is how long a fresh blob can't be hit.
const spawnShield = 2.0

// Palette. The four blob colors are lifted straight from the DevF8rge slides.
var (
	ColPeriwinkle = Hex(0x8AB4E8)
	ColMustard    = Hex(0xF5B731)
	ColCoral      = Hex(0xEC7D7A)
	ColTomato     = Hex(0xE8432F)
	ColCream      = Hex(0xF3EAD8)
	ColSage       = Hex(0x7FB7A4)
	ColGold       = Hex(0xFFD86B)

	confetti = []RGB{ColPeriwinkle, ColMustard, ColCoral, ColTomato, ColCream, ColSage}
)

// Controls is what a blob wants to do this step. The player's controls come
// from the input layer; bots fill in their own.
type Controls struct {
	Move      Vec // desired direction, length 0..1
	Aim       Vec // desired aim direction (unit) when HasAim
	HasAim    bool
	Fire      bool // held
	ThrowBomb bool // edge-triggered: consumed by the world
}

// Blob is a player or bot.
type Blob struct {
	ID       int
	Name     string
	Color    RGB
	IsPlayer bool

	Pos, Vel, Aim Vec
	HP, MaxHP     int
	Weapon        WeaponKind
	Ammo          int
	Bombs         int
	Cooldown      float64
	DuckTime      float64 // golden duck power-up remaining

	Alive     bool
	RespawnIn float64
	Shield    float64 // spawn protection: bullets pass through
	Score     int

	// Feel. Scale is a uniform squash (fire = shrink then spring back past 1
	// = the jerky shrink-and-grow). Stretch is an aspect squish along
	// StretchAngle (movement stretch, hit squish).
	Scale, ScaleVel     float64
	Stretch, StretchVel float64
	StretchAngle        float64
	Flash               float64 // white hit flash
	BlinkIn, Blink      float64
	Phase               float64 // idle breathing offset

	In  Controls
	bot botBrain
}

// Bullet is a projectile.
type Bullet struct {
	Pos, Vel Vec
	Owner    int
	Life     float64
	Dmg      int
	Color    RGB
	Heart    bool // duck-mode bullet
}

// Bomb is a thrown bomb with a fuse.
type Bomb struct {
	Pos, Vel Vec
	Owner    int
	Fuse     float64
}

// Pickup lies on the floor until someone rolls over it.
type Pickup struct {
	Kind PickupKind
	Pos  Vec
	Age  float64
}

// Particle is a single colored pixel with velocity and a lifetime.
type Particle struct {
	Pos, Vel      Vec
	Color         RGB
	Life, MaxLife float64
	Drag          float64
	Big           bool
}

// Popup is a floating word ("boop!", "pop!").
type Popup struct {
	Text          string
	Pos           Vec
	Color         RGB
	Life, MaxLife float64
}

// Ring is an expanding shockwave circle.
type Ring struct {
	Pos           Vec
	MaxR          float64
	Life, MaxLife float64
	Color         RGB
}

// Sound is a game event worth a noise. Only things involving the player
// make sounds (except pops and booms), so bots fighting far away stay quiet.
type Sound int

const (
	SndShoot Sound = iota
	SndHit
	SndPop
	SndPickup
	SndQuack
	SndThrow
	SndBoom
)

func (w *World) sound(s Sound) { w.Sounds = append(w.Sounds, s) }

// DrainSounds returns and clears the pending sound events.
func (w *World) DrainSounds() []Sound {
	s := w.Sounds
	w.Sounds = w.Sounds[:0:0]
	return s
}

// World holds the whole simulation. It has no idea it is being drawn in a
// terminal, which keeps it testable and reusable for an SSH server later.
type World struct {
	W, H float64

	Blobs     []*Blob
	Bullets   []*Bullet
	Bombs     []*Bomb
	Pickups   []*Pickup
	Particles []*Particle
	Popups    []*Popup
	Rings     []*Ring

	Time  float64
	Shake float64
	Party bool // konami code easter egg

	// Sounds is what happened this step that is worth a noise. The app
	// drains it each frame; the sim never plays audio itself.
	Sounds []Sound

	rng         *rand.Rand
	pickupTimer float64
	scaleSpring harmonica.Spring
	strSpring   harmonica.Spring
}

// NewWorld creates an arena of w×h pixels with the player and n bots.
func NewWorld(w, h float64, bots int, seed int64) *World {
	wd := &World{
		W: w, H: h,
		rng:         rand.New(rand.NewSource(seed)),
		pickupTimer: 2.0,
		// Underdamped springs give the overshoot that reads as "jelly".
		scaleSpring: harmonica.NewSpring(StepDT, 11.0, 0.28),
		strSpring:   harmonica.NewSpring(StepDT, 8.0, 0.42),
	}
	names := []string{"mochi", "boba", "tofu", "pudding", "dumpling", "taro"}
	colors := []RGB{ColMustard, ColCoral, ColTomato, ColSage, ColCream}

	player := wd.newBlob(0, "you", ColPeriwinkle)
	player.IsPlayer = true
	wd.Blobs = append(wd.Blobs, player)
	for i := 0; i < bots; i++ {
		b := wd.newBlob(i+1, names[i%len(names)], colors[i%len(colors)])
		b.bot.init(wd.rng)
		wd.Blobs = append(wd.Blobs, b)
	}
	for _, b := range wd.Blobs {
		wd.respawn(b)
	}
	return wd
}

func (w *World) newBlob(id int, name string, c RGB) *Blob {
	return &Blob{
		ID: id, Name: name, Color: c,
		MaxHP: 5, Aim: Vec{1, 0},
		Scale: 1, Stretch: 1,
		BlinkIn: 1 + w.rng.Float64()*3,
		Phase:   w.rng.Float64() * math.Pi * 2,
	}
}

// Player returns the human's blob.
func (w *World) Player() *Blob { return w.Blobs[0] }

// Resize changes the arena size and keeps everything inside it.
func (w *World) Resize(width, height float64) {
	w.W, w.H = width, height
	for _, b := range w.Blobs {
		w.keepInside(&b.Pos, BlobRadius)
	}
	for _, p := range w.Pickups {
		w.keepInside(&p.Pos, 3)
	}
}

func (w *World) keepInside(p *Vec, r float64) {
	p.X = clamp(p.X, r+1, math.Max(r+1, w.W-r-1))
	p.Y = clamp(p.Y, r+1, math.Max(r+1, w.H-r-1))
}

// Step advances the simulation by one fixed timestep.
func (w *World) Step() {
	dt := StepDT
	w.Time += dt
	w.Shake = math.Max(0, w.Shake-dt*1.6)

	for _, b := range w.Blobs {
		if !b.Alive {
			b.RespawnIn -= dt
			if b.RespawnIn <= 0 {
				w.respawn(b)
			}
			continue
		}
		if !b.IsPlayer {
			b.bot.think(w, b, dt)
		}
		w.stepBlob(b, dt)
	}
	w.separate()
	w.stepBullets(dt)
	w.stepBombs(dt)
	w.stepPickups(dt)
	w.stepFX(dt)
	if w.Party && w.rng.Float64() < 0.35 {
		w.Particles = append(w.Particles, &Particle{
			Pos:   V(w.rng.Float64()*w.W, -1),
			Vel:   V((w.rng.Float64()-0.5)*6, 10+w.rng.Float64()*8),
			Color: confetti[w.rng.Intn(len(confetti))],
			Life:  6, MaxLife: 6, Drag: 0,
		})
	}
}

func (w *World) stepBlob(b *Blob, dt float64) {
	speed := 36.0
	if !b.IsPlayer {
		speed = 28.0
	}
	in := b.In
	move := in.Move
	if move.Len() > 1 {
		move = move.Norm()
	}
	// Ease toward the target velocity: snappy start, soft stop.
	target := move.Mul(speed)
	b.Vel = b.Vel.Lerp(target, 1-math.Exp(-dt*14))
	b.Pos = b.Pos.Add(b.Vel.Mul(dt))

	// Walls: bounce a little and squish.
	r := BlobRadius
	if b.Pos.X < r || b.Pos.X > w.W-r {
		b.Vel.X *= -0.4
		b.squish(0)
	}
	if b.Pos.Y < r || b.Pos.Y > w.H-r {
		b.Vel.Y *= -0.4
		b.squish(math.Pi / 2)
	}
	w.keepInside(&b.Pos, r)

	if in.HasAim && in.Aim.Len() > 0.01 {
		b.Aim = in.Aim.Norm()
	} else if b.IsPlayer && move.Len() > 0.1 && !in.Fire {
		b.Aim = move.Norm()
	}

	b.Cooldown -= dt
	b.DuckTime = math.Max(0, b.DuckTime-dt)
	if in.Fire && b.Cooldown <= 0 {
		w.fire(b)
	}
	if in.ThrowBomb {
		w.throwBomb(b)
		b.In.ThrowBomb = false
	}

	// Springs. Scale always wants 1. Stretch wants a little elongation along
	// the direction of travel, which makes running blobs look gooey.
	b.Scale, b.ScaleVel = w.scaleSpring.Update(b.Scale, b.ScaleVel, 1.0)
	sp := b.Vel.Len()
	stretchTarget := 1.0 + clamp(sp/speed, 0, 1)*0.16
	if sp > 4 && b.Stretch >= 0.95 {
		b.StretchAngle = b.Vel.Angle()
	}
	b.Stretch, b.StretchVel = w.strSpring.Update(b.Stretch, b.StretchVel, stretchTarget)

	b.Flash = math.Max(0, b.Flash-dt)
	b.Shield = math.Max(0, b.Shield-dt)
	b.Blink = math.Max(0, b.Blink-dt)
	b.BlinkIn -= dt
	if b.BlinkIn <= 0 {
		b.Blink = 0.12
		b.BlinkIn = 2 + w.rng.Float64()*4
	}
}

// squish flattens the blob along the given axis angle (hit, wall bump).
func (b *Blob) squish(angle float64) {
	b.StretchAngle = angle
	b.Stretch = 0.72
	b.StretchVel = 0
}

// separate pushes overlapping blobs apart so they bump like jelly.
func (w *World) separate() {
	for i := 0; i < len(w.Blobs); i++ {
		a := w.Blobs[i]
		if !a.Alive {
			continue
		}
		for j := i + 1; j < len(w.Blobs); j++ {
			c := w.Blobs[j]
			if !c.Alive {
				continue
			}
			d := c.Pos.Sub(a.Pos)
			dist := d.Len()
			min := BlobRadius * 1.8
			if dist < min && dist > 0.001 {
				push := d.Norm().Mul((min - dist) / 2)
				a.Pos = a.Pos.Sub(push)
				c.Pos = c.Pos.Add(push)
			}
		}
	}
}

func (w *World) stepBullets(dt float64) {
	alive := w.Bullets[:0]
	for _, bl := range w.Bullets {
		bl.Pos = bl.Pos.Add(bl.Vel.Mul(dt))
		bl.Life -= dt
		if bl.Life <= 0 || bl.Pos.X < 0 || bl.Pos.Y < 0 || bl.Pos.X > w.W || bl.Pos.Y > w.H {
			continue
		}
		hit := false
		for _, b := range w.Blobs {
			if !b.Alive || b.ID == bl.Owner || b.Shield > 0 {
				continue
			}
			if b.Pos.Dist(bl.Pos) < BlobRadius*b.Scale+0.8 {
				w.hit(b, bl.Owner, bl.Dmg, bl.Vel.Norm(), bl.Color)
				hit = true
				break
			}
		}
		if !hit {
			alive = append(alive, bl)
		}
	}
	w.Bullets = alive
}

var hitWords = []string{"boop", "bonk", "pff", "eep", "oof", "blep"}

func (w *World) hit(b *Blob, owner, dmg int, dir Vec, c RGB) {
	b.HP -= dmg
	b.Flash = 0.09
	b.squish(dir.Angle())
	b.Vel = b.Vel.Add(dir.Mul(26))
	for i := 0; i < 4; i++ {
		w.Particles = append(w.Particles, &Particle{
			Pos:   b.Pos.Sub(dir.Mul(BlobRadius)),
			Vel:   dir.Mul(-12).Rotate((w.rng.Float64() - 0.5) * 2.2).Mul(0.6 + w.rng.Float64()),
			Color: c, Life: 0.3, MaxLife: 0.3, Drag: 5,
		})
	}
	if b.IsPlayer || owner == 0 {
		w.sound(SndHit)
	}
	if w.rng.Float64() < 0.45 {
		w.popup(hitWords[w.rng.Intn(len(hitWords))], b.Pos.Add(V(0, -BlobRadius-2)), ColCream)
	}
	if b.HP <= 0 {
		w.pop(b, owner)
	}
}

// pop is what happens instead of dying: the blob bursts into confetti and
// bounces back a moment later. No death screen, no lives.
func (w *World) pop(b *Blob, killer int) {
	b.Alive = false
	b.RespawnIn = 1.4
	w.Shake = math.Max(w.Shake, 0.18)
	for i := 0; i < 26; i++ {
		c := confetti[w.rng.Intn(len(confetti))]
		if i%3 == 0 {
			c = b.Color
		}
		ang := w.rng.Float64() * math.Pi * 2
		sp := 14 + w.rng.Float64()*30
		w.Particles = append(w.Particles, &Particle{
			Pos: b.Pos, Vel: FromAngle(ang).Mul(sp), Color: c,
			Life: 0.6 + w.rng.Float64()*0.6, MaxLife: 1.2, Drag: 3, Big: i%4 == 0,
		})
	}
	w.Rings = append(w.Rings, &Ring{Pos: b.Pos, MaxR: 9, Life: 0.3, MaxLife: 0.3, Color: b.Color})
	w.popup("pop!", b.Pos, b.Color)
	w.sound(SndPop)
	for _, k := range w.Blobs {
		if k.ID == killer && k.ID != b.ID {
			k.Score++
			k.Scale = 1.3 // proud little hop
		}
	}
}

func (w *World) respawn(b *Blob) {
	b.Alive = true
	b.HP = b.MaxHP
	b.Weapon = Pea
	b.Ammo = 0
	b.Bombs = 0
	b.DuckTime = 0
	b.Vel = Vec{}
	b.Scale = 0.05 // springs back up past 1: a cute pop-in
	b.ScaleVel = 0
	b.Stretch = 1
	b.Shield = spawnShield
	b.Pos = w.freeSpot(14)
}

// freeSpot finds a point at least minDist from every live blob.
func (w *World) freeSpot(minDist float64) Vec {
	m := BlobRadius + 2
	var best Vec
	bestD := -1.0
	for try := 0; try < 24; try++ {
		p := V(m+w.rng.Float64()*math.Max(1, w.W-2*m), m+w.rng.Float64()*math.Max(1, w.H-2*m))
		d := math.Inf(1)
		for _, b := range w.Blobs {
			if b.Alive && b.Pos != p {
				d = math.Min(d, b.Pos.Dist(p))
			}
		}
		if d >= minDist {
			return p
		}
		if d > bestD {
			best, bestD = p, d
		}
	}
	return best
}

func (w *World) popup(text string, p Vec, c RGB) {
	w.Popups = append(w.Popups, &Popup{Text: text, Pos: p, Color: c, Life: 0.9, MaxLife: 0.9})
}

func (w *World) stepFX(dt float64) {
	ps := w.Particles[:0]
	for _, p := range w.Particles {
		p.Life -= dt
		if p.Life <= 0 {
			continue
		}
		p.Vel = p.Vel.Mul(math.Exp(-p.Drag * dt))
		p.Pos = p.Pos.Add(p.Vel.Mul(dt))
		if p.Pos.Y > w.H+2 {
			continue
		}
		ps = append(ps, p)
	}
	w.Particles = ps

	pu := w.Popups[:0]
	for _, p := range w.Popups {
		p.Life -= dt
		p.Pos.Y -= dt * 6
		if p.Life > 0 {
			pu = append(pu, p)
		}
	}
	w.Popups = pu

	rs := w.Rings[:0]
	for _, r := range w.Rings {
		r.Life -= dt
		if r.Life > 0 {
			rs = append(rs, r)
		}
	}
	w.Rings = rs
}

// Celebrate makes every blob hop and throws confetti (agent finished).
func (w *World) Celebrate() {
	for _, b := range w.Blobs {
		if b.Alive {
			b.Scale = 1.4
			b.ScaleVel = 0
		}
	}
	for i := 0; i < 40; i++ {
		w.Particles = append(w.Particles, &Particle{
			Pos:   V(w.rng.Float64()*w.W, w.H*0.3*w.rng.Float64()),
			Vel:   V((w.rng.Float64()-0.5)*20, 6+w.rng.Float64()*14),
			Color: confetti[w.rng.Intn(len(confetti))],
			Life:  2.5, MaxLife: 2.5, Drag: 0.6,
		})
	}
}

// TogglePartyMode is the konami code easter egg.
func (w *World) TogglePartyMode() {
	w.Party = !w.Party
	if w.Party {
		w.popup("party mode!", V(w.W/2, w.H/2), ColGold)
		w.Celebrate()
	}
}
