import { describe, expect, it } from "vitest";
import { alongPolyline, distToTime, lowerIndex, pointAt, timeToDist, wrap01 } from "../src/track/geometry";
import { squareTrack } from "./fixtures";

describe("geometry", () => {
  it("lowerIndex", () => {
    const a = [0, 0.25, 0.5, 0.75];
    expect(lowerIndex(a, -1)).toBe(0);
    expect(lowerIndex(a, 0.3)).toBe(1);
    expect(lowerIndex(a, 0.75)).toBe(3);
    expect(lowerIndex(a, 9)).toBe(3);
    expect(lowerIndex([], 1)).toBe(0);
  });

  it("wraps and follows the centre line", () => {
    const t = squareTrack();
    expect(wrap01(-0.25)).toBeCloseTo(0.75);
    const p = pointAt(t, 0.125);
    expect(p.x).toBeCloseTo(0.5);
    expect(p.y).toBeCloseTo(0);
    const q = pointAt(t, 1.125);
    expect(q.x).toBeCloseTo(p.x);
  });

  it("time and distance mappings are inverse and monotone", () => {
    const t = squareTrack();
    t.timeFrac = t.distFrac.map((d) => d * d * 0.5 + d * 0.5); // non-uniform speed
    let prev = -1;
    for (let f = 0; f <= 1; f += 0.01) {
      const d = timeToDist(t, f);
      expect(d).toBeGreaterThanOrEqual(prev);
      prev = d;
      expect(distToTime(t, d)).toBeCloseTo(f, 2);
    }
  });

  it("walks a polyline by length", () => {
    const pts = [
      { x: 0, y: 0 },
      { x: 1, y: 0 },
      { x: 1, y: 1 },
    ];
    expect(alongPolyline(pts, 0.5)).toEqual({ x: 1, y: 0 });
    expect(alongPolyline(pts, 2).y).toBe(1);
    expect(alongPolyline([], 0.5)).toEqual({ x: 0.5, y: 0.5 });
  });
});
