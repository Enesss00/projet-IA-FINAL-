// HiDPI canvas bound to its container size.
export class Surface {
  readonly canvas: HTMLCanvasElement;
  readonly ctx: CanvasRenderingContext2D;
  w = 0;
  h = 0;
  dpr = 1;
  private ro: ResizeObserver | null = null;

  constructor(
    parent: HTMLElement,
    private readonly onResize: () => void = () => undefined,
  ) {
    this.canvas = document.createElement("canvas");
    this.canvas.className = "fill";
    parent.append(this.canvas);
    const ctx = this.canvas.getContext("2d");
    if (!ctx) throw new Error("canvas 2D unavailable");
    this.ctx = ctx;
    if (typeof ResizeObserver !== "undefined") {
      this.ro = new ResizeObserver(() => {
        this.resize();
      });
      // observer callbacks are async: the owner is fully built by then
      this.ro.observe(parent);
    }
    this.resize(false); // no callback: the owner is still being constructed
  }

  resize(notify = true): void {
    const r = this.canvas.getBoundingClientRect();
    this.dpr = Math.min(2, window.devicePixelRatio || 1);
    const w = Math.max(1, Math.round(r.width));
    const h = Math.max(1, Math.round(r.height));
    if (w === this.w && h === this.h) return;
    this.w = w;
    this.h = h;
    this.canvas.width = Math.round(w * this.dpr);
    this.canvas.height = Math.round(h * this.dpr);
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    if (notify) this.onResize();
  }

  clear(): void {
    this.ctx.setTransform(1, 0, 0, 1, 0, 0);
    this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
  }
}

/** Reads a CSS custom property (theme tokens live in styles.css). */
export function cssVar(name: string, fallback: string): string {
  if (typeof getComputedStyle === "undefined") return fallback;
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

export const STRAT_COLORS = ["#3fe0ff", "#ffb021", "#c58bff", "#5dffb0"];
export const TYRE_COLORS: Record<string, string> = { S: "#ff4d5e", M: "#ffd23f", H: "#eef3f8" };

/** Ten distinct, dark-background-friendly team hues (fictional teams). */
export const TEAM_COLORS = [
  "#3fe0ff",
  "#ff5a36",
  "#7dff6b",
  "#ff3dbb",
  "#f2f2f2",
  "#8f7bff",
  "#ffd23f",
  "#2bd4a4",
  "#ff8a3d",
  "#5b8cff",
];
