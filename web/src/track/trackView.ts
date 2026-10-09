// Top-down circuit renderer. A static layer (asphalt, kerbs, zones, sectors,
// pit lane) is cached in an offscreen canvas; cars and trails are drawn
// every frame on top.
import type { Driver, Track } from "../net/protocol";
import { Surface, TEAM_COLORS } from "../charts/surface";
import { pointAt, timeToDist, wrap01, type XY } from "./geometry";

export interface CarDraw extends XY {
  car: number;
  inPit: boolean;
  out: boolean;
  pos: number; // 1-based classification
}

const TRAIL = 9;

export class TrackView {
  private s: Surface;
  private track: Track | null = null;
  private stat: HTMLCanvasElement | null = null;
  private scale = 1;
  private ox = 0;
  private oy = 0;
  private trails = new Map<number, XY[]>();
  mode: "lab" | "live" = "lab";
  drivers: Driver[] = [];
  player = -1;

  constructor(parent: HTMLElement) {
    this.s = new Surface(parent, () => {
      this.rebuild();
    });
  }

  setTrack(t: Track | null): void {
    this.track = t;
    this.trails.clear();
    this.rebuild();
  }

  setMode(m: "lab" | "live"): void {
    if (m === this.mode) return;
    this.mode = m;
    this.trails.clear();
    this.rebuild();
  }

  resetTrails(): void {
    this.trails.clear();
  }

  private map(p: XY): XY {
    return { x: this.ox + p.x * this.scale, y: this.oy + p.y * this.scale };
  }

  private rebuild(): void {
    const t = this.track;
    const { w, h, dpr } = this.s;
    if (!t || w < 10 || h < 10) {
      this.stat = null;
      return;
    }
    let minX = Infinity,
      minY = Infinity,
      maxX = -Infinity,
      maxY = -Infinity;
    for (const p of [...t.points, ...t.pitLane]) {
      minX = Math.min(minX, p.x);
      maxX = Math.max(maxX, p.x);
      minY = Math.min(minY, p.y);
      maxY = Math.max(maxY, p.y);
    }
    const m = 34;
    this.scale = Math.min(
      (w - 2 * m) / Math.max(1e-6, maxX - minX),
      (h - 2 * m) / Math.max(1e-6, maxY - minY),
    );
    this.ox = (w - (maxX - minX) * this.scale) / 2 - minX * this.scale;
    this.oy = (h - (maxY - minY) * this.scale) / 2 - minY * this.scale;

    const c = document.createElement("canvas");
    c.width = Math.round(w * dpr);
    c.height = Math.round(h * dpr);
    const g = c.getContext("2d");
    if (!g) return;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);

    // dot grid
    g.fillStyle = "rgba(108,124,142,0.13)";
    for (let x = 12; x < w; x += 24) for (let y = 12; y < h; y += 24) g.fillRect(x, y, 1, 1);

    const path = new Path2D();
    t.points.forEach((p, i) => {
      const q = this.map(p);
      if (i === 0) path.moveTo(q.x, q.y);
      else path.lineTo(q.x, q.y);
    });
    path.closePath();
    const width = Math.max(7, Math.min(14, this.scale * 0.022));

    g.lineJoin = "round";
    g.lineCap = "round";
    // glow + verge + asphalt
    g.strokeStyle = "rgba(63,224,255,0.05)";
    g.lineWidth = width + 18;
    g.stroke(path);
    g.strokeStyle = "#1c2733";
    g.lineWidth = width + 3;
    g.stroke(path);
    g.strokeStyle = "#0d1218";
    g.lineWidth = width;
    g.stroke(path);

    // pit lane
    if (t.pitLane.length > 1) {
      g.save();
      g.setLineDash([3, 3]);
      g.strokeStyle = "rgba(63,224,255,0.45)";
      g.lineWidth = 1.5;
      g.beginPath();
      t.pitLane.forEach((p, i) => {
        const q = this.map(p);
        if (i === 0) g.moveTo(q.x, q.y);
        else g.lineTo(q.x, q.y);
      });
      g.stroke();
      g.restore();
      const mid = t.pitLane[Math.floor(t.pitLane.length / 2)];
      if (mid) {
        const q = this.map(mid);
        label(g, "PIT", q.x, q.y + 12, "rgba(63,224,255,0.7)", 9);
      }
    }

    // overtaking zones: bright inner band
    t.zones.forEach((z, zi) => {
      g.save();
      g.strokeStyle = "rgba(63,224,255,0.75)";
      g.lineWidth = 2;
      g.setLineDash([6, 4]);
      g.beginPath();
      const n = 40;
      const span = wrap01(z.to - z.from);
      for (let i = 0; i <= n; i++) {
        const q = this.map(pointAt(t, z.from + (span * i) / n));
        if (i === 0) g.moveTo(q.x, q.y);
        else g.lineTo(q.x, q.y);
      }
      g.stroke();
      g.restore();
      const mid = this.map(pointAt(t, z.from + span * 0.5));
      const off = this.normalOut(z.from + span * 0.5, 16);
      label(g, `Z${zi + 1}`, mid.x + off.x, mid.y + off.y, "#3fe0ff", 10);
    });

