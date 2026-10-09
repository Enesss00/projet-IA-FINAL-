// Protocol v1 (docs/PROTOCOL.md). Types mirror server/internal/api.
import { arr, bool, int, intIn, num, obj, oneOf, opt, str, tuple2, type Decoder } from "./guard";

export const PROTOCOL_VERSION = 1;

export type Compound = "S" | "M" | "H";
export const COMPOUNDS: Compound[] = ["S", "M", "H"];
const compound = oneOf<Compound>("S", "M", "H");

export interface Envelope {
  v: number;
  type: string;
  id: string;
  data: unknown;
}

export function decodeEnvelope(raw: string): Envelope | null {
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  if (o.v !== PROTOCOL_VERSION || typeof o.type !== "string") return null;
  return { v: o.v, type: o.type, id: typeof o.id === "string" ? o.id : "", data: o.data };
}

// ---- payloads ----

const point = obj({ x: num, y: num });
export const decodeTrack = obj({
  name: str,
  region: str,
  lengthM: num,
  laps: intIn(1, 500),
  baseLapS: num,
  wearFactor: num,
  pitLossS: num,
  overtakeEase: num,
  fuelPerLapKg: num,
  points: arr(point, 5000),
  distFrac: arr(num, 5000),
  timeFrac: arr(num, 5000),
  speedKmh: arr(num, 5000),
  corners: opt(arr(obj({ label: str, at: num, speed: num }), 200), []),
  zones: opt(arr(obj({ from: num, to: num, length: num }), 20), []),
  pitLane: arr(point, 2000),
  sectors: tuple2(num, num),
  pitEntry: num,
});
export type Track = NonNullable<ReturnType<typeof decodeTrack>>;

const driver = obj({
  name: str,
  code: str,
  team: str,
  teamIdx: int,
  number: int,
  paceS: num,
  sigmaS: num,
  mistakeP: num,
  tyreSave: num,
  defending: num,
});
export type Driver = NonNullable<ReturnType<typeof driver>>;

const stopDTO = obj({ lap: int, compound });
export const decodeStrategy = obj({ name: str, start: compound, stops: opt(arr(stopDTO, 50), []) });
export type Strategy = NonNullable<ReturnType<typeof decodeStrategy>>;
export type Stop = Strategy["stops"][number];

const tyre = obj({
  code: compound,
  name: str,
  paceS: num,
  wearPerLap: num,
  cliffLap: num,
  curve: arr(num, 500),
});
export type TyreView = NonNullable<ReturnType<typeof tyre>>;

const limits = obj({
  maxSims: int,
  maxStrategies: int,
  maxStops: int,
  minSpeed: num,
  maxSpeed: num,
  maxCars: int,
});
export type Limits = NonNullable<ReturnType<typeof limits>>;

const decodeScenarioRaw = obj({
  seed: str,
  cars: intIn(1, 64),
  laps: intIn(1, 500),
  pointsTop: int,
  track: decodeTrack,
  drivers: arr(driver, 64),
  teams: arr(str, 64),
  grid: arr(int, 64),
  player: int,
  suggested: decodeStrategy,
  tyres: arr(tyre, 8),
  limits,
});
export type Scenario = NonNullable<ReturnType<typeof decodeScenarioRaw>>;

/** A permutation of 0..n-1. */
const isPermutation = (a: readonly number[], n: number): boolean =>
  a.length === n && new Set(a).size === n && a.every((x) => x >= 0 && x < n);

/** Scenario decoder with cross-field coherence: the grid, drivers and player
 * must agree with the grid size (the UI allocates per car). */
export const decodeScenario: Decoder<Scenario> = (v) => {
  const s = decodeScenarioRaw(v);
  if (!s) return null;
  const ok =
    s.drivers.length === s.cars &&
    isPermutation(s.grid, s.cars) &&
    s.player >= 0 &&
    s.player < s.cars &&
    s.laps === s.track.laps &&
    s.track.points.length >= 3;
  return ok ? s : null;
};

export const decodeWelcome = obj({ session: str, protocol: int, resumed: bool, limits });
export type Welcome = NonNullable<ReturnType<typeof decodeWelcome>>;

const interval = obj({ p: num, lo: num, hi: num });
export type Interval = NonNullable<ReturnType<typeof interval>>;

const stratStats = obj({
  name: str,
  plan: str,
  n: int,
  hist: arr(int, 64),
  win: interval,
  podium: interval,
  points: interval,
  dnf: interval,
  meanPos: interval,
  medPos: num,
  p95Pos: num,
  cvarPos: num,
  worstPos: int,
  bestPos: int,
  timeMed: interval,
  timeP5: num,
  timeP95: num,
  ahead: arr(num, 8),
});
export type StratStats = NonNullable<ReturnType<typeof stratStats>>;

