// Package trackgen builds fictional race circuits from a seed.
//
// A circuit is a closed Catmull-Rom spline through randomly perturbed control
// points. From the geometry we derive, with explicit physics, everything the
// race model needs: a speed profile (lateral-grip and braking limits), the
// reference lap time, overtaking zones (long straights followed by a heavy
// braking point), a tyre-wear sensitivity (from cumulated lateral load) and
// the pit-lane time loss. Nothing is hand-tuned per circuit, so every seed
// yields a credible, internally consistent track.
package trackgen

import (
	"fmt"
	"math"
	"sort"

	"pitwall/internal/rng"
)

// Physical constants of the generic car used to derive the speed profile.
const (
	vMax       = 91.0 // m/s, top speed (≈ 328 km/h)
	vMin       = 19.0 // m/s, slowest hairpin speed
	aLat       = 31.0 // m/s², peak lateral acceleration (≈ 3.9 g)
	aBrake     = 38.0 // m/s², peak deceleration
	aAccel     = 8.0  // m/s², mean traction-limited acceleration
	pitSpeed   = 22.2 // m/s, pit-lane speed limit (80 km/h)
	pitExtraS  = 5.5  // s, deceleration into / acceleration out of the pit lane
	samples    = 480  // number of points along the centre line
	raceKmGoal = 255  // km, target race distance
)

// Point is a 2D point in the normalised drawing space [0,1]².
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Corner is a named corner apex.
type Corner struct {
	Label string  `json:"label"`
	At    float64 `json:"at"`    // fraction of lap distance [0,1)
	Speed float64 `json:"speed"` // apex speed, km/h
}

// Zone is an overtaking zone: a straight that ends in a heavy braking point.
type Zone struct {
	From   float64 `json:"from"`   // fraction of lap distance
	To     float64 `json:"to"`     // fraction of lap distance (braking point)
	Length float64 `json:"length"` // m
}

// Track is a generated circuit.
type Track struct {
	Seed         uint64     `json:"-"`
	Name         string     `json:"name"`
	Country      string     `json:"region"`
	LengthM      float64    `json:"lengthM"`      // m
	Laps         int        `json:"laps"`         // race laps
	BaseLapS     float64    `json:"baseLapS"`     // s, reference lap (medium tyre, no fuel, clean air)
	WearFactor   float64    `json:"wearFactor"`   // dimensionless tyre-wear multiplier [0.75, 1.35]
	PitLossS     float64    `json:"pitLossS"`     // s, pit-lane loss excluding the stationary time
	OvertakeEase float64    `json:"overtakeEase"` // dimensionless [0.4, 1.6], 1 = average
	FuelPerLapKg float64    `json:"fuelPerLapKg"` // kg/lap
	Points       []Point    `json:"points"`       // centre line, start/finish at index 0
	DistFrac     []float64  `json:"distFrac"`     // cumulative distance fraction for each point
	TimeFrac     []float64  `json:"timeFrac"`     // cumulative time fraction for each point (speed profile)
	SpeedKmh     []float64  `json:"speedKmh"`     // speed profile, km/h
	Corners      []Corner   `json:"corners"`
	Zones        []Zone     `json:"zones"`
	PitLane      []Point    `json:"pitLane"`
	Sectors      [2]float64 `json:"sectors"` // distance fractions of the S1/S2 and S2/S3 boundaries
}

// Generate returns the circuit for a seed. It is a pure function of seed.
func Generate(seed uint64) *Track {
	r := rng.New(seed).Derive(rng.LabelTrack)
	for attempt := uint64(0); ; attempt++ {
		ar := r.Derive(attempt)
		if t, ok := try(&ar, seed); ok {
			return t
		}
		if attempt > 200 { // practically unreachable; keeps Generate total.
			fallback := rng.New(0).Derive(rng.LabelTrack, 0)
			t, _ := try(&fallback, seed)
			return t
		}
	}
}

