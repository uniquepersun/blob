package sound

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	master    = 0.45 // quiet: this plays next to someone's work
	maxVoices = 12
)

type voice struct {
	s   []float32
	pos int
}

// Player mixes effects into one oto stream. Play is safe from any
// goroutine and never blocks on audio.
type Player struct {
	mu     sync.Mutex
	fx     [numIDs][][]float32 // pitch variants per effect
	voices []voice
	last   [numIDs]time.Time
	muted  bool
	ready  bool
	shot   string // current shot sound name

	ctx      *oto.Context
	out      *oto.Player
	lastRead time.Time
	log      *os.File // BLOB_SOUND_LOG, nil = no logging
	t0       time.Time
}

func (p *Player) logf(format string, a ...any) {
	if p.log != nil {
		fmt.Fprintf(p.log, "%8.3f "+format+"\n", append([]any{time.Since(p.t0).Seconds()}, a...)...)
	}
}

// New starts audio in the background with the given shot sound (a name
// from Shots; "" picks one of RandomShots). If no device turns up, the
// player stays silent.
func New(muted bool, shot string) *Player {
	return newPlayer(muted, PickShot(shot))
}

// SetShot swaps the shot sound (for auditioning; oto allows one context
// per process, so this reuses the player).
func (p *Player) SetShot(shot string) {
	if p == nil {
		return
	}
	fx := varied(build(shot))
	p.mu.Lock()
	p.fx[Shoot], p.shot = fx[Shoot], shot
	p.mu.Unlock()
}

// Shot is the shot sound in use ("" with no player).
func (p *Player) Shot() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shot
}

func newPlayer(muted bool, shot string) *Player {
	p := &Player{fx: varied(build(shot)), shot: shot, muted: muted, t0: time.Now()}
	if f := os.Getenv("BLOB_SOUND_LOG"); f != "" {
		p.log, _ = os.Create(f)
	}
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   rate,
		ChannelCount: 1,
		Format:       oto.FormatFloat32LE,
		BufferSize:   30 * time.Millisecond,
	})
	if err != nil {
		p.logf("no audio: %v", err)
		return p
	}
	p.ctx = ctx
	go func() {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			p.logf("audio device never became ready")
			return
		}
		out := ctx.NewPlayer(p)
		out.SetBufferSize(4096) // ~23ms: effects land on the frame they belong to
		out.Play()
		p.mu.Lock()
		p.out, p.ready, p.lastRead = out, true, time.Now()
		p.mu.Unlock()
		p.logf("audio ready (muted=%v)", muted)
		p.watch()
	}()
	return p
}

// watch keeps the stream alive. oto stops for good after an audio-queue
// error, and the device can stall (output switched, interruptions), so
// every half second it checks that samples are still being pulled and
// nudges the queue back to life if not. Everything it sees goes to the log.
func (p *Player) watch() {
	var lastErr string
	for range time.Tick(500 * time.Millisecond) {
		p.mu.Lock()
		out, ready, since := p.out, p.ready, time.Since(p.lastRead)
		p.mu.Unlock()
		if !ready {
			return // closed
		}
		if err := p.ctx.Err(); err != nil && err.Error() != lastErr {
			lastErr = err.Error()
			p.logf("context error: %v", err)
		}
		if err := out.Err(); err != nil && err.Error() != lastErr {
			lastErr = err.Error()
			p.logf("player error: %v", err)
		}
		if !out.IsPlaying() {
			p.logf("player stopped; restarting it")
			out.Play()
		}
		if since > time.Second {
			p.logf("no samples pulled for %.1fs; suspend+resume", since.Seconds())
			_ = p.ctx.Suspend()
			_ = p.ctx.Resume()
			p.mu.Lock()
			p.lastRead = time.Now() // give it a second before trying again
			p.mu.Unlock()
		}
	}
}

// Play starts an effect unless muted, rate-limited, or audio is unavailable.
func (p *Player) Play(id ID) {
	if p == nil || id < 0 || id >= numIDs {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ready || p.muted {
		p.logf("play %s skipped (ready=%v muted=%v)", Names[id], p.ready, p.muted)
		return
	}
	p.logf("play %s (stream pulled %.0fms ago)", Names[id], time.Since(p.lastRead).Seconds()*1000)
	now := time.Now()
	if g := minGap[id]; g > 0 && now.Sub(p.last[id]).Seconds() < g {
		return
	}
	p.last[id] = now
	if len(p.voices) >= maxVoices {
		p.voices = p.voices[1:]
	}
	vs := p.fx[id]
	p.voices = append(p.voices, voice{s: vs[rand.Intn(len(vs))]})
}

// Muted reports the current setting.
func (p *Player) Muted() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.muted
}

// SetMuted turns sound off or on; turning it on plays a soft blip so you
// know it worked.
func (p *Player) SetMuted(m bool) {
	p.mu.Lock()
	p.muted = m
	p.logf("muted=%v", m)
	if m {
		p.voices = p.voices[:0]
	}
	p.mu.Unlock()
	if !m {
		p.Play(Toggle)
	}
}

// Read is oto's pull: mix live voices into float32 samples.
func (p *Player) Read(buf []byte) (int, error) {
	n := len(buf) / 4
	p.mu.Lock()
	p.lastRead = time.Now()
	for i := 0; i < n; i++ {
		var v float32
		for j := range p.voices {
			vc := &p.voices[j]
			if vc.pos < len(vc.s) {
				v += vc.s[vc.pos]
				vc.pos++
			}
		}
		// tanh keeps stacked booms from clipping harshly.
		s := float32(math.Tanh(float64(v) * master))
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(s))
	}
	live := p.voices[:0]
	for _, vc := range p.voices {
		if vc.pos < len(vc.s) {
			live = append(live, vc)
		}
	}
	p.voices = live
	p.mu.Unlock()
	return n * 4, nil
}

// Close stops playback.
func (p *Player) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	out := p.out
	p.ready = false
	p.mu.Unlock()
	if out != nil {
		_ = out.Close()
	}
}

// Duration of an effect, for `blob sounds`.
func (p *Player) Duration(id ID) time.Duration {
	return time.Duration(float64(len(p.fx[id][0])) / rate * float64(time.Second))
}

// WaitReady blocks until audio is up or the timeout passes.
func (p *Player) WaitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		p.mu.Lock()
		ok := p.ready
		p.mu.Unlock()
		if ok || !time.Now().Before(deadline) {
			return ok
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// StartMuted decides whether sound starts off: BLOB_SOUND wins over the
// saved setting.
func StartMuted(saved bool) bool {
	switch os.Getenv("BLOB_SOUND") {
	case "0", "off", "false":
		return true
	case "1", "on", "true":
		return false
	}
	return saved
}
