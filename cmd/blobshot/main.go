// Command blobshot runs a terminal program inside a virtual terminal, plays a
// small script of keys and agent events at it, and saves PNG screenshots.
// It is how blob is tested headlessly, and it's handy for README and pitch
// images without screen-recording software.
//
//	blobshot -cols 120 -rows 36 -sock /tmp/t.sock \
//	  wait:1 hold:d:1.2 key:space shot:a.png event:attention:wants\ to\ use\ Bash \
//	  wait:0.5 shot:b.png -- ./bin/blob
//
// Steps:
//
//	wait:SECS              sleep
//	keys:TEXT              type text (\e \r \t escapes allowed)
//	key:NAME               one key: up down left right space enter esc tab ctrl+]
//	hold:KEY:SECS          simulate holding a key (press + OS auto-repeat)
//	mouse:X,Y / click:X,Y / rclick:X,Y / release:X,Y   SGR mouse at cell X,Y
//	event:KIND[:MESSAGE]   send an agent event to -sock (working/attention/done)
//	shot:FILE.png          save a screenshot
//	text:FILE.txt          save the screen as plain text
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/uniquepersun/blob/internal/ipc"
)

const cellW, cellH = 7, 14

func main() {
	cols := flag.Int("cols", 120, "terminal columns")
	rows := flag.Int("rows", 36, "terminal rows")
	sock := flag.String("sock", "", "blob socket for event: steps (also exported as BLOB_SOCKET)")
	flag.Parse()

	var steps, argv []string
	all := flag.Args()
	for i, a := range all {
		if a == "--" {
			steps, argv = all[:i], all[i+1:]
			break
		}
	}
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: blobshot [flags] steps... -- command [args...]")
		os.Exit(2)
	}

	emu := vt.NewSafeEmulator(*cols, *rows)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	if *sock != "" {
		cmd.Env = append(cmd.Env, ipc.EnvSocket+"="+*sock)
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(*rows), Cols: uint16(*cols)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	go func() { _, _ = io.Copy(emu, ptmx) }()
	go func() { _, _ = io.Copy(ptmx, emu) }() // terminal query replies

	write := func(s string) { _, _ = ptmx.Write([]byte(s)) }
	for _, st := range steps {
		kind, arg, _ := strings.Cut(st, ":")
		switch kind {
		case "wait":
			sleep(arg)
		case "keys":
			write(unescape(arg))
		case "key":
			write(named(arg))
		case "hold":
			k, secs, _ := strings.Cut(arg, ":")
			d, _ := strconv.ParseFloat(secs, 64)
			seq := named(k)
			end := time.Now().Add(time.Duration(d * float64(time.Second)))
			write(seq)
			time.Sleep(300 * time.Millisecond) // OS repeat delay
			for time.Now().Before(end) {
				write(seq)
				time.Sleep(40 * time.Millisecond)
			}
		case "mouse", "click", "rclick", "release":
			x, y := xy(arg)
			b, suffix := 35, "M" // motion, no button
			switch kind {
			case "click":
				b = 0
			case "rclick":
				b = 2
			case "release":
				b, suffix = 0, "m"
			}
			write(fmt.Sprintf("\x1b[<%d;%d;%d%s", b, x+1, y+1, suffix))
		case "event":
			k, msg, _ := strings.Cut(arg, ":")
			if err := ipc.Send(*sock, ipc.Event{Agent: "claude", Kind: ipc.Kind(k), Message: msg}); err != nil {
				fmt.Fprintln(os.Stderr, "event:", err)
			}
		case "shot":
			if err := savePNG(emu, arg); err != nil {
				fmt.Fprintln(os.Stderr, "shot:", err)
			}
		case "text":
			_ = os.WriteFile(arg, []byte(emu.String()), 0o644)
		default:
			fmt.Fprintln(os.Stderr, "unknown step:", st)
		}
	}
	_ = cmd.Process.Signal(os.Interrupt)
	write("q")
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func sleep(s string) {
	d, _ := strconv.ParseFloat(s, 64)
	time.Sleep(time.Duration(d * float64(time.Second)))
}

func xy(s string) (int, int) {
	a, b, _ := strings.Cut(s, ",")
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return x, y
}

func named(k string) string {
	switch k {
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	case "space":
		return " "
	case "enter":
		return "\r"
	case "esc":
		return "\x1b"
	case "tab":
		return "\t"
	case "ctrl+]":
		return "\x1d"
	case "ctrl+c":
		return "\x03"
	}
	return k
}

func unescape(s string) string {
	r := strings.NewReplacer(`\e`, "\x1b", `\r`, "\r", `\n`, "\n", `\t`, "\t")
	return r.Replace(s)
}

var (
	defFg = color.RGBA{0xd0, 0xd0, 0xd0, 0xff}
	defBg = color.RGBA{0x0a, 0x0a, 0x0f, 0xff}
)

func rgba(c color.Color, def color.RGBA) color.RGBA {
	if c == nil {
		return def
	}
	r, g, b, _ := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
}

func savePNG(emu *vt.SafeEmulator, path string) error {
	cols, rows := emu.Width(), emu.Height()
	img := image.NewRGBA(image.Rect(0, 0, cols*cellW, rows*cellH))
	face := basicfont.Face7x13
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			c := emu.CellAt(x, y)
			fg, bg, content := defFg, defBg, " "
			if c != nil {
				fg, bg = rgba(c.Style.Fg, defFg), rgba(c.Style.Bg, defBg)
				if c.Style.Attrs&uv.AttrReverse != 0 {
					fg, bg = bg, fg
				}
				content = c.Content
				if c.Width == 0 {
					continue
				}
			}
			r := image.Rect(x*cellW, y*cellH, (x+1)*cellW, (y+1)*cellH)
			top := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+cellH/2)
			bot := image.Rect(r.Min.X, r.Min.Y+cellH/2, r.Max.X, r.Max.Y)
			switch content {
			case "▀":
				draw.Draw(img, top, image.NewUniform(fg), image.Point{}, draw.Src)
				draw.Draw(img, bot, image.NewUniform(bg), image.Point{}, draw.Src)
			case "▄":
				draw.Draw(img, top, image.NewUniform(bg), image.Point{}, draw.Src)
				draw.Draw(img, bot, image.NewUniform(fg), image.Point{}, draw.Src)
			case "█":
				draw.Draw(img, r, image.NewUniform(fg), image.Point{}, draw.Src)
			default:
				draw.Draw(img, r, image.NewUniform(bg), image.Point{}, draw.Src)
				if content != "" && content != " " {
					d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face,
						Dot: fixed.P(r.Min.X, r.Min.Y+11)}
					d.DrawString(content)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
