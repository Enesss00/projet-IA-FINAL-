// Live convergence view: finishing-position distribution of every strategy
// (bars spring towards each new estimate, with per-bin 95% Wilson whiskers)
// and, on the right, the "funnel": the estimate of the selected probability
// with its confidence band against the number of simulated races (log
// scale) — the band visibly narrows as 1/sqrt(N).
import type { StratStats } from "../net/protocol";
import { STRAT_COLORS, Surface } from "./surface";
import { spring, wilson } from "../util/stats";

export type Metric = "win" | "podium" | "points";
export interface TrailPoint {
  n: number;
  stats: StratStats[];
}

interface Bar {
  v: number;
  vel: number;
  lo: number;
  hi: number;
}

export class ConvergenceChart {
  private s: Surface;
  private bars: Bar[][] = [];
  private stats: StratStats[] = [];
  private trail: TrailPoint[] = [];
  private total = 1;
  private cars = 10;
  private pointsTop = 5;
  private yMax = 0.5;
  private yVel = 0;
  private flash = 0;
  private last = 0;
  metric: Metric = "podium";

  constructor(parent: HTMLElement) {
    this.s = new Surface(parent);
  }

  setGrid(cars: number, pointsTop: number): void {
    this.cars = cars;
    this.pointsTop = pointsTop;
  }

  reset(): void {
    this.bars = [];
    this.stats = [];
    this.trail = [];
    this.flash = 0;
  }

  update(stats: StratStats[], trail: TrailPoint[], total: number): void {
    // change magnitude drives a brief brightening: visible "breathing"
    let change = 0;
    stats.forEach((st, s) => {
      const n = Math.max(1, st.n);
      st.hist.forEach((k, i) => {
        const b = this.bars[s]?.[i];
        if (b) change += Math.abs(k / n - b.v);
      });
    });
    this.flash = Math.min(1, this.flash + change * 3);
    this.stats = stats;
    this.trail = trail;
    this.total = Math.max(1, total);
    stats.forEach((st, s) => {
      const row = (this.bars[s] ??= []);
      st.hist.forEach((k, i) => {
        const w = wilson(k, st.n);
        const b = (row[i] ??= { v: 0, vel: 0, lo: 0, hi: 0 });
        b.lo = w.lo;
        b.hi = w.hi;
      });
    });
  }