    // centre line: speed heat in the lab, discreet in live mode
    const n = t.points.length;
    for (let i = 0; i < n; i++) {
      const a = this.map(t.points[i] ?? { x: 0, y: 0 });
      const b = this.map(t.points[(i + 1) % n] ?? { x: 0, y: 0 });
      const v = t.speedKmh[i] ?? 200;
      g.strokeStyle = this.mode === "lab" ? speedColor(v) : "rgba(108,124,142,0.25)";
      g.lineWidth = this.mode === "lab" ? 2.2 : 1;
      g.beginPath();
      g.moveTo(a.x, a.y);
      g.lineTo(b.x, b.y);
      g.stroke();
    }

    // kerbs and corner labels
    for (const cn of t.corners) {
      for (let k = -3; k <= 3; k++) {
        const f = cn.at + k * 0.0018;
        const p = this.map(pointAt(t, f));
        const o = this.normalOut(f, width / 2 + 1.5);
        g.fillStyle = k % 2 === 0 ? "#ff3d4a" : "#e8eef4";
        g.fillRect(p.x + o.x - 1.2, p.y + o.y - 1.2, 2.4, 2.4);
      }
      const p = this.map(pointAt(t, cn.at));
      const o = this.normalOut(cn.at, width / 2 + 13);
      label(g, cn.label, p.x + o.x, p.y + o.y, "rgba(205,215,226,0.55)", 9);
    }