func try(r *rng.Stream, seed uint64) (*Track, bool) {
	length := 3900 + r.Float64()*2400 // m
	m, ok := filleted(r, length)
	if !ok {
		return nil, false
	}
	ds := segLengths(m)
	kappa := curvature(m)

	// Rotate so that the start/finish line sits on the longest straight.
	start := longestStraightMid(kappa, ds)
	m = rotate(m, start)
	ds = segLengths(m)
	kappa = curvature(m)

	v := speedProfile(kappa, ds)
	lapT, tf, df := integrate(v, ds)
	if lapT < 60 || lapT > 125 {
		return nil, false
	}
	// minimum corner speed sanity: reject degenerate kinks
	minV := math.Inf(1)
	for _, x := range v {
		minV = math.Min(minV, x)
	}
	if minV < vMin*0.99 {
		return nil, false
	}

	t := &Track{Seed: seed, LengthM: round(length, 1), BaseLapS: round(lapT, 3)}
	t.Name, t.Country = name(r)
	t.Laps = clampInt(int(math.Round(raceKmGoal*1000/length)), 38, 66)
	t.FuelPerLapKg = round(length/1000*0.31, 3)

	// Wear: mean lateral acceleration normalised to the generic range.
	var latSum float64
	for i := range v {
		latSum += v[i] * v[i] * math.Abs(kappa[i]) * ds[i]
	}
	meanLat := latSum / length
	t.WearFactor = round(clamp(0.75+(meanLat-6)/14*0.6, 0.75, 1.35), 3)
	t.WearFactor = round(clamp(t.WearFactor*(0.92+r.Float64()*0.16), 0.75, 1.35), 3) // surface abrasiveness

	t.Zones = zones(v, df, ds)
	ease := 0.4
	for _, z := range t.Zones {
		ease += z.Length / 2000
	}
	t.OvertakeEase = round(clamp(ease, 0.4, 1.6), 3)

	// Pit lane follows the main straight around the start line.
	pitLen := 0.0
	var pitIdx []int
	for k := -1; pitLen < 380; k++ {
		i := (len(m) + k) % len(m)
		pitIdx = append(pitIdx, i)
		pitLen += ds[i]
		if len(pitIdx) > len(m)/4 {
			break
		}
	}
	// pit loss = slow lane - fast track over the same distance + entry/exit
	var trackT float64
	for _, i := range pitIdx {
		trackT += ds[i] / v[i]
	}
	t.PitLossS = round(clamp(pitLen/pitSpeed-trackT+pitExtraS+r.Float64()*4, 16, 27), 2)

	// Normalise to [0,1]² for drawing, preserving aspect ratio.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range m {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	span := math.Max(maxX-minX, maxY-minY)
	ox := (span - (maxX - minX)) / 2
	oy := (span - (maxY - minY)) / 2
	norm := func(p Point) Point {
		return Point{round((p.X-minX+ox)/span, 5), round((p.Y-minY+oy)/span, 5)}
	}
	t.Points = make([]Point, len(m))
	t.SpeedKmh = make([]float64, len(m))
	t.DistFrac = make([]float64, len(m))
	t.TimeFrac = make([]float64, len(m))
	for i, p := range m {
		t.Points[i] = norm(p)
		t.SpeedKmh[i] = round(v[i]*3.6, 1)
		t.DistFrac[i] = round(df[i], 6)
		t.TimeFrac[i] = round(tf[i], 6)
	}
	t.PitLane = pitLane(m, pitIdx, norm, span)
	t.Corners = corners(v, kappa, df)
	t.Sectors = [2]float64{round(sectorAt(tf, df, 1.0/3), 4), round(sectorAt(tf, df, 2.0/3), 4)}
	return t, true
}

