import type { LapView, Track } from "../src/net/protocol";

/** A square track of 4 equal straights; time fraction = distance fraction. */
export function squareTrack(): Track {
  const points = [];
  const distFrac = [];
  const n = 40;
  for (let i = 0; i < n; i++) {
    const f = i / n;
    const side = Math.floor(f * 4);
    const u = f * 4 - side;
    const p = [
      { x: u, y: 0 },
      { x: 1, y: u },
      { x: 1 - u, y: 1 },
      { x: 0, y: 1 - u },
    ][side] ?? { x: 0, y: 0 };
    points.push(p);
    distFrac.push(f);
  }
  return {
    name: "Test Ring",
    region: "Nowhere",
    lengthM: 4000,
    laps: 3,
    baseLapS: 80,
    wearFactor: 1,
    pitLossS: 20,
    overtakeEase: 1,
    fuelPerLapKg: 1.5,
    points,
    distFrac,
    timeFrac: [...distFrac],
    speedKmh: points.map(() => 200),
    corners: [],
    zones: [],
    pitLane: [
      { x: 0, y: 0.2 },
      { x: 0, y: 0.1 },
      { x: 0, y: 0 },
    ],
    sectors: [0.33, 0.66],
    pitEntry: 0.9,
  };
}

export function lap(
  n: number,
  laps: number,
  cars: { car: number; time: number; pitted?: boolean; out?: boolean }[],
): LapView {
  const sorted = [...cars].sort((a, b) => a.time - b.time);
  const lead = sorted[0]?.time ?? 0;
  return {
    lap: n,
    laps,
    flag: n === laps ? "chequered" : "green",
    cars: sorted.map((c, i) => ({
      car: c.car,
      pos: i + 1,
      time: c.time,
      lapTime: 80,
      best: 80,
      gap: c.time - lead,
      interval: i === 0 ? 0 : c.time - (sorted[i - 1]?.time ?? 0),
      compound: "M",
      age: n,
      pits: 0,
      pitted: c.pitted ?? false,
      out: c.out ?? false,
      wear: 0.1,
    })),
    events: [],
  };
}