    // sectors
    t.sectors.forEach((sf, i) => {
      this.tick(g, sf, width, "#ffb021");
      const p = this.map(pointAt(t, sf));
      const o = this.normalOut(sf, -(width / 2 + 14));
      label(g, `S${i + 2}`, p.x + o.x, p.y + o.y, "#ffb021", 9);
    });
    // start / finish: chequered
    {
      const p = this.map(pointAt(t, 0));
      const o = this.normalOut(0, 1);
      const len = width / 2 + 3;
      for (let k = -3; k < 3; k++) {
        for (let r = 0; r < 2; r++) {
          g.fillStyle = (k + r) % 2 === 0 ? "#fff" : "#000";
          const tg = this.tangent(0);
          const x = p.x + (o.x * len * (k + 0.5)) / 3 + tg.x * (r - 1) * 2.4;
          const y = p.y + (o.y * len * (k + 0.5)) / 3 + tg.y * (r - 1) * 2.4;
          g.fillRect(x - 1.2, y - 1.2, 2.4, 2.4);
        }
      }
      const lo = this.normalOut(0, -(width / 2 + 16));
      label(g, "S/F", p.x + lo.x, p.y + lo.y, "#f2f6fa", 10);
    }
    this.stat = c;
  }

  private tangent(f: number): XY {
    const t = this.track;
    if (!t) return { x: 1, y: 0 };
    const a = pointAt(t, f - 0.001);
    const b = pointAt(t, f + 0.001);
    const l = Math.hypot(b.x - a.x, b.y - a.y) || 1;
    return { x: (b.x - a.x) / l, y: (b.y - a.y) / l };
  }

  /** Normal pointing away from the circuit's centroid, scaled to px. */
  private normalOut(f: number, px: number): XY {
    const t = this.track;
    if (!t) return { x: 0, y: 0 };
    const tg = this.tangent(f);
    let nx = -tg.y;
    let ny = tg.x;
    const p = pointAt(t, f);
    let cx = 0,
      cy = 0;
    for (const q of t.points) {
      cx += q.x;
      cy += q.y;
    }
    cx /= t.points.length || 1;
    cy /= t.points.length || 1;
    if (nx * (p.x - cx) + ny * (p.y - cy) < 0) {
      nx = -nx;
      ny = -ny;
    }
    return { x: nx * px, y: ny * px };
  }

  private tick(g: CanvasRenderingContext2D, f: number, width: number, color: string): void {
    const p = this.map(pointAt(this.track ?? ({} as Track), f));
    const o = this.normalOut(f, width / 2 + 4);
    g.strokeStyle = color;
    g.lineWidth = 2;
    g.beginPath();
    g.moveTo(p.x - o.x, p.y - o.y);
    g.lineTo(p.x + o.x, p.y + o.y);
    g.stroke();
  }

  /** Draws one frame. `now` (ms) drives the pulses; `cars` comes from the
   * playback (live mode) and is ignored in the lab, where a reference car
   * laps at the circuit's reference pace. */
  draw(now: number, cars: CarDraw[]): void {
    this.s.resize();
    const g = this.s.ctx;
    this.s.clear();
    const t = this.track;
    if (!t || !this.stat) return;
    g.drawImage(this.stat, 0, 0, this.s.w, this.s.h);

    if (this.mode === "lab") {
      // reference lap, 12x real time, follows the speed profile
      const lapMs = (t.baseLapS * 1000) / 12;
      const tf = (now % lapMs) / lapMs;
      const p = this.map(pointAt(t, timeToDist(t, tf)));
      this.drawTrail(-1, p, "rgba(242,246,250,0.6)");
      g.fillStyle = "#f2f6fa";
      g.beginPath();
      g.arc(p.x, p.y, 3.5, 0, Math.PI * 2);
      g.fill();
      label(g, "RÉF", p.x + 12, p.y - 10, "rgba(242,246,250,0.8)", 9);
      return;
    }

    const sorted = [...cars].sort((a, b) => b.pos - a.pos); // leader drawn last
    for (const c of sorted) {
      const p = this.map(c);
      const d = this.drivers[c.car];
      const col = c.out ? "#3a4858" : (TEAM_COLORS[(d?.teamIdx ?? 0) % TEAM_COLORS.length] ?? "#fff");
      if (!c.out) this.drawTrail(c.car, p, col);
      const isP = c.car === this.player;
      if (isP && !c.out) {
        const pulse = 0.5 + 0.5 * Math.sin(now / 260);
        g.strokeStyle = `rgba(255,176,33,${0.25 + 0.35 * pulse})`;
        g.lineWidth = 2;
        g.beginPath();
        g.arc(p.x, p.y, 10 + pulse * 3, 0, Math.PI * 2);
        g.stroke();
      }
      g.fillStyle = col;
      g.strokeStyle = isP ? "#ffb021" : "#000";
      g.lineWidth = isP ? 2 : 1.5;
      g.beginPath();
      g.arc(p.x, p.y, c.inPit ? 3.5 : 5, 0, Math.PI * 2);
      g.fill();
      g.stroke();
      const showLabel = isP || c.pos <= 3 || this.drivers.length <= 10;
      if (showLabel && d && !c.out) {
        label(g, d.code, p.x + 9, p.y - 9, isP ? "#ffb021" : "rgba(242,246,250,0.85)", isP ? 11 : 9, true);
      }
    }
  }

  private drawTrail(id: number, p: XY, color: string): void {
    const tr = this.trails.get(id) ?? [];
    const last = tr[tr.length - 1];
    if (last && Math.hypot(last.x - p.x, last.y - p.y) > 60) tr.length = 0; // teleport (pit exit, resync)
    tr.push(p);
    if (tr.length > TRAIL) tr.shift();
    this.trails.set(id, tr);
    const g = this.s.ctx;
    for (let i = 1; i < tr.length; i++) {
      const a = tr[i - 1];
      const b = tr[i];
      if (!a || !b) continue;
      g.strokeStyle = color;
      g.globalAlpha = (i / tr.length) * 0.5;
      g.lineWidth = 2.5 * (i / tr.length);
      g.beginPath();
      g.moveTo(a.x, a.y);
      g.lineTo(b.x, b.y);
      g.stroke();
    }
    g.globalAlpha = 1;
  }
}

function label(
  g: CanvasRenderingContext2D,
  s: string,
  x: number,
  y: number,
  color: string,
  size: number,
  bold = false,
): void {
  g.font = `${bold ? "600 " : ""}${size}px "Barlow Condensed", "Arial Narrow", sans-serif`;
  g.textAlign = "center";
  g.textBaseline = "middle";
  g.fillStyle = "rgba(4,6,10,0.75)";
  const w = g.measureText(s).width;
  g.fillRect(x - w / 2 - 2, y - size / 2 - 1, w + 4, size + 2);
  g.fillStyle = color;
  g.fillText(s, x, y + 0.5);
}

/** 80 km/h (deep red) → 330 km/h (cyan). */
export function speedColor(kmh: number): string {
  const u = Math.max(0, Math.min(1, (kmh - 80) / 250));
  const stops: [number, number, number][] = [
    [255, 61, 74],
    [255, 176, 33],
    [63, 224, 255],
  ];
  const seg = u < 0.5 ? 0 : 1;
  const v = u < 0.5 ? u * 2 : (u - 0.5) * 2;
  const a = stops[seg] ?? [0, 0, 0];
  const b = stops[seg + 1] ?? a;
  const c = a.map((x, i) => Math.round(x + ((b[i] ?? x) - x) * v));
  return `rgb(${c[0] ?? 0},${c[1] ?? 0},${c[2] ?? 0})`;
}
