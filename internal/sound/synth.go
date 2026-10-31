// Package sound makes blob's little noises. Every effect is synthesized at
// startup (no audio files), mixed in software, and played through oto, which
// needs no cgo on macOS or Linux. If there is no audio device, everything is
// a silent no-op: sound must never break the game.
package sound

import (
	"math"
	"math/rand"
)

// ID names an effect. The game reports events; the app maps them to IDs.
type ID int

const (
	Shoot ID = iota
	Hit
	Pop
	Pickup
	Quack
	Throw
	Boom
	Nudge  // the agent started working, the corner chip pulses
	Chime  // the agent needs you
	Done   // the agent finished
	Toggle // sound just turned on
	numIDs
)

// Names is for `blob sounds`, which plays each effect so they can be tuned.
var Names = [numIDs]string{"shoot", "hit", "pop", "pickup", "quack", "throw", "boom", "nudge", "chime", "done", "toggle"}

const rate = 44100

// minGap stops rapid-fire effects (burst is 12 shots a second) from
// turning into a buzz: a repeat inside the gap is skipped.
var minGap = [numIDs]float64{Shoot: 0.07, Hit: 0.05, Pop: 0.04, Throw: 0.1}

// gain balances effects against each other; the mixer's master volume
// keeps the whole thing quiet.
var gain = [numIDs]float64{
	Shoot: 0.42, Hit: 0.45, Pop: 0.6, Pickup: 0.5, Quack: 0.5, Throw: 0.25,
	Boom: 0.5, Nudge: 0.35, Chime: 0.75, Done: 0.6, Toggle: 0.4,
}

func secs(s float64) int { return int(s * rate) }

// tone is one synthesized voice: freq glides from f0 to f1, with a quick
// attack and an exponential decay so nothing clicks.
type tone struct {
	at, dur  float64 // start offset and length, seconds
	f0, f1   float64 // pitch glide, Hz
	wave     func(phase float64) float64
	decay    float64 // larger = shorter tail
	vibrato  float64 // Hz of pitch wobble (quack)
	partials float64 // bell-ish second partial level
}

func sine(p float64) float64 { return math.Sin(2 * math.Pi * p) }
func tri(p float64) float64  { return 4*math.Abs(p-math.Floor(p+0.5)) - 1 }

// softSquare is a square with rounded edges: chiptune-ish without the harshness.
func softSquare(p float64) float64 { return math.Tanh(4 * sine(p)) }

// hardSquare is nearly square: more bite for the shot.
func hardSquare(p float64) float64 { return math.Tanh(10 * sine(p)) }

func render(total float64, tones ...tone) []float32 {
	out := make([]float32, secs(total))
	for _, t := range tones {
		start, n := secs(t.at), secs(t.dur)
		phase, phase2 := 0.0, 0.0
		for i := 0; i < n && start+i < len(out); i++ {
			x := float64(i) / float64(n)
			f := t.f0 + (t.f1-t.f0)*x
			if t.vibrato > 0 {
				f *= 1 + 0.06*math.Sin(2*math.Pi*t.vibrato*float64(i)/rate)
			}
			phase += f / rate
			phase2 += f * 2.76 / rate // inharmonic partial: sounds like a small bell
			v := t.wave(phase)
			if t.partials > 0 {
				v = (v + t.partials*sine(phase2)) / (1 + t.partials)
			}
			env := math.Min(1, float64(i)/float64(secs(0.004))) * math.Exp(-t.decay*x)
			env *= math.Min(1, float64(n-i)/float64(secs(0.006))) // fade the very end
			out[start+i] += float32(v * env)
		}
	}
	return out
}

// noise is filtered white noise with a falling cutoff (booms, whooshes).
func noise(dur, cutoff0, cutoff1, decay float64, seed int64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	n := secs(dur)
	out := make([]float32, n)
	lp := 0.0
	for i := range out {
		x := float64(i) / float64(n)
		fc := cutoff0 + (cutoff1-cutoff0)*x
		a := 1 - math.Exp(-2*math.Pi*fc/rate)
		lp += a * (rng.Float64()*2 - 1 - lp)
		env := math.Min(1, float64(i)/float64(secs(0.003))) * math.Exp(-decay*x)
		out[i] = float32(lp * env * 2.2)
	}
	return out
}

func mix(dst, src []float32) []float32 {
	if len(src) > len(dst) {
		dst = append(dst, make([]float32, len(src)-len(dst))...)
	}
	for i, v := range src {
		dst[i] += v
	}
	return dst
}

// Notes used by the jingles.
const (
	c5 = 523.25
	e5 = 659.25
	g5 = 783.99
	c6 = 1046.5
	e6 = 1318.5
	a5 = 880.0
)

// Shots are the most frequent sound, so there are a few to choose from.
// BLOB_SHOT picks one; `blob sounds shots` plays each as a short volley.
var Shots = []string{"pew", "plip", "puff", "tick", "off"}

// RandomShots are what an unchosen shot sound picks from, once per run
// (Abhay's call: random between tick and puff). pew is the punchy one,
// available from the esc menu.
var RandomShots = []string{"tick", "puff"}