// filleted builds the centre line as a star-shaped polygon whose vertices are
// rounded with circular arcs: real straights joined by corners of explicit
// radius (hairpins ≈ 15 m, sweepers ≈ 250 m). The result is scaled to the
// requested length (m) and resampled at uniform arc length.
func filleted(r *rng.Stream, length float64) ([]Point, bool) {
	base := 9 + r.IntN(6)
	var poly []Point
	for i := 0; i < base; i++ {
		ang := 2*math.Pi*float64(i)/float64(base) + (r.Float64()-0.5)*(2*math.Pi/float64(base))*0.7
		rad := 0.45 + r.Float64()*0.55
		if r.Bernoulli(0.22) {
			rad *= 0.5 // inward bite: creates hairpins and complexes
		}
		poly = append(poly, Point{X: math.Cos(-ang) * rad * 1.4, Y: math.Sin(-ang) * rad}) // clockwise
	}
	// Break some long edges with an offset vertex: kinks, esses, chicanes.
	var withKinks []Point
	for i, p := range poly {
		q := poly[(i+1)%len(poly)]
		withKinks = append(withKinks, p)
		l := math.Hypot(q.X-p.X, q.Y-p.Y)
		if l > 0.45 && r.Bernoulli(0.45) {
			f := 0.3 + r.Float64()*0.4
			off := (r.Float64() - 0.5) * 0.22 * l
			nx, ny := -(q.Y-p.Y)/l, (q.X-p.X)/l
			withKinks = append(withKinks, Point{p.X + (q.X-p.X)*f + nx*off, p.Y + (q.Y-p.Y)*f + ny*off})
		}
	}
	poly = withKinks
	n := len(poly)
	per := 0.0
	for i := range poly {
		q := poly[(i+1)%n]
		per += math.Hypot(q.X-poly[i].X, q.Y-poly[i].Y)
	}
	type arc struct {
		a, b   Point // tangent points (entry, exit)
		c      Point // centre
		r      float64
		a0, da float64 // start angle and signed sweep
	}
	arcs := make([]arc, n)
	for i := 0; i < n; i++ {
		p0, p1, p2 := poly[(i-1+n)%n], poly[i], poly[(i+1)%n]
		e1 := Point{p1.X - p0.X, p1.Y - p0.Y}
		e2 := Point{p2.X - p1.X, p2.Y - p1.Y}
		l1, l2 := math.Hypot(e1.X, e1.Y), math.Hypot(e2.X, e2.Y)
		if l1 < 1e-9 || l2 < 1e-9 {
			return nil, false
		}
		u1 := Point{e1.X / l1, e1.Y / l1}
		u2 := Point{e2.X / l2, e2.Y / l2}
		turn := math.Atan2(u1.X*u2.Y-u1.Y*u2.X, u1.X*u2.X+u1.Y*u2.Y)
		at := math.Abs(turn)
		if at > 2.75 { // > 157°: unrealistically tight polygon, retry
			return nil, false
		}
		rad := per * (0.003 + math.Pow(r.Float64(), 2)*0.04)
		if at < 1e-3 {
			at = 1e-3
		}
		tl := rad * math.Tan(at/2)
		maxT := 0.46 * math.Min(l1, l2)
		if tl > maxT {
			tl = maxT
			rad = tl / math.Tan(at/2)
		}
		a := Point{p1.X - u1.X*tl, p1.Y - u1.Y*tl}
		b := Point{p1.X + u2.X*tl, p1.Y + u2.Y*tl}
		sgn := 1.0
		if turn < 0 {
			sgn = -1
		}
		nrm := Point{-u1.Y * sgn, u1.X * sgn} // towards the centre
		c := Point{a.X + nrm.X*rad, a.Y + nrm.Y*rad}
		a0 := math.Atan2(a.Y-c.Y, a.X-c.X)
		arcs[i] = arc{a: a, b: b, c: c, r: rad, a0: a0, da: turn}
	}
	// Dense polyline: arc i, then straight to arc i+1.
	var dense []Point
	for i := 0; i < n; i++ {
		ac := arcs[i]
		steps := 4 + int(math.Abs(ac.da)*ac.r/per*2400)
		for k := 0; k < steps; k++ {
			t := ac.a0 + ac.da*float64(k)/float64(steps)
			dense = append(dense, Point{ac.c.X + ac.r*math.Cos(t), ac.c.Y + ac.r*math.Sin(t)})
		}
		nx := arcs[(i+1)%n]
		dense = append(dense, ac.b, Point{(ac.b.X + nx.a.X) / 2, (ac.b.Y + nx.a.Y) / 2})
	}
	// Resample at uniform arc length.
	total := perimeter(dense)
	out := make([]Point, 0, samples)
	step := total / samples
	seg, acc := 0, 0.0
	for k := 0; k < samples; k++ {
		target := float64(k) * step
		for {
			a, b := dense[seg%len(dense)], dense[(seg+1)%len(dense)]
			l := math.Hypot(b.X-a.X, b.Y-a.Y)
			if acc+l >= target || seg > 4*len(dense) {
				f := 0.0
				if l > 0 {
					f = (target - acc) / l
				}
				out = append(out, Point{a.X + (b.X-a.X)*f, a.Y + (b.Y-a.Y)*f})
				break
			}
			acc += l
			seg++
		}
	}
	if selfIntersects(out) {
		return nil, false
	}
	scale := length / perimeter(out)
	for i := range out {
		out[i].X *= scale
		out[i].Y *= scale
	}
	return out, true
}

