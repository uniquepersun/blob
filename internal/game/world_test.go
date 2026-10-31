package game

import (
	"math"
	"testing"
)

func TestBotsPlayForAWhileWithoutBreaking(t *testing.T) {
	w := NewWorld(80, 50, 3, 1)
	for i := 0; i < 60*60; i++ { // one simulated minute
		w.Step()
		for _, b := range w.Blobs {
			if math.IsNaN(b.Pos.X) || math.IsNaN(b.Pos.Y) || math.IsNaN(b.Scale) {
				t.Fatalf("NaN on blob %s at step %d", b.Name, i)
			}
			if b.Alive && (b.Pos.X < 0 || b.Pos.Y < 0 || b.Pos.X > w.W || b.Pos.Y > w.H) {
				t.Fatalf("blob %s left the arena: %+v", b.Name, b.Pos)
			}
		}
	}
	score := 0
	for _, b := range w.Blobs {
		score += b.Score
	}
	if score == 0 {
		t.Fatalf("a full minute of bots and nobody popped anyone")
	}
}

func TestFiringShrinksThenOvershoots(t *testing.T) {
	w := NewWorld(80, 50, 0, 2)
	p := w.Player()
	for i := 0; i < 60; i++ { // let the spawn pop-in settle
		w.Step()
	}
	p.In = Controls{Aim: V(1, 0), HasAim: true, Fire: true}
	w.Step()
	p.In.Fire = false
	if len(w.Bullets) != 1 {
		t.Fatalf("want 1 bullet, got %d", len(w.Bullets))
	}
	if p.Scale >= 0.8 {
		t.Fatalf("blob should shrink on fire, scale=%.2f", p.Scale)
	}
	peak := 0.0
	for i := 0; i < 30; i++ {
		w.Step()
		peak = math.Max(peak, p.Scale)
	}
	if peak <= 1.02 {
		t.Fatalf("spring should overshoot past full size (the grow), peak=%.3f", peak)
	}
	if math.Abs(p.Scale-1) > 0.05 {
		t.Fatalf("spring should settle near 1, got %.3f", p.Scale)
	}
}

func TestPopAndRespawn(t *testing.T) {
	w := NewWorld(80, 50, 1, 3)
	victim := w.Blobs[1]
	victim.HP = 1
	w.hit(victim, 0, 1, V(1, 0), ColCream)
	if victim.Alive {
		t.Fatal("blob at 0 HP should pop")
	}
	if w.Player().Score != 1 {
		t.Fatalf("player should get the point, score=%d", w.Player().Score)
	}
	for i := 0; i < 120; i++ {
		w.Step()
	}
	if !victim.Alive || victim.HP != victim.MaxHP {
		t.Fatal("popped blob should bounce back with full HP")
	}
}

func TestBombHurtsNearbyBlobs(t *testing.T) {
	w := NewWorld(80, 50, 1, 4)
	p, bot := w.Player(), w.Blobs[1]
	p.Pos, bot.Pos = V(20, 25), V(30, 25)
	bot.bot.burstIn, bot.bot.burstOn = 99, false
	bot.Shield = 0 // past spawn protection
	w.Bombs = append(w.Bombs, &Bomb{Pos: V(29, 25), Owner: p.ID, Fuse: 0.001})
	hp := bot.HP
	w.Step()
	if bot.HP >= hp && bot.Alive {
		t.Fatalf("bomb should damage the bot, hp %d -> %d", hp, bot.HP)
	}
}

func TestDuckPickup(t *testing.T) {
	w := NewWorld(80, 50, 0, 5)
	p := w.Player()
	w.Pickups = []*Pickup{{Kind: PickDuck, Pos: p.Pos}}
	w.Step()
	if p.DuckTime <= 0 {
		t.Fatal("rubber duck should grant duck mode")
	}
}

func TestSpawnShieldBlocksBullets(t *testing.T) {
	w := NewWorld(80, 50, 1, 6)
	bot := w.Blobs[1]
	bot.Pos = V(40, 25)
	bot.bot.burstIn, bot.bot.burstOn = 99, false
	w.Bullets = append(w.Bullets, &Bullet{Pos: V(40, 25), Vel: V(1, 0), Owner: 0, Life: 1, Dmg: 1})
	w.Step()
	if bot.HP != bot.MaxHP {
		t.Fatal("a freshly spawned blob should be shielded")
	}
}

func TestBotsDontGangUpOnThePlayer(t *testing.T) {
	ganged := 0
	for seed := int64(0); seed < 20; seed++ {
		w := NewWorld(100, 60, 3, seed)
		for i := 0; i < 60*10; i++ {
			w.Step()
			onPlayer := 0
			for _, b := range w.Blobs[1:] {
				if b.bot.target == 0 {
					onPlayer++
				}
			}
			if onPlayer == 3 {
				ganged++
			}
		}
	}
	if frac := float64(ganged) / (20 * 600); frac > 0.15 {
		t.Fatalf("all three bots chased the player %.0f%% of the time", frac*100)
	}
}