// PickShot resolves a choice: "" means random from RandomShots.
func PickShot(choice string) string {
	if choice != "" {
		return choice
	}
	return RandomShots[rand.Intn(len(RandomShots))]
}

func shotSound(name string) []float32 {
	switch name {
	case "pew": // round two of "more aggressive": harder edge, bigger crack and kick
		s := render(0.075, tone{dur: 0.075, f0: 1900, f1: 190, wave: hardSquare, decay: 5})
		s = mix(s, scale(render(0.06, tone{dur: 0.06, f0: 950, f1: 95, wave: hardSquare, decay: 6}), 0.45)) // grit, octave down
		s = mix(s, scale(noise(0.03, 9000, 3000, 4, 5), 0.9))                                               // crack
		s = mix(s, scale(render(0.06, tone{dur: 0.06, f0: 210, f1: 60, wave: sine, decay: 3.5}), 1.1))      // kick
		return scale(s, 0.62)
	case "puff": // a soft breath of air
		return noise(0.07, 1800, 350, 5, 4)
	case "tick": // a tiny wooden tick
		return render(0.03, tone{dur: 0.03, f0: 1900, f1: 1500, wave: tri, decay: 9})
	case "off":
		return make([]float32, secs(0.01))
	default: // plip: a water drop, pitch falling fast
		return render(0.05, tone{dur: 0.05, f0: 1500, f1: 420, wave: sine, decay: 6})
	}
}

// build synthesizes every effect. It takes a few milliseconds.
func build(shot string) [numIDs][]float32 {
	var fx [numIDs][]float32
	fx[Shoot] = shotSound(shot)
	fx[Hit] = render(0.09, tone{dur: 0.09, f0: 320, f1: 170, wave: tri, decay: 4})
	fx[Pop] = mix(
		render(0.12, tone{dur: 0.12, f0: 380, f1: 1250, wave: sine, decay: 3}),
		scale(noise(0.05, 4000, 1500, 6, 1), 0.25))
	fx[Pickup] = render(0.2,
		tone{dur: 0.08, f0: e5, f1: e5, wave: tri, decay: 2},
		tone{at: 0.07, dur: 0.13, f0: a5 * 1.125, f1: a5 * 1.125, wave: tri, decay: 3})
	fx[Quack] = render(0.16, tone{dur: 0.16, f0: 620, f1: 380, wave: softSquare, decay: 2, vibrato: 30})
	fx[Throw] = noise(0.14, 900, 2500, 3, 2)
	fx[Boom] = mix(
		noise(0.5, 1400, 120, 4, 3),
		render(0.4, tone{dur: 0.4, f0: 110, f1: 45, wave: sine, decay: 4}))
	fx[Nudge] = render(0.12, tone{dur: 0.12, f0: 660, f1: 700, wave: sine, decay: 5, partials: 0.3})
	fx[Chime] = render(1.0,
		tone{dur: 0.6, f0: e6, f1: e6, wave: sine, decay: 5, partials: 0.35},
		tone{at: 0.16, dur: 0.8, f0: c6, f1: c6, wave: sine, decay: 5, partials: 0.35})
	fx[Done] = render(0.9,
		tone{dur: 0.3, f0: c5, f1: c5, wave: sine, decay: 4, partials: 0.25},
		tone{at: 0.09, dur: 0.3, f0: e5, f1: e5, wave: sine, decay: 4, partials: 0.25},
		tone{at: 0.18, dur: 0.3, f0: g5, f1: g5, wave: sine, decay: 4, partials: 0.25},
		tone{at: 0.27, dur: 0.6, f0: c6, f1: c6, wave: sine, decay: 4, partials: 0.25})
	fx[Toggle] = render(0.1, tone{dur: 0.1, f0: g5, f1: g5, wave: sine, decay: 5, partials: 0.2})
	for id := range fx {
		fx[id] = scale(fx[id], gain[id])
	}
	return fx
}

// pitched resamples s to play k times faster (k > 1 = higher, shorter).
func pitched(s []float32, k float64) []float32 {
	n := int(float64(len(s)) / k)
	out := make([]float32, n)
	for i := range out {
		x := float64(i) * k
		j := int(x)
		if j+1 >= len(s) {
			break
		}
		f := float32(x - float64(j))
		out[i] = s[j]*(1-f) + s[j+1]*f
	}
	return out
}

// varied gives repeated effects a few pitches to pick from at random, so a
// volley of shots or a run of hits doesn't sound like one stamped sample.
func varied(fx [numIDs][]float32) [numIDs][][]float32 {
	var out [numIDs][][]float32
	for id, s := range fx {
		switch ID(id) {
		case Shoot, Hit, Pop:
			for _, k := range []float64{0.92, 0.96, 1, 1.04, 1.08} {
				out[id] = append(out[id], pitched(s, k))
			}
		default:
			out[id] = [][]float32{s}
		}
	}
	return out
}

func scale(s []float32, k float64) []float32 {
	for i := range s {
		s[i] *= float32(k)
	}
	return s
}