func segLengths(p []Point) []float64 {
	ds := make([]float64, len(p))
	for i := range p {
		q := p[(i+1)%len(p)]
		ds[i] = math.Hypot(q.X-p[i].X, q.Y-p[i].Y)
	}
	return ds
}

func perimeter(p []Point) float64 {
	s := 0.0
	for _, d := range segLengths(p) {
		s += d
	}
	return s
}

// curvature returns signed curvature (1/m) estimated from three points.
func curvature(p []Point) []float64 {
	n := len(p)
	k := make([]float64, n)
	for i := 0; i < n; i++ {
		a, b, c := p[(i-1+n)%n], p[i], p[(i+1)%n]
		ab := math.Hypot(b.X-a.X, b.Y-a.Y)
		bc := math.Hypot(c.X-b.X, c.Y-b.Y)
		ca := math.Hypot(a.X-c.X, a.Y-c.Y)
		cross := (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
		den := ab * bc * ca
		if den > 1e-12 {
			k[i] = 2 * cross / den
		}
	}
	// light smoothing
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = (k[(i-1+n)%n] + 2*k[i] + k[(i+1)%n]) / 4
	}
	return out
}

func longestStraightMid(k, ds []float64) int {
	n := len(k)
	const thr = 1.0 / 900 // radius > 900 m counts as straight
	bestLen, bestMid := -1.0, 0
	for s := 0; s < n; s++ {
		if math.Abs(k[s]) >= thr || math.Abs(k[(s-1+n)%n]) < thr {
			continue // not the beginning of a straight
		}
		l := 0.0
		j := s
		for c := 0; c < n && math.Abs(k[j%n]) < thr; c++ {
			l += ds[j%n]
			j++
		}
		if l > bestLen {
			bestLen = l
			bestMid = (s + (j-s)*2/3) % n // line 2/3 down the straight
		}
	}
	return bestMid
}

func rotate(p []Point, start int) []Point {
	out := make([]Point, len(p))
	for i := range p {
		out[i] = p[(i+start)%len(p)]
	}
	return out
}

func speedProfile(k, ds []float64) []float64 {
	n := len(k)
	v := make([]float64, n)
	for i := range k {
		ak := math.Abs(k[i])
		v[i] = vMax
		if ak > 1e-9 {
			v[i] = math.Min(vMax, math.Sqrt(aLat/ak))
		}
		v[i] = math.Max(v[i], vMin)
	}
	// forward (acceleration) and backward (braking) passes, twice around the
	// loop so that the closure is consistent.
	for pass := 0; pass < 2; pass++ {
		for c := 0; c < n; c++ {
			i, j := c, (c+1)%n
			lim := math.Sqrt(v[i]*v[i] + 2*aAccel*(1-v[i]/vMax*0.6)*ds[i])
			if v[j] > lim {
				v[j] = lim
			}
		}
		for c := n - 1; c >= 0; c-- {
			i, j := c, (c+1)%n
			lim := math.Sqrt(v[j]*v[j] + 2*aBrake*ds[i])
			if v[i] > lim {
				v[i] = lim
			}
		}
	}
	return v
}

// integrate returns the lap time and cumulative time/distance fractions.
func integrate(v, ds []float64) (float64, []float64, []float64) {
	n := len(v)
	t := make([]float64, n)
	d := make([]float64, n)
	var tt, dd float64
	for i := 0; i < n; i++ {
		t[i], d[i] = tt, dd
		vv := (v[i] + v[(i+1)%n]) / 2
		tt += ds[i] / vv
		dd += ds[i]
	}
	for i := range t {
		t[i] /= tt
		d[i] /= dd
	}
	return tt, t, d
}

