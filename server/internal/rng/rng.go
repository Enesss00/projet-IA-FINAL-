// Package rng provides the only source of randomness allowed in PIT WALL.
//
// It is a SplitMix64 generator with explicit, hierarchical sub-streams:
// every random draw in a race is a pure function of (seed, path...), where
// the path identifies the simulation index, the car, the event channel, etc.
// Nothing here reads the wall clock or touches math/rand's global state, so
// the same seed and the same inputs always produce the same output, whatever
// the number of goroutines that run the simulations.
package rng

import (
	"hash/fnv"
	"math"
)

const golden = 0x9e3779b97f4a7c15

// mix is the SplitMix64 finaliser (Stafford variant 13). It is a bijection on
// uint64 with excellent avalanche, used both to step streams and to derive
// child seeds.
func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Stream is a value-type SplitMix64 generator. Copying a Stream forks it:
// both copies will produce the same sequence. This is what makes race
// snapshots (and therefore parallel universes) exact.
type Stream struct {
	s uint64
}

// New returns a stream seeded with seed.
func New(seed uint64) Stream { return Stream{s: mix(seed ^ 0x5851f42d4c957f2d)} }

// Derive returns an independent child stream identified by the given path.
// Derive(a).Derive(b) and Derive(a, b) are equivalent.
func (r Stream) Derive(path ...uint64) Stream {
	s := r.s
	for _, p := range path {
		s = mix(s ^ mix(p+golden))
	}
	return Stream{s: s}
}

// State exposes the internal state (for hashing / debugging only).
func (r Stream) State() uint64 { return r.s }

// Uint64 returns the next 64 random bits.
func (r *Stream) Uint64() uint64 {
	r.s += golden
	return mix(r.s)
}

// Float64 returns a uniform number in [0, 1) with 53 bits of precision.
func (r *Stream) Float64() float64 {
	return float64(r.Uint64()>>11) * (1.0 / (1 << 53))
}

// Open01 returns a uniform number in the open interval (0, 1).
func (r *Stream) Open01() float64 {
	return (float64(r.Uint64()>>11) + 0.5) * (1.0 / (1 << 53))
}

// IntN returns a uniform integer in [0, n). It returns 0 when n <= 0.
// It uses Lemire's multiply-shift reduction (bias < 2^-32 for n < 2^32,
// negligible for our use).
func (r *Stream) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	hi, _ := mul64(r.Uint64(), uint64(n))
	return int(hi)
}

func mul64(a, b uint64) (hi, lo uint64) {
	const mask32 = 1<<32 - 1
	a0, a1 := a&mask32, a>>32
	b0, b1 := b&mask32, b>>32
	w0 := a0 * b0
	t := a1*b0 + w0>>32
	w1 := t&mask32 + a0*b1
	hi = a1*b1 + t>>32 + w1>>32
	lo = a * b
	return
}

// Bernoulli returns true with probability p (p is clamped to [0, 1]).
func (r *Stream) Bernoulli(p float64) bool {
	return r.Float64() < p
}

// Exp returns an exponential variate with the given mean.
func (r *Stream) Exp(mean float64) float64 {
	return -math.Log(r.Open01()) * mean
}

// Norm returns a standard normal variate, truncated to ±normClip.
//
// It uses a 4096-entry inverse-CDF table with linear interpolation: one
// 64-bit draw, no transcendental call. The truncation at ±3.7σ is a
// documented model hypothesis (docs/MODELS.md) — real lap-to-lap noise has
// bounded support, and the tails are handled by explicit "mistake" events.
func (r *Stream) Norm() float64 {
	u := r.Uint64()
	idx := u >> (64 - normBits)
	frac := float64((u>>(64-normBits-20))&(1<<20-1)) * (1.0 / (1 << 20))
	a := normTable[idx]
	return a + (normTable[idx+1]-a)*frac
}

const (
	normBits = 12
	normSize = 1 << normBits
	normClip = 3.7
)

var normTable [normSize + 1]float64

func init() {
	// Table of the inverse normal CDF at evenly spaced probabilities,
	// clipped to ±normClip so that the extreme cells stay bounded.
	for i := 0; i <= normSize; i++ {
		p := float64(i) / normSize
		normTable[i] = clamp(InvNormCDF(p), -normClip, normClip)
	}
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// InvNormCDF is Acklam's rational approximation of the standard normal
// quantile function (relative error < 1.15e-9). p outside (0,1) maps to ±Inf.
func InvNormCDF(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := [...]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [...]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := [...]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [...]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > 1-plow:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		t := q * q
		return (((((a[0]*t+a[1])*t+a[2])*t+a[3])*t+a[4])*t + a[5]) * q / (((((b[0]*t+b[1])*t+b[2])*t+b[3])*t+b[4])*t + 1)
	}
}

// SeedFromString turns a user-visible seed code (e.g. "NIGHT-42") into a
// 64-bit seed. Pure digits are parsed as a number so that ?seed=42 is
// stable and readable; anything else is hashed with FNV-1a.
func SeedFromString(s string) uint64 {
	var n uint64
	digits := len(s) > 0 && len(s) <= 19
	for i := 0; i < len(s) && digits; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			digits = false
			break
		}
		n = n*10 + uint64(c-'0')
	}
	if digits {
		return n
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// Labels for Derive paths, so that call sites read like sentences.
const (
	LabelTrack uint64 = iota + 1
	LabelGrid
	LabelRivals
	LabelSim
	LabelCar
	LabelEvents
	LabelLive
	LabelOptimizer
	LabelBranch
)