  draw(now: number): void {
    const dt = this.last ? Math.min(0.05, (now - this.last) / 1000) : 0.016;
    this.last = now;
    this.s.resize();
    this.s.clear();
    const g = this.s.ctx;
    const W = this.s.w;
    const H = this.s.h;
    const funnelW = W > 760 ? Math.round(W * 0.34) : 0;
    const hx0 = 46;
    const hx1 = W - funnelW - (funnelW ? 26 : 14);
    const top = 64;
    const bottom = H - 30;

    // targets & springs
    let peak = 0.05;
    this.stats.forEach((st, s) => {
      const row = this.bars[s] ?? [];
      st.hist.forEach((k, i) => {
        const b = row[i];
        if (!b) return;
        const target = st.n > 0 ? k / st.n : 0;
        [b.v, b.vel] = spring(b.v, b.vel, target, dt);
        peak = Math.max(peak, b.hi, target);
      });
    });
    [this.yMax, this.yVel] = spring(this.yMax, this.yVel, Math.min(1, peak * 1.12), dt, 5);
    this.flash *= Math.exp(-dt * 3);

    const bins = this.cars + 1;
    const gw = (hx1 - hx0) / bins;
    const y = (p: number) => bottom - (p / Math.max(0.02, this.yMax)) * (bottom - top);

    // points zone & podium zone
    g.fillStyle = "rgba(61,255,160,0.025)";
    g.fillRect(hx0, top, gw * this.pointsTop, bottom - top);
    g.fillStyle = "rgba(255,176,33,0.04)";
    g.fillRect(hx0, top, gw * Math.min(3, this.cars), bottom - top);

    // y grid
    g.font = '10px "JetBrains Mono", monospace';
    g.textAlign = "right";
    g.textBaseline = "middle";
    const step = this.yMax > 0.5 ? 0.2 : this.yMax > 0.2 ? 0.1 : 0.05;
    for (let p = 0; p <= this.yMax + 1e-9; p += step) {
      const yy = Math.round(y(p)) + 0.5;
      g.strokeStyle = p === 0 ? "#2a3a4c" : "#121a23";
      g.lineWidth = 1;
      g.beginPath();
      g.moveTo(hx0, yy);
      g.lineTo(hx1, yy);
      g.stroke();
      g.fillStyle = "#4a5a6c";
      g.fillText(`${Math.round(p * 100)}%`, hx0 - 6, yy);
    }

    // bars
    const ns = Math.max(1, this.stats.length);
    const bw = (gw * 0.78) / ns;
    this.stats.forEach((_, s) => {
      const col = STRAT_COLORS[s % STRAT_COLORS.length] ?? "#fff";
      const row = this.bars[s] ?? [];
      for (let i = 0; i < bins; i++) {
        const b = row[i];
        if (!b) continue;
        const x = hx0 + i * gw + gw * 0.11 + s * bw;
        const yt = y(Math.max(0, b.v));
        g.globalAlpha = 0.55 + 0.35 * this.flash;
        g.fillStyle = col;
        g.fillRect(x, yt, bw - 1, bottom - yt);
        g.globalAlpha = 1;
        g.fillRect(x, yt - 1, bw - 1, 2);
        // CI whisker
        if (b.hi - b.lo > 0.002 && (this.stats[s]?.n ?? 0) > 0) {
          const cx = x + (bw - 1) / 2;
          g.strokeStyle = "rgba(242,246,250,0.7)";
          g.lineWidth = 1;
          g.beginPath();
          g.moveTo(cx, y(b.lo));
          g.lineTo(cx, y(b.hi));
          g.moveTo(cx - 2, y(b.lo));
          g.lineTo(cx + 2, y(b.lo));
          g.moveTo(cx - 2, y(b.hi));
          g.lineTo(cx + 2, y(b.hi));
          g.stroke();
        }
      }
    });

    // x labels
    g.textAlign = "center";
    g.textBaseline = "top";
    for (let i = 0; i < bins; i++) {
      const lab = i === this.cars ? "DNF" : `P${i + 1}`;
      g.fillStyle =
        i < 3 ? "#ffb021" : i < this.pointsTop ? "#9fb0c2" : i === this.cars ? "#ff3d4a" : "#4a5a6c";
      if (bins <= 12 || i % 2 === 0 || i === this.cars) g.fillText(lab, hx0 + i * gw + gw / 2, bottom + 6);
    }
    // median markers
    this.stats.forEach((st, s) => {
      if (st.n === 0) return;
      const col = STRAT_COLORS[s % STRAT_COLORS.length] ?? "#fff";
      const x = hx0 + (st.medPos - 0.5) * gw;
      g.fillStyle = col;
      // median: small notch on the axis
      g.fillRect(x - 1, bottom - 7, 2, 7);
      g.beginPath();
      g.moveTo(x, bottom - 7);
      g.lineTo(x - 3.5, bottom - 12);
      g.lineTo(x + 3.5, bottom - 12);
      g.closePath();
      g.fill();
    });

    if (funnelW) this.drawFunnel(W - funnelW - 6, top - 22, funnelW - 8, bottom - top + 22);
  }