func zones(v, df, ds []float64) []Zone {
	n := len(v)
	var zs []Zone
	// A zone is a straight that ends in a heavy braking point: a local speed
	// maximum followed by a drop of more than 20 m/s. It starts where the car
	// was still below 85% of that peak speed.
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		if v[j] >= v[i]-0.01 || v[i] < v[(i-1+n)%n] {
			continue // not the end of an acceleration phase
		}
		if v[i]-v[(i+12)%n] < 20 {
			continue
		}
		l := 0.0
		s := i
		for c := 0; c < n && v[(s-1+n)%n] > v[i]*0.85; c++ {
			s = (s - 1 + n) % n
			l += ds[s]
		}
		if l > 300 {
			zs = append(zs, Zone{From: round(df[s], 4), To: round(df[i], 4), Length: round(l, 0)})
		}
	}
	sort.Slice(zs, func(a, b int) bool { return zs[a].Length > zs[b].Length })
	if len(zs) > 3 {
		zs = zs[:3]
	}
	sort.Slice(zs, func(a, b int) bool { return zs[a].From < zs[b].From })
	return zs
}

func corners(v, k, df []float64) []Corner {
	n := len(v)
	var cs []Corner
	for i := 0; i < n; i++ {
		a, b := v[(i-1+n)%n], v[(i+1)%n]
		if v[i] <= a && v[i] < b && v[i] < vMax*0.8 && math.Abs(k[i]) > 1.0/600 {
			cs = append(cs, Corner{At: round(df[i], 4), Speed: round(v[i]*3.6, 0)})
		}
	}
	for i := range cs {
		cs[i].Label = fmt.Sprintf("T%d", i+1)
	}
	return cs
}

func sectorAt(tf, df []float64, frac float64) float64 {
	for i := range tf {
		if tf[i] >= frac {
			return df[i]
		}
	}
	return frac
}

func pitLane(m []Point, idx []int, norm func(Point) Point, span float64) []Point {
	off := span * 0.035
	out := make([]Point, 0, len(idx))
	for _, i := range idx {
		a, b := m[(i-1+len(m))%len(m)], m[(i+1)%len(m)]
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		// right-hand normal, i.e. inside of a clockwise lap
		out = append(out, norm(Point{m[i].X - dy/l*off, m[i].Y + dx/l*off}))
	}
	return out
}

// selfIntersects reports whether the closed polyline crosses itself
// (non-adjacent segments only). O(n²) but n is small and this runs once.
func selfIntersects(p []Point) bool {
	n := len(p)
	for i := 0; i < n; i++ {
		a, b := p[i], p[(i+1)%n]
		for j := i + 2; j < n; j++ {
			if i == 0 && j == n-1 {
				continue
			}
			c, d := p[j], p[(j+1)%n]
			if segX(a, b, c, d) {
				return true
			}
		}
	}
	// also reject tracks where two non-adjacent parts come too close
	minD := math.Inf(1)
	for i := 0; i < n; i += 3 {
		for j := i + n/8; j < n-n/8+i && j < n; j += 3 {
			minD = math.Min(minD, math.Hypot(p[i].X-p[j].X, p[i].Y-p[j].Y))
		}
	}
	return minD < 0.08
}

func segX(a, b, c, d Point) bool {
	o := func(p, q, r Point) float64 { return (q.X-p.X)*(r.Y-p.Y) - (q.Y-p.Y)*(r.X-p.X) }
	d1, d2 := o(c, d, a), o(c, d, b)
	d3, d4 := o(a, b, c), o(a, b, d)
	return ((d1 > 0) != (d2 > 0)) && ((d3 > 0) != (d4 > 0))
}

var (
	syl1    = []string{"Ka", "Ve", "Mor", "Sel", "Tra", "Ost", "Lu", "Dar", "Ize", "Qua", "Bel", "Nor", "Ry", "Esk", "Val", "Zan", "Cor", "Ama", "Fen", "Hal"}
	syl2    = []string{"ren", "lia", "dor", "vik", "sa", "mont", "tera", "vel", "nos", "ria", "ston", "bai", "quen", "ro", "lan", "dris", "ma", "thal"}
	suffix  = []string{"Ring", "Park", "Circuit", "Autodrome", "Raceway", "Speedway", "Street Circuit", "Motorpark"}
	regions = []string{"North Varen", "Isle of Kesh", "Ostmark Coast", "Sel Plateau", "Lower Amadris", "Port Halvik", "The Zanmor Basin", "Fennel Bay", "Corvane Hills", "Dar Mesa"}
)

func name(r *rng.Stream) (string, string) {
	n := syl1[r.IntN(len(syl1))] + syl2[r.IntN(len(syl2))]
	return n + " " + suffix[r.IntN(len(suffix))], regions[r.IntN(len(regions))]
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

func clampInt(x, lo, hi int) int {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func round(x float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(x*p) / p
}
