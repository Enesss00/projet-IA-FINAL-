// Client-side strategy helpers. The server is authoritative (it validates
// everything again); these only give instant feedback while editing.
import type { Compound, StrategyOut } from "../net/protocol";

export const MAX_STOPS = 5;

export function toCompact(s: StrategyOut): string {
  return [s.start, ...s.stops.flatMap((x) => [String(x.lap), x.compound])].join("-");
}

export function parseCompact(text: string, name: string): StrategyOut | string {
  const parts = text.trim().toUpperCase().split("-");
  if (parts.length % 2 === 0 || parts.length > 2 * MAX_STOPS + 1)
    return "format : GOMME-TOUR-GOMME… (ex. M-23-H)";
  const isC = (c: string | undefined): c is Compound => c === "S" || c === "M" || c === "H";
  const start = parts[0];
  if (!isC(start)) return `gomme inconnue « ${start ?? ""} »`;
  const stops: StrategyOut["stops"] = [];
  for (let i = 1; i < parts.length; i += 2) {
    const lapS = parts[i] ?? "";
    const c = parts[i + 1];
    if (!/^\d{1,3}$/.test(lapS)) return `tour invalide « ${lapS} »`;
    if (!isC(c)) return `gomme inconnue « ${c ?? ""} »`;
    stops.push({ lap: Number(lapS), compound: c });
  }
  return { name, start, stops };
}

/** Returns a user-facing problem, or null when the plan looks valid. */
export function validate(s: StrategyOut, laps: number): string | null {
  if (!Number.isInteger(laps) || laps < 2) return "course invalide";
  if (s.stops.length > MAX_STOPS) return `${MAX_STOPS} arrêts au maximum`;
  let prev = 0;
  const used = new Set<Compound>([s.start]);
  for (const st of s.stops) {
    if (!Number.isInteger(st.lap) || st.lap < 1)
      return "un arrêt ne peut pas avoir lieu avant la fin du tour 1";
    if (st.lap >= laps) return `arrêt au tour ${st.lap} : la course s'arrête au tour ${laps}`;
    if (st.lap <= prev) return "arrêts dans l'ordre, à des tours distincts";
    prev = st.lap;
    used.add(st.compound);
  }
  if (used.size < 2) return "règlement : deux gommes différentes minimum";
  return null;
}

export interface Stint {
  compound: Compound;
  from: number; // first lap of the stint (1-based)
  to: number; // last lap (inclusive)
}

export function stints(s: StrategyOut, laps: number): Stint[] {
  const out: Stint[] = [];
  let from = 1;
  let c = s.start;
  for (const st of s.stops) {
    out.push({ compound: c, from, to: st.lap });
    from = st.lap + 1;
    c = st.compound;
  }
  out.push({ compound: c, from, to: laps });
  return out;
}

/** Tyre risk of a plan: the worst ratio stint length / cliff lap. */
export function cliffRisk(
  s: StrategyOut,
  laps: number,
  cliffLap: (c: Compound) => number,
): { ratio: number; level: "low" | "medium" | "high"; worst: Stint | null } {
  let worst: Stint | null = null;
  let ratio = 0;
  for (const st of stints(s, laps)) {
    const r = (st.to - st.from + 1) / Math.max(1e-9, cliffLap(st.compound));
    if (!Number.isFinite(r)) {
      ratio = Infinity;
      worst = st;
      break;
    }
    if (r > ratio) {
      ratio = r;
      worst = st;
    }
  }
  // unusable cliff data (NaN, ∞) is never reported as low risk
  const level = !Number.isFinite(ratio) ? "high" : ratio < 0.85 ? "low" : ratio < 1 ? "medium" : "high";
  return { ratio, level, worst };
}

/** Moves stop i to lap, keeping the plan ordered and inside the race. */
export function moveStop(s: StrategyOut, i: number, lap: number, laps: number): StrategyOut {
  const stops = s.stops.map((x) => ({ ...x }));
  const st = stops[i];
  if (!st || !Number.isFinite(lap)) return s;
  const lo = (stops[i - 1]?.lap ?? 0) + 1;
  const hi = (stops[i + 1]?.lap ?? laps) - 1;
  st.lap = Math.max(lo, Math.min(hi, Math.round(lap)));
  return { ...s, stops };
}

const NEXT: Record<Compound, Compound> = { S: "M", M: "H", H: "S" };
export const cycleCompound = (c: Compound): Compound => NEXT[c];

/** Adds a stop in the middle of the longest stint. */
export function addStop(s: StrategyOut, laps: number): StrategyOut {
  if (s.stops.length >= MAX_STOPS) return s;
  const ss = stints(s, laps);
  let best = 0;
  ss.forEach((x, i) => {
    const b = ss[best];
    if (b && x.to - x.from > b.to - b.from) best = i;
  });
  const t = ss[best];
  if (!t || t.to - t.from < 2) return s;
  const lap = Math.floor((t.from + t.to) / 2);
  const stops = [...s.stops, { lap, compound: t.compound === "H" ? ("M" as const) : ("H" as const) }].sort(
    (a, b) => a.lap - b.lap,
  );
  return { ...s, stops };
}

export function removeStop(s: StrategyOut): StrategyOut {
  if (s.stops.length === 0) return s;
  return { ...s, stops: s.stops.slice(0, -1) };
}