export const decodeSimProgress = obj({
  run: int,
  done: int,
  total: int,
  races: int,
  strategies: arr(stratStats, 8),
  final: bool,
  truncated: bool,
  pointsTop: int,
  elapsedMs: num,
  reason: opt(str, ""),
});
export type SimProgress = NonNullable<ReturnType<typeof decodeSimProgress>>;

export const decodeSimStarted = obj({ run: int, sims: int, strategies: int });

const carLap = obj({
  car: int,
  pos: int,
  time: num,
  lapTime: num,
  best: num,
  gap: num,
  interval: num,
  compound,
  age: int,
  pits: int,
  pitted: bool,
  out: bool,
  wear: num,
});
export type CarLap = NonNullable<ReturnType<typeof carLap>>;

const raceEvent = obj({
  lap: int,
  kind: oneOf("pit", "pass", "retire", "mistake", "slowstop", "fastest"),
  car: int,
  other: int,
  value: num,
  compound,
});
export type RaceEvent = NonNullable<ReturnType<typeof raceEvent>>;

export const decodeLap = obj({
  lap: int,
  laps: intIn(1, 500),
  flag: oneOf("green", "chequered"),
  cars: arr(carLap, 64),
  events: opt(arr(raceEvent, 2000), []),
});
export type LapView = NonNullable<ReturnType<typeof decodeLap>>;

export const decodeRaceState = obj({
  status: oneOf("running", "paused", "finished", "stopped"),
  speed: num,
  lap: int,
  laps: intIn(1, 500),
  playhead: num,
  autoPaused: bool,
});
export type RaceState = NonNullable<ReturnType<typeof decodeRaceState>>;

export const decodeRaceStarted = obj({
  seed: str,
  cars: intIn(1, 64),
  laps: intIn(1, 500),
  speed: num,
  strategy: decodeStrategy,
});

export const decodeRaceSync = obj({
  seed: str,
  cars: intIn(1, 64),
  strategy: decodeStrategy,
  state: decodeRaceState,
  laps: arr(decodeLap, 500),
});
export type RaceSync = NonNullable<ReturnType<typeof decodeRaceSync>>;

export const decodeError = obj({ code: str, message: str, field: opt(str, "") });
export type ServerError = NonNullable<ReturnType<typeof decodeError>>;

/** All inbound message types and their decoders. */
export const decoders = {
  welcome: decodeWelcome,
  scenario: decodeScenario,
  "sim.started": decodeSimStarted,
  "sim.progress": decodeSimProgress,
  "sim.done": decodeSimProgress,
  "sim.cancelled": (v: unknown) => (v === undefined || typeof v === "object" ? {} : null),
  "race.started": decodeRaceStarted,
  "race.lap": decodeLap,
  "race.state": decodeRaceState,
  "race.sync": decodeRaceSync,
  pong: (v: unknown) => (v === undefined || typeof v === "object" ? {} : null),
  error: decodeError,
} satisfies Record<string, Decoder<unknown>>;

export type InboundType = keyof typeof decoders;
export type Inbound = {
  [K in InboundType]: { type: K; id: string; data: NonNullable<ReturnType<(typeof decoders)[K]>> };
}[InboundType];

/** Decodes a raw frame into a typed message, or null if anything is off. */
export function decodeInbound(raw: string): Inbound | null {
  const env = decodeEnvelope(raw);
  if (!env) return null;
  if (!Object.prototype.hasOwnProperty.call(decoders, env.type)) return null;
  const type = env.type as InboundType;
  const data = decoders[type](env.data);
  if (data === null) return null;
  return { type, id: env.id, data } as Inbound;
}

// ---- outbound ----

export interface StrategyOut {
  name: string;
  start: Compound;
  stops: { lap: number; compound: Compound }[];
}

export type Outbound =
  | { type: "hello"; data: { session: string; client: string } }
  | { type: "ping"; data: Record<string, never> }
  | { type: "scenario.get"; data: { seed: string; cars: number } }
  | {
      type: "sim.start";
      data: { seed: string; cars: number; strategies: StrategyOut[]; sims: number; pace: "live" | "fast" };
    }
  | { type: "sim.cancel"; data: Record<string, never> }
  | { type: "race.start"; data: { seed: string; cars: number; strategy: StrategyOut; speed: number } }
  | {
      type: "race.control";
      data: { action: "pause" | "resume" | "stop" } | { action: "speed"; speed: number };
    };

export function encode(msg: Outbound, id: string): string {
  return JSON.stringify({ v: PROTOCOL_VERSION, type: msg.type, id, data: msg.data });
}
