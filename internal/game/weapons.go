package game

import (
	"math"
)

// WeaponKind is the gun a blob is holding.
type WeaponKind int

const (
	Pea     WeaponKind = iota // default, infinite, slow and friendly
	Burst                     // assault blaster: fast, slight spread
	Scatter                   // five-pellet shotgun
)

func (k WeaponKind) String() string {
	switch k {
	case Burst:
		return "burst"
	case Scatter:
		return "scatter"
	default:
		return "pea"
	}
}

// PickupKind is something lying in the arena.
type PickupKind int

const (
	PickBurst PickupKind = iota
	PickScatter
	PickBomb
	PickHeart
	PickDuck // rare golden rubber duck: the debugging easter egg
)

var pickupWeights = []struct {
	kind PickupKind
	w    int
}{
	{PickBurst, 30}, {PickScatter, 24}, {PickBomb, 26}, {PickHeart, 15}, {PickDuck, 5},
}

const (
	maxPickups   = 4
	pickupLife   = 18.0
	bombFuse     = 1.1
	bombRadius   = 13.0
	bombMaxDmg   = 3
	duckDuration = 8.0
	// Bots shoot slower than you. It's a cozy game.
	botFireSlowdown = 1.7
)

func (w *World) fire(b *Blob) {
	aim := b.Aim
	muzzle := b.Pos.Add(aim.Mul(BlobRadius + 1))

	shoot := func(dir Vec, speed, life float64, c RGB, heart bool) {
		w.Bullets = append(w.Bullets, &Bullet{
			Pos: muzzle, Vel: dir.Mul(speed).Add(b.Vel.Mul(0.3)),
			Owner: b.ID, Life: life, Dmg: 1, Color: c, Heart: heart,
		})
	}

	kick := 0.74 // how far the blob shrinks on the shot before springing back
	switch {
	case b.DuckTime > 0:
		c := confetti[int(w.Time*20)%len(confetti)]
		shoot(aim.Rotate((w.rng.Float64()-0.5)*0.12), 80, 0.9, c, true)
		b.Cooldown = 0.075
		kick = 0.86
	case b.Weapon == Burst:
		shoot(aim.Rotate((w.rng.Float64()-0.5)*0.18), 78, 0.9, ColGold, false)
		b.Cooldown = 0.085
		kick = 0.86
		w.useAmmo(b)
	case b.Weapon == Scatter:
		for i := -2; i <= 2; i++ {
			spread := float64(i)*0.16 + (w.rng.Float64()-0.5)*0.08
			shoot(aim.Rotate(spread), 66+w.rng.Float64()*10, 0.4, ColCoral, false)
		}
		b.Cooldown = 0.55
		kick = 0.62
		w.useAmmo(b)
	default:
		shoot(aim, 62, 1.1, ColCream, false)
		b.Cooldown = 0.3
	}

	if !b.IsPlayer {
		b.Cooldown *= botFireSlowdown
	} else {
		w.sound(SndShoot)
	}

	// The jerk: shrink instantly, the underdamped spring overshoots back
	// past full size (the "grow"), then settles.
	b.Scale = math.Min(b.Scale, kick)
	b.ScaleVel = 0
	b.Vel = b.Vel.Sub(aim.Mul(10)) // recoil
	for i := 0; i < 2; i++ {
		w.Particles = append(w.Particles, &Particle{
			Pos: muzzle, Vel: aim.Rotate((w.rng.Float64() - 0.5) * 1.4).Mul(18),
			Color: ColGold, Life: 0.12, MaxLife: 0.12, Drag: 8,
		})
	}
}

func (w *World) useAmmo(b *Blob) {
	b.Ammo--
	if b.Ammo <= 0 {
		b.Weapon = Pea
		b.Ammo = 0
		if b.IsPlayer {
			w.popup("click", b.Pos.Add(V(0, -BlobRadius-2)), ColCream)
		}
	}
}

func (w *World) throwBomb(b *Blob) {
	if b.Bombs <= 0 {
		return
	}
	b.Bombs--
	w.Bombs = append(w.Bombs, &Bomb{
		Pos:   b.Pos.Add(b.Aim.Mul(BlobRadius + 1)),
		Vel:   b.Aim.Mul(64).Add(b.Vel.Mul(0.5)),
		Owner: b.ID, Fuse: bombFuse,
	})
	b.Scale = 0.7
	b.ScaleVel = 0
	if b.IsPlayer {
		w.sound(SndThrow)
	}
}

func (w *World) stepBombs(dt float64) {
	live := w.Bombs[:0]
	for _, bm := range w.Bombs {
		bm.Vel = bm.Vel.Mul(math.Exp(-2.6 * dt))
		bm.Pos = bm.Pos.Add(bm.Vel.Mul(dt))
		if bm.Pos.X < 1 || bm.Pos.X > w.W-1 {
			bm.Vel.X = -bm.Vel.X * 0.6
		}
		if bm.Pos.Y < 1 || bm.Pos.Y > w.H-1 {
			bm.Vel.Y = -bm.Vel.Y * 0.6
		}
		w.keepInside(&bm.Pos, 1)
		bm.Fuse -= dt
		if bm.Fuse <= 0 {
			w.explode(bm)
			continue
		}
		live = append(live, bm)
	}
	w.Bombs = live
}

