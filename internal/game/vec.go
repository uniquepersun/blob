package game

import "math"

// Vec is a 2D vector in arena pixel units. One pixel is one terminal column
// wide and half a terminal row tall, so pixels are roughly square on screen.
type Vec struct{ X, Y float64 }

func V(x, y float64) Vec                { return Vec{x, y} }
func (a Vec) Add(b Vec) Vec             { return Vec{a.X + b.X, a.Y + b.Y} }
func (a Vec) Sub(b Vec) Vec             { return Vec{a.X - b.X, a.Y - b.Y} }
func (a Vec) Mul(s float64) Vec         { return Vec{a.X * s, a.Y * s} }
func (a Vec) Dot(b Vec) float64         { return a.X*b.X + a.Y*b.Y }
func (a Vec) Len() float64              { return math.Hypot(a.X, a.Y) }
func (a Vec) Dist(b Vec) float64        { return a.Sub(b).Len() }
func (a Vec) Angle() float64            { return math.Atan2(a.Y, a.X) }
func (a Vec) Perp() Vec                 { return Vec{-a.Y, a.X} }
func (a Vec) Lerp(b Vec, t float64) Vec { return a.Add(b.Sub(a).Mul(t)) }

// Norm returns the unit vector, or zero for a zero vector.
func (a Vec) Norm() Vec {
	l := a.Len()
	if l < 1e-9 {
		return Vec{}
	}
	return Vec{a.X / l, a.Y / l}
}

// Rotate rotates the vector by theta radians.
func (a Vec) Rotate(theta float64) Vec {
	s, c := math.Sincos(theta)
	return Vec{a.X*c - a.Y*s, a.X*s + a.Y*c}
}

// FromAngle returns a unit vector pointing at angle theta.
func FromAngle(theta float64) Vec {
	s, c := math.Sincos(theta)
	return Vec{c, s}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// RGB is a plain 24-bit color. The game owns colors so the renderer stays
// a pure "draw what the world says" layer.
type RGB struct{ R, G, B uint8 }

// Hex builds an RGB from 0xRRGGBB.
func Hex(h uint32) RGB { return RGB{uint8(h >> 16), uint8(h >> 8), uint8(h)} }
