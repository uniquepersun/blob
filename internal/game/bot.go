package game

import (
	"math"
	"math/rand"
)

// botBrain is a deliberately gentle opponent: it wanders, strafes, grabs
// loot, and shoots in short bursts with a wobbly aim. The goal is a game
// that feels alive while you wait, not one that stresses you out.
type botBrain struct {
	target     int
	retarget   float64
	strafe     float64 // +1 / -1
	strafeIn   float64
	burstOn    bool
	burstIn    float64
	aimWobble  float64
	wobbleVel  float64
	preferDist float64
}

func (bb *botBrain) init(r *rand.Rand) {
	bb.target = -1
	bb.strafe = 1
	if r.Intn(2) == 0 {
		bb.strafe = -1
	}
	bb.preferDist = 22 + r.Float64()*12
}

func (bb *botBrain) think(w *World, me *Blob, dt float64) {
	bb.retarget -= dt
	bb.strafeIn -= dt
	bb.burstIn -= dt

	tgt := w.blobByID(bb.target)
	if tgt == nil || !tgt.Alive || bb.retarget <= 0 {
		tgt = w.pickTarget(me)
		if tgt != nil {
			bb.target = tgt.ID
		}
		bb.retarget = 2.5 + w.rng.Float64()*3
	}
	if bb.strafeIn <= 0 {
		bb.strafe = -bb.strafe
		bb.strafeIn = 1.0 + w.rng.Float64()*1.8
	}
	if bb.burstIn <= 0 {
		bb.burstOn = !bb.burstOn
		if bb.burstOn {
			bb.burstIn = 0.4 + w.rng.Float64()*0.6
		} else {
			bb.burstIn = 1.0 + w.rng.Float64()*1.4
		}
	}

	var move Vec
	me.In.Fire = false
	me.In.HasAim = false

	// Loot first if we're only holding the pea shooter.
	if p := w.nearestPickup(me.Pos); p != nil && (me.Weapon == Pea || p.Kind == PickHeart && me.HP < me.MaxHP) {
		if me.Pos.Dist(p.Pos) < 40 {
			move = p.Pos.Sub(me.Pos).Norm()
		}
	}

	if tgt != nil {
		to := tgt.Pos.Sub(me.Pos)
		dist := to.Len()
		dir := to.Norm()
		pref := bb.preferDist
		if tgt.IsPlayer {
			pref += 10 // keep a polite distance from you
		}
		if move.Len() == 0 {
			switch {
			case dist > pref+6:
				move = dir
			case dist < pref-6:
				move = dir.Mul(-1)
			}
			move = move.Add(dir.Perp().Mul(bb.strafe * 0.8))
		}

		// Wobbly aim with a little lead on the target.
		bb.wobbleVel += (w.rng.Float64() - 0.5) * 6 * dt
		bb.wobbleVel *= math.Exp(-2 * dt)
		wob := 0.28
		if tgt.IsPlayer {
			wob = 0.42 // bots are worse shots against you
		}
		wob *= w.Tune.BotWobble
		bb.aimWobble = clamp(bb.aimWobble+bb.wobbleVel, -wob, wob)
		lead := tgt.Vel.Mul(dist / 70)
		aim := tgt.Pos.Add(lead).Sub(me.Pos).Norm().Rotate(bb.aimWobble)
		me.In.Aim, me.In.HasAim = aim, true

		if bb.burstOn && dist < 60 {
			me.In.Fire = true
		}
		if me.Bombs > 0 && dist > 16 && dist < 38 && w.rng.Float64() < 0.6*dt {
			me.In.ThrowBomb = true
		}
	}

	// Run away from live bombs.
	for _, bm := range w.Bombs {
		if d := me.Pos.Dist(bm.Pos); d < bombRadius+4 {
			move = move.Add(me.Pos.Sub(bm.Pos).Norm().Mul(1.6))
		}
	}
	// Don't hug walls.
	m := 8.0
	if me.Pos.X < m {
		move.X += 0.8
	}
	if me.Pos.X > w.W-m {
		move.X -= 0.8
	}
	if me.Pos.Y < m {
		move.Y += 0.8
	}
	if me.Pos.Y > w.H-m {
		move.Y -= 0.8
	}
	me.In.Move = move
}

func (w *World) blobByID(id int) *Blob {
	for _, b := range w.Blobs {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// pickTarget prefers the nearest blob, but treats the player as further
// away than they are and avoids piling onto someone another bot already
// chases, so the bots squabble among themselves instead of ganging up.
func (w *World) pickTarget(me *Blob) *Blob {
	chased := map[int]int{}
	for _, b := range w.Blobs {
		if b != me && !b.IsPlayer && b.Alive {
			chased[b.bot.target]++
		}
	}
	var best *Blob
	bd := math.Inf(1)
	for _, b := range w.Blobs {
		if b == me || !b.Alive {
			continue
		}
		d := b.Pos.Dist(me.Pos) * (1 + 0.6*float64(chased[b.ID]))
		if b.IsPlayer {
			d *= 2.2 * w.Tune.BotShy
		}
		if d < bd {
			best, bd = b, d
		}
	}
	return best
}

func (w *World) nearestPickup(p Vec) *Pickup {
	var best *Pickup
	bd := math.Inf(1)
	for _, pk := range w.Pickups {
		if d := pk.Pos.Dist(p); d < bd {
			best, bd = pk, d
		}
	}
	return best
}