func (w *World) explode(bm *Bomb) {
	w.Shake = math.Max(w.Shake, 0.45)
	w.sound(SndBoom)
	w.Rings = append(w.Rings,
		&Ring{Pos: bm.Pos, MaxR: bombRadius, Life: 0.35, MaxLife: 0.35, Color: ColGold},
		&Ring{Pos: bm.Pos, MaxR: bombRadius * 0.6, Life: 0.25, MaxLife: 0.25, Color: ColCream},
	)
	for i := 0; i < 36; i++ {
		ang := w.rng.Float64() * math.Pi * 2
		c := ColGold
		switch i % 3 {
		case 1:
			c = ColTomato
		case 2:
			c = ColCream
		}
		w.Particles = append(w.Particles, &Particle{
			Pos: bm.Pos, Vel: FromAngle(ang).Mul(20 + w.rng.Float64()*45), Color: c,
			Life: 0.35 + w.rng.Float64()*0.4, MaxLife: 0.75, Drag: 4, Big: i%5 == 0,
		})
	}
	w.popup("kaboom", bm.Pos, ColGold)
	for _, b := range w.Blobs {
		if !b.Alive || b.Shield > 0 {
			continue
		}
		d := b.Pos.Dist(bm.Pos)
		if d > bombRadius+BlobRadius {
			continue
		}
		dmg := int(math.Ceil(float64(bombMaxDmg) * (1 - d/(bombRadius+BlobRadius))))
		if dmg < 1 {
			dmg = 1
		}
		dir := b.Pos.Sub(bm.Pos).Norm()
		if dir.Len() == 0 {
			dir = V(0, -1)
		}
		w.hit(b, bm.Owner, dmg, dir, ColGold)
		b.Vel = b.Vel.Add(dir.Mul(40))
	}
}

func (w *World) stepPickups(dt float64) {
	w.pickupTimer -= dt
	if w.pickupTimer <= 0 {
		w.pickupTimer = 3.5 + w.rng.Float64()*3
		if len(w.Pickups) < maxPickups {
			w.spawnPickup()
		}
	}
	live := w.Pickups[:0]
	for _, p := range w.Pickups {
		p.Age += dt
		if p.Age > pickupLife {
			w.burst(p.Pos, ColCream, 6)
			continue
		}
		taken := false
		for _, b := range w.Blobs {
			if b.Alive && b.Pos.Dist(p.Pos) < BlobRadius+3 {
				w.collect(b, p)
				taken = true
				break
			}
		}
		if !taken {
			live = append(live, p)
		}
	}
	w.Pickups = live
}

func (w *World) spawnPickup() {
	total := 0
	for _, pw := range pickupWeights {
		total += pw.w
	}
	r := w.rng.Intn(total)
	kind := PickBurst
	for _, pw := range pickupWeights {
		if r < pw.w {
			kind = pw.kind
			break
		}
		r -= pw.w
	}
	w.spawnPickupKind(kind)
}

func (w *World) spawnPickupKind(kind PickupKind) {
	p := &Pickup{Kind: kind, Pos: w.freeSpot(12)}
	w.Pickups = append(w.Pickups, p)
	w.burst(p.Pos, ColCream, 5)
}

func (w *World) collect(b *Blob, p *Pickup) {
	b.Scale = 1.28
	b.ScaleVel = 0
	label := ""
	switch p.Kind {
	case PickBurst:
		b.Weapon, b.Ammo = Burst, 40
		label = "burst!"
	case PickScatter:
		b.Weapon, b.Ammo = Scatter, 10
		label = "scatter!"
	case PickBomb:
		b.Bombs += 2
		label = "+2 bombs"
	case PickHeart:
		b.HP = min(b.MaxHP, b.HP+2)
		label = "<3"
	case PickDuck:
		b.DuckTime = duckDuration
		label = "quack!"
	}
	if b.IsPlayer {
		if p.Kind == PickDuck {
			w.sound(SndQuack)
		} else {
			w.sound(SndPickup)
		}
	}
	w.burst(p.Pos, ColGold, 8)
	w.popup(label, b.Pos.Add(V(0, -BlobRadius-2)), ColGold)
}

func (w *World) burst(p Vec, c RGB, n int) {
	for i := 0; i < n; i++ {
		w.Particles = append(w.Particles, &Particle{
			Pos: p, Vel: FromAngle(w.rng.Float64() * math.Pi * 2).Mul(8 + w.rng.Float64()*10),
			Color: c, Life: 0.35, MaxLife: 0.35, Drag: 5,
		})
	}
}