  private drawFunnel(x0: number, y0: number, w: number, h: number): void {
    const g = this.s.ctx;
    g.strokeStyle = "#151d27";
    g.strokeRect(x0 + 0.5, y0 + 0.5, w, h);
    g.font = '600 11px "Barlow Condensed", sans-serif';
    g.textAlign = "left";
    g.textBaseline = "top";
    g.fillStyle = "#6c7c8e";
    const title =
      this.metric === "win" ? "P(VICTOIRE)" : this.metric === "podium" ? "P(PODIUM)" : "P(POINTS)";
    g.fillText(`${title} · IC 95 % vs N`, x0 + 8, y0 + 6);
    const px0 = x0 + 34;
    const px1 = x0 + w - 44;
    const py0 = y0 + 26;
    const py1 = y0 + h - 20;
    const lx = (n: number) =>
      px0 +
      ((Math.log10(Math.max(25, n)) - Math.log10(25)) /
        Math.max(0.3, Math.log10(this.total) - Math.log10(25))) *
        (px1 - px0);
    const ly = (p: number) => py1 - p * (py1 - py0);
    // axes
    g.font = '9px "JetBrains Mono", monospace';
    g.fillStyle = "#3a4858";
    g.textAlign = "right";
    g.textBaseline = "middle";
    for (const p of [0, 0.25, 0.5, 0.75, 1]) {
      g.strokeStyle = "#10171f";
      g.beginPath();
      g.moveTo(px0, Math.round(ly(p)) + 0.5);
      g.lineTo(px1, Math.round(ly(p)) + 0.5);
      g.stroke();
      g.fillText(`${p * 100}`, px0 - 4, ly(p));
    }
    g.textAlign = "center";
    g.textBaseline = "top";
    for (let e = 2; Math.pow(10, e) <= this.total * 1.01; e++) {
      const xx = lx(Math.pow(10, e));
      g.fillText(e === 2 ? "100" : e === 3 ? "1k" : e === 4 ? "10k" : "100k", xx, py1 + 4);
    }
    if (this.trail.length === 0) return;
    const pick = (st: StratStats) =>
      this.metric === "win" ? st.win : this.metric === "podium" ? st.podium : st.points;
    const ns = this.trail[this.trail.length - 1]?.stats.length ?? 0;
    for (let s = 0; s < ns; s++) {
      const col = STRAT_COLORS[s % STRAT_COLORS.length] ?? "#fff";
      // band
      g.beginPath();
      this.trail.forEach((tp, i) => {
        const st = tp.stats[s];
        if (!st) return;
        const xx = lx(tp.n);
        if (i === 0) g.moveTo(xx, ly(pick(st).hi));
        else g.lineTo(xx, ly(pick(st).hi));
      });
      for (let i = this.trail.length - 1; i >= 0; i--) {
        const tp = this.trail[i];
        const st = tp?.stats[s];
        if (!tp || !st) continue;
        g.lineTo(lx(tp.n), ly(pick(st).lo));
      }
      g.closePath();
      g.fillStyle = col;
      g.globalAlpha = 0.14;
      g.fill();
      g.globalAlpha = 1;
      // estimate
      g.strokeStyle = col;
      g.lineWidth = 1.5;
      g.beginPath();
      this.trail.forEach((tp, i) => {
        const st = tp.stats[s];
        if (!st) return;
        if (i === 0) g.moveTo(lx(tp.n), ly(pick(st).p));
        else g.lineTo(lx(tp.n), ly(pick(st).p));
      });
      g.stroke();
      const lastTp = this.trail[this.trail.length - 1];
      const lastSt = lastTp?.stats[s];
      if (lastTp && lastSt) {
        const iv = pick(lastSt);
        const xx = lx(lastTp.n);
        g.fillStyle = col;
        g.beginPath();
        g.arc(xx, ly(iv.p), 2.5, 0, Math.PI * 2);
        g.fill();
        g.font = '10px "JetBrains Mono", monospace';
        g.textAlign = "left";
        g.textBaseline = "middle";
        g.fillText((iv.p * 100).toFixed(1), xx + 5, ly(iv.p) + (s - (ns - 1) / 2) * 0);
        g.fillStyle = "#6c7c8e";
        g.font = '8px "JetBrains Mono", monospace';
        g.fillText(`±${(((iv.hi - iv.lo) / 2) * 100).toFixed(1)}`, xx + 5, ly(iv.p) + 10);
      }
    }
  }
}
