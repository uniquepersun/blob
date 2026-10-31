package game

import (
	"fmt"
	"os"
	"testing"
)

// go test ./internal/game -run Tune -v   (set BLOB_TUNE=1)
func TestTuneIdlePlayerSurvival(t *testing.T) {
	for _, bots := range []int{3, 5, 8} {
		tuneRun(t, bots)
	}
}

func tuneRun(t *testing.T, bots int) {
	if os.Getenv("BLOB_TUNE") == "" {
		t.Skip("tuning helper")
	}
	total, pops := 0.0, 0
	for seed := int64(0); seed < 40; seed++ {
		w := NewWorld(70, 64, bots, seed)
		p := w.Player()
		for i := 0; i < 60*60; i++ {
			w.Step()
			if !p.Alive {
				total += w.Time
				pops++
				break
			}
		}
	}
	fmt.Printf("%d bots: idle player popped in %d/40 runs, mean time-to-pop %.1fs\n", bots, pops, total/float64(max(1, pops)))
}
