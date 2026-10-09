// Pure geometry helpers on the generated track.
import type { Track } from "../net/protocol";

export interface XY {
  x: number;
  y: number;
}

/** Index of the last element <= v in a sorted array (binary search). */
export function lowerIndex(a: readonly number[], v: number): number {
  let lo = 0;
  let hi = a.length - 1;
  if (hi < 0) return 0;
  if (v <= (a[0] ?? 0)) return 0;
  if (v >= (a[hi] ?? 0)) return hi;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if ((a[mid] ?? 0) <= v) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

/** Wraps a distance fraction to [0, 1). */
export const wrap01 = (f: number): number => f - Math.floor(f);

/** Point on the centre line at a distance fraction (any real number). */
export function pointAt(t: Track, distFrac: number): XY {
  const f = wrap01(distFrac);
  const n = t.points.length;
  if (n === 0) return { x: 0.5, y: 0.5 };
  const i = lowerIndex(t.distFrac, f);
  const a = t.points[i] ?? { x: 0, y: 0 };
  const b = t.points[(i + 1) % n] ?? a;
  const d0 = t.distFrac[i] ?? 0;
  const d1 = i + 1 < n ? (t.distFrac[i + 1] ?? 1) : 1;
  const u = d1 > d0 ? (f - d0) / (d1 - d0) : 0;
  return { x: a.x + (b.x - a.x) * u, y: a.y + (b.y - a.y) * u };
}

/** Converts a fraction of lap *time* into a fraction of lap *distance*,
 * using the speed profile: cars are slow in corners, fast on straights. */
export function timeToDist(t: Track, timeFrac: number): number {
  const f = Math.max(0, Math.min(1, timeFrac));
  const n = t.timeFrac.length;
  if (n === 0) return f;
  const i = lowerIndex(t.timeFrac, f);
  const t0 = t.timeFrac[i] ?? 0;
  const t1 = i + 1 < n ? (t.timeFrac[i + 1] ?? 1) : 1;
  const d0 = t.distFrac[i] ?? 0;
  const d1 = i + 1 < n ? (t.distFrac[i + 1] ?? 1) : 1;
  const u = t1 > t0 ? (f - t0) / (t1 - t0) : 0;
  return d0 + (d1 - d0) * u;
}

/** Inverse of timeToDist. */
export function distToTime(t: Track, distFrac: number): number {
  const f = Math.max(0, Math.min(1, distFrac));
  const n = t.distFrac.length;
  if (n === 0) return f;
  const i = lowerIndex(t.distFrac, f);
  const d0 = t.distFrac[i] ?? 0;
  const d1 = i + 1 < n ? (t.distFrac[i + 1] ?? 1) : 1;
  const t0 = t.timeFrac[i] ?? 0;
  const t1 = i + 1 < n ? (t.timeFrac[i + 1] ?? 1) : 1;
  const u = d1 > d0 ? (f - d0) / (d1 - d0) : 0;
  return t0 + (t1 - t0) * u;
}

/** Point along a polyline at fraction u in [0, 1] (by length). */
export function alongPolyline(pts: readonly XY[], u: number): XY {
  if (pts.length === 0) return { x: 0.5, y: 0.5 };
  if (pts.length === 1) return pts[0] ?? { x: 0.5, y: 0.5 };
  let total = 0;
  const seg: number[] = [];
  for (let i = 0; i + 1 < pts.length; i++) {
    const a = pts[i] ?? { x: 0, y: 0 };
    const b = pts[i + 1] ?? a;
    const l = Math.hypot(b.x - a.x, b.y - a.y);
    seg.push(l);
    total += l;
  }
  let target = Math.max(0, Math.min(1, u)) * total;
  for (let i = 0; i < seg.length; i++) {
    const l = seg[i] ?? 0;
    if (target <= l || i === seg.length - 1) {
      const a = pts[i] ?? { x: 0, y: 0 };
      const b = pts[i + 1] ?? a;
      const v = l > 0 ? Math.min(1, target / l) : 0;
      return { x: a.x + (b.x - a.x) * v, y: a.y + (b.y - a.y) * v };
    }
    target -= l;
  }
  return pts[pts.length - 1] ?? { x: 0.5, y: 0.5 };
}
