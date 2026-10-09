// Tyre cliff chart: lap-time loss vs tyre age for each compound on this
// circuit, the cliff where the curve breaks, and the planned stints of the
// selected strategy drawn on top (red where a stint runs past the cliff).
import type { TyreView } from "../net/protocol";
import type { StrategyOut } from "../net/protocol";
import { stints } from "../util/strategy";
import { STRAT_COLORS, Surface, TYRE_COLORS } from "./surface";

export class CliffChart {
  private s: Surface;
  private tyres: TyreView[] = [];
  private laps = 50;
  private strat: StrategyOut | null = null;
  private stratIdx = 0;
  private dirty = true;

  constructor(parent: HTMLElement) {
    this.s = new Surface(parent, () => {
      this.dirty = true;
    });
  }

  set(tyres: TyreView[], laps: number): void {
    this.tyres = tyres;
    this.laps = laps;
    this.dirty = true;
  }

  setStrategy(s: StrategyOut | null, idx: number): void {
    this.strat = s;
    this.stratIdx = idx;
    this.dirty = true;
  }

  draw(): void {
    this.s.resize();
    if (!this.dirty) return;
    this.dirty = false;
    this.s.clear();
    const g = this.s.ctx;
    const W = this.s.w;
    const H = this.s.h;
    if (this.tyres.length === 0) return;
    const x0 = 34;
    const x1 = W - 10;
    const y0 = 12;
    const y1 = H - 22;
    const maxAge = Math.min(this.laps, Math.max(...this.tyres.map((t) => t.curve.length - 1)));
    const lo = Math.min(...this.tyres.map((t) => t.curve[0] ?? 0)) - 0.2;
    const hi = Math.max(
      lo + 2,
      Math.min(7, Math.max(...this.tyres.map((t) => t.curve[Math.min(maxAge, t.curve.length - 1)] ?? 0))),
    );
    const X = (a: number) => x0 + (a / Math.max(1, maxAge)) * (x1 - x0);
    const Y = (d: number) => y1 - ((Math.min(hi, d) - lo) / (hi - lo)) * (y1 - y0);

    g.font = '9px "JetBrains Mono", monospace';
    g.fillStyle = "#3a4858";
    g.textAlign = "right";
    g.textBaseline = "middle";
    for (let d = Math.ceil(lo); d <= hi; d += 1) {
      g.strokeStyle = d === 0 ? "#2a3a4c" : "#10171f";
      g.beginPath();
      g.moveTo(x0, Math.round(Y(d)) + 0.5);
      g.lineTo(x1, Math.round(Y(d)) + 0.5);
      g.stroke();
      g.fillText(`${d > 0 ? "+" : ""}${d}s`, x0 - 4, Y(d));
    }
    g.textAlign = "center";
    g.textBaseline = "top";
    for (let a = 0; a <= maxAge; a += 10) g.fillText(String(a), X(a), y1 + 5);

    // compound curves with their cliff
    for (const t of this.tyres) {
      const col = TYRE_COLORS[t.code] ?? "#fff";
      const cl = t.cliffLap;
      if (cl < maxAge) {
        g.fillStyle = "rgba(255,61,74,0.035)";
        g.fillRect(X(cl), y0, x1 - X(cl), y1 - y0);
        g.save();
        g.setLineDash([2, 3]);
        g.strokeStyle = col;
        g.globalAlpha = 0.5;
        g.beginPath();
        g.moveTo(X(cl), y0);
        g.lineTo(X(cl), y1);
        g.stroke();
        g.restore();
        g.font = '600 9px "Barlow Condensed", sans-serif';
        g.fillStyle = col;
        g.textAlign = "left";
        g.fillText(`FALAISE ${t.code} · ${cl.toFixed(0)}`, X(cl) + 3, y0 + 2 + "SMH".indexOf(t.code) * 11);
      }
      g.strokeStyle = col;
      g.globalAlpha = 0.85;
      g.lineWidth = 1.5;
      g.beginPath();
      t.curve.forEach((d, a) => {
        if (a > maxAge) return;
        if (a === 0) g.moveTo(X(a), Y(d));
        else g.lineTo(X(a), Y(d));
      });
      g.stroke();
      g.globalAlpha = 1;
    }

    // planned stints of the selected strategy
    const s = this.strat;
    if (!s) return;
    const sc = STRAT_COLORS[this.stratIdx % STRAT_COLORS.length] ?? "#fff";
    for (const st of stints(s, this.laps)) {
      const t = this.tyres.find((x) => x.code === st.compound);
      if (!t) continue;
      const len = st.to - st.from + 1;
      g.lineWidth = 3;
      for (let a = 0; a < Math.min(len, t.curve.length - 1); a++) {
        const over = a + 1 > t.cliffLap;
        g.strokeStyle = over ? "#ff3d4a" : sc;
        g.beginPath();
        g.moveTo(X(a), Y(t.curve[a] ?? 0));
        g.lineTo(X(a + 1), Y(t.curve[a + 1] ?? 0));
        g.stroke();
      }
      const end = Math.min(len, t.curve.length - 1);
      g.fillStyle = len > t.cliffLap ? "#ff3d4a" : sc;
      g.beginPath();
      g.arc(X(end), Y(t.curve[end] ?? 0), 3.5, 0, Math.PI * 2);
      g.fill();
      g.font = '9px "JetBrains Mono", monospace';
      g.textAlign = "left";
      g.textBaseline = "bottom";
      g.fillText(`${st.compound}${len}`, X(end) + 5, Y(t.curve[end] ?? 0) - 2);
    }
  }
}
