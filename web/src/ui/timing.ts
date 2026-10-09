// Timing tower: rows are absolutely positioned so that a change of
// position animates as a slide; a green / red wash marks places gained /
// lost on the last lap; purple lap time = overall fastest, green = personal.
import type { Driver, LapView } from "../net/protocol";
import { TEAM_COLORS } from "../charts/surface";
import { fmtLap } from "../util/format";
import { h, tyreDot } from "./dom";

const ROW_H = 26;

interface Row {
  el: HTMLElement;
  pos: HTMLElement;
  gap: HTMLElement;
  tyre: HTMLElement;
  age: HTMLElement;
  last: HTMLElement;
  prevPos: number;
}

export class TimingTower {
  private rows = new Map<number, Row>();
  private body: HTMLElement;
  private mode: "gap" | "int" = "gap";
  private lastLap: LapView | null = null;
  private drivers: Driver[] = [];
  private player = -1;
  private fastest = Infinity;

  constructor(parent: HTMLElement) {
    const gapHead = h("span", { title: "cliquer : écart au leader / intervalle", text: "GAP" });
    gapHead.style.cursor = "pointer";
    gapHead.addEventListener("click", () => {
      this.mode = this.mode === "gap" ? "int" : "gap";
      gapHead.textContent = this.mode === "gap" ? "GAP" : "INT";
      if (this.lastLap) this.update(this.lastLap);
    });
    const head = h(
      "div",
      { class: "tower-head" },
      h("span", { text: "P" }),
      h("span"),
      h("span", { text: "PILOTE" }),
      gapHead,
      h("span", { text: "GOM" }),
      h("span", { text: "ÂGE" }),
      h("span", { text: "DERNIER" }),
    );
    this.body = h("div", { class: "pb scroll" });
    this.body.style.position = "relative";
    parent.append(head, this.body);
  }

  setDrivers(drivers: Driver[], player: number, grid: number[]): void {
    this.drivers = drivers;
    this.player = player;
    this.rows.clear();
    this.body.replaceChildren();
    this.fastest = Infinity;
    this.lastLap = null;
    const inner = h("div");
    inner.style.height = `${drivers.length * ROW_H}px`;
    inner.style.position = "relative";
    drivers.forEach((d, c) => {
      const col = TEAM_COLORS[d.teamIdx % TEAM_COLORS.length] ?? "#fff";
      const stripe = h("span", { class: "stripe" });
      stripe.style.background = col;
      const pos = h("span", { class: "pos" });
      const gap = h("span", { class: "gap" });
      const tyre = h("span", { class: "tyre" });
      const age = h("span", { class: "age" });
      const last = h("span", { class: "last" });
      const el = h(
        "div",
        { class: `tower-row${c === player ? " player" : ""}`, title: `#${d.number} ${d.name} — ${d.team}` },
        pos,
        stripe,
        h("span", { class: "code", text: d.code }),
        gap,
        tyre,
        age,
        last,
      );
      inner.append(el);
      this.rows.set(c, { el, pos, gap, tyre, age, last, prevPos: 0 });
    });
    this.body.append(inner);
    this.showGrid(grid);
  }

  /** Before the start: the starting grid. */
  showGrid(grid: number[]): void {
    grid.forEach((c, p) => {
      const r = this.rows.get(c);
      if (!r) return;
      r.el.style.transform = `translateY(${p * ROW_H}px)`;
      r.pos.textContent = String(p + 1);
      r.gap.textContent = p === 0 ? "POLE" : "GRILLE";
      r.gap.style.color = "var(--dim)";
      r.tyre.replaceChildren();
      r.age.textContent = "";
      r.last.textContent = "";
      r.el.classList.remove("up", "down", "out");
      r.prevPos = p + 1;
    });
  }

  update(lap: LapView): void {
    if (lap === this.lastLap) return;
    const fresh = this.lastLap?.lap !== lap.lap;
    this.lastLap = lap;
    for (const cl of lap.cars) if (!cl.out && cl.best > 0) this.fastest = Math.min(this.fastest, cl.best);
    for (const cl of lap.cars) {
      const r = this.rows.get(cl.car);
      if (!r) continue;
      r.el.style.transform = `translateY(${(cl.pos - 1) * ROW_H}px)`;
      r.pos.textContent = String(cl.pos);
      r.el.classList.toggle("out", cl.out);
      if (fresh) {
        r.el.classList.toggle("up", r.prevPos > 0 && cl.pos < r.prevPos);
        r.el.classList.toggle("down", r.prevPos > 0 && cl.pos > r.prevPos);
        r.prevPos = cl.pos;
      }
      r.gap.style.color = "";
      if (cl.out) {
        r.gap.textContent = "OUT";
        r.gap.style.color = "var(--red)";
      } else if (cl.pitted) {
        r.gap.textContent = "PIT";
        r.gap.style.color = "var(--cyan)";
      } else if (cl.pos === 1) {
        r.gap.textContent = this.mode === "gap" ? "LEADER" : "—";
      } else {
        const v = this.mode === "gap" ? cl.gap : cl.interval;
        const leader = lap.cars[0];
        const lapsDown = this.mode === "gap" && leader ? Math.floor(v / Math.max(1, leader.lapTime)) : 0;
        r.gap.textContent = lapsDown >= 1 ? `+${lapsDown} T` : `+${v.toFixed(v < 10 ? 3 : 1)}`;
      }
      r.tyre.replaceChildren(tyreDot(cl.compound));
      r.age.textContent = String(cl.age);
      r.last.textContent = cl.out ? "" : fmtLap(cl.lapTime);
      r.last.classList.toggle("fastest", !cl.out && Math.abs(cl.lapTime - this.fastest) < 1e-6);
      r.last.classList.toggle(
        "pb",
        !cl.out && lap.lap > 2 && Math.abs(cl.lapTime - cl.best) < 1e-6 && cl.lapTime !== this.fastest,
      );
    }
  }

  get playerDriver(): Driver | undefined {
    return this.drivers[this.player];
  }
}
