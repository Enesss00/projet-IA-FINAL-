export function fmtLap(s: number): string {
  if (!Number.isFinite(s) || s <= 0) return "—";
  const m = Math.floor(s / 60);
  const r = s - m * 60;
  return `${m}:${r.toFixed(3).padStart(6, "0")}`;
}

export function fmtRaceTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) return "—";
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  return `${h}:${String(m).padStart(2, "0")}:${r.toFixed(1).padStart(4, "0")}`;
}

export function fmtGap(s: number): string {
  if (!Number.isFinite(s)) return "—";
  return `+${s.toFixed(3)}`;
}

export function fmtPct(p: number, digits = 1): string {
  if (!Number.isFinite(p)) return "—";
  return `${(100 * p).toFixed(digits)}%`;
}

export function fmtInt(n: number): string {
  // thin-space thousands separator, timing-screen style
  return Math.round(n)
    .toString()
    .replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

export function fmtSigned(s: number, digits = 1): string {
  if (!Number.isFinite(s)) return "—";
  const v = s.toFixed(digits);
  return s > 0 ? `+${v}` : s < 0 ? v : `±${v}`;
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}
