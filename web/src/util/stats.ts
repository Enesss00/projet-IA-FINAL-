// Small statistics helpers used for display (the server computes the
// authoritative numbers; these derive per-bin intervals from raw counts).
export const Z95 = 1.959963984540054;

export function wilson(k: number, n: number): { p: number; lo: number; hi: number } {
  if (n <= 0) return { p: 0, lo: 0, hi: 1 };
  const p = k / n;
  const z2 = Z95 * Z95;
  const den = 1 + z2 / n;
  const centre = (p + z2 / (2 * n)) / den;
  const half = (Z95 * Math.sqrt((p * (1 - p)) / n + z2 / (4 * n * n))) / den;
  return { p, lo: Math.max(0, centre - half), hi: Math.min(1, centre + half) };
}

/** Critically damped spring step towards target (frame-rate independent). */
export function spring(cur: number, vel: number, target: number, dt: number, omega = 9): [number, number] {
  const x = cur - target;
  const e = Math.exp(-omega * dt);
  const nx = (x + (vel + omega * x) * dt) * e;
  const nv = (vel - omega * (vel + omega * x) * dt) * e;
  return [target + nx, nv];
}
