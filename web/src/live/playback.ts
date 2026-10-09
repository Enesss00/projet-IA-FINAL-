// Race playback: turns the lap-by-lap timing feed into continuous car
// positions. Between two line crossings a car moves along the circuit
// following the speed profile (time fraction -> distance fraction); on an
// in-lap it leaves the track at the pit entry, drives the pit lane, stops
// for its stationary time and rejoins at the line.
import type { LapView, Track } from "../net/protocol";
import { alongPolyline, distToTime, pointAt, timeToDist, type XY } from "../track/geometry";

export const GRID_SLOT_FRAC = 0.0019; // distance fraction between grid slots
const DEFAULT_STATIONARY_S = 2.4;

export interface CarPose extends XY {
  car: number;
  lap: number; // laps completed + fraction of the current lap
  inPit: boolean;
  out: boolean;
}

export class Playback {
  readonly laps: LapView[] = [];
  /** end[car][k] = race time at the end of lap k+1 */
  private end: number[][] = [];
  private pitted: boolean[][] = [];
  private outAt: number[] = [];
  private stationary = new Map<string, number>();

  constructor(
    private readonly track: Track,
    private readonly cars: number,
    private readonly gridPos: readonly number[],
  ) {
    for (let c = 0; c < cars; c++) {
      this.end.push([]);
      this.pitted.push([]);
      this.outAt.push(Infinity);
    }
  }

  /** Appends the next lap. Returns false if it does not follow the last one
   * (the caller then asks for a resync). */
  add(lv: LapView): boolean {
    if (lv.lap !== this.laps.length + 1) return lv.lap <= this.laps.length; // duplicates are harmless
    this.laps.push(lv);
    for (const cl of lv.cars) {
      if (cl.car < 0 || cl.car >= this.cars) continue;
      const e = this.end[cl.car];
      const p = this.pitted[cl.car];
      if (!e || !p) continue;
      e.push(cl.time);
      p.push(cl.pitted);
      if (cl.out && this.outAt[cl.car] === Infinity) this.outAt[cl.car] = lv.lap;
    }
    for (const ev of lv.events) {
      if (ev.kind === "pit") this.stationary.set(`${ev.car}:${ev.lap}`, ev.value);
    }
    return true;
  }

  /** Race time up to which every car's position is known. Once the
   * chequered flag is in the feed, the playback runs until the last
   * classified car has crossed the line. */
  get available(): number {
    const last = this.laps[this.laps.length - 1];
    if (!last) return 0;
    if (last.flag === "chequered") {
      let t = 0;
      for (const c of last.cars) if (!c.out) t = Math.max(t, c.time);
      return t;
    }
    const leader = last.cars[0];
    return leader ? leader.time : 0;
  }

  get finished(): boolean {
    return this.laps[this.laps.length - 1]?.flag === "chequered";
  }

  /** Leader end time of lap k (1-based); 0 for k = 0. */
  leaderEnd(k: number): number {
    if (k <= 0) return 0;
    const lv = this.laps[k - 1];
    return lv?.cars[0]?.time ?? 0;
  }

  /** The last lap fully completed by the leader at race time T. */
  lapAt(T: number): LapView | null {
    let lo = 0;
    let hi = this.laps.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.leaderEnd(mid + 1) <= T) lo = mid + 1;
      else hi = mid;
    }
    return lo > 0 ? (this.laps[lo - 1] ?? null) : null;
  }

  pose(car: number, T: number): CarPose {
    const e = this.end[car] ?? [];
    const out = this.outAt[car] ?? Infinity;
    const slot = this.gridPos[car] ?? 0;
    // first lap whose end is after T
    let k = 0;
    while (k < e.length && (e[k] ?? 0) <= T) k++;
    if (k + 1 >= out) {
      // retired during lap `out`: parked at the side of the track
      const d = 0.15 + 0.7 * (((car * 37) % 100) / 100);
      return { ...pointAt(this.track, d), car, lap: out - 1, inPit: false, out: true };
    }
    if (k >= e.length) {
      // finished (parc fermé, cars line up past the line) or waiting for
      // data: hold, never extrapolate
      const d = this.finished
        ? 0.012 -
          (this.laps[this.laps.length - 1]?.cars.find((c) => c.car === car)?.pos ?? 1) * GRID_SLOT_FRAC
        : 0;
      return { ...pointAt(this.track, d), car, lap: e.length, inPit: false, out: false };
    }
    const start = k === 0 ? 0 : (e[k - 1] ?? 0);
    const stop = e[k] ?? start + 1;
    const dur = Math.max(1e-6, stop - start);
    const dt = Math.max(0, T - start);
    const d0 = k === 0 ? -slot * GRID_SLOT_FRAC : 0;
    if (this.pitted[car]?.[k]) {
      const stat = this.stationary.get(`${car}:${k + 1}`) ?? DEFAULT_STATIONARY_S;
      const racing = Math.max(dur * 0.5, dur - this.track.pitLossS - stat);
      const tEntry = distToTime(this.track, this.track.pitEntry) * racing;
      if (dt < tEntry) {
        const d = d0 + (1 - d0) * timeToDist(this.track, dt / racing);
        return { ...pointAt(this.track, d), car, lap: k + d, inPit: false, out: false };
      }
      const pitT = Math.max(1e-6, dur - tEntry);
      const drive = Math.max(1e-6, pitT - stat);
      const v = dt - tEntry;
      const boxU = 0.55;
      let u: number;
      if (v < drive * boxU) u = (v / (drive * boxU)) * boxU;
      else if (v < drive * boxU + stat) u = boxU;
      else u = boxU + ((v - drive * boxU - stat) / (drive * (1 - boxU))) * (1 - boxU);
      const p = alongPolyline(this.track.pitLane, Math.min(1, u));
      const lapF = this.track.pitEntry + (1 - this.track.pitEntry) * Math.min(1, u);
      return { ...p, car, lap: k + lapF, inPit: true, out: false };
    }
    const d = d0 + (1 - d0) * timeToDist(this.track, dt / dur);
    return { ...pointAt(this.track, d), car, lap: k + d, inPit: false, out: false };
  }
}
