// Strategy board: up to four plans, each editable three ways — compact
// notation (M-23-H), dragging the pit-stop handles on the stint bar, or
// clicking a stint to cycle its compound. The plan's tyre risk (longest
// stint vs. the compound's cliff) is shown live.
import type { Compound, Scenario, StrategyOut } from "../net/protocol";
import { STRAT_COLORS } from "../charts/surface";
import {
  addStop,
  cliffRisk,
  cycleCompound,
  MAX_STOPS,
  moveStop,
  parseCompact,
  removeStop,
  stints,
  toCompact,
  validate,
} from "../util/strategy";
import { h } from "./dom";

export interface BoardCallbacks {
  onChange: (i: number, s: StrategyOut) => void;
  onRemove: (i: number) => void;
  onFocus: (i: number) => void;
}

const RISK_LABEL = { low: "RISQUE FAIBLE", medium: "RISQUE MOYEN", high: "RISQUE ÉLEVÉ" };

export class StrategyEditor {
  readonly el: HTMLElement;
  private input: HTMLInputElement;
  private bar: HTMLElement;
  private scale: HTMLElement;
  private foot: HTMLElement;
  private err: HTMLElement;
  private strat: StrategyOut;

  constructor(
    private idx: number,
    s: StrategyOut,
    private scen: Scenario,
    private cb: BoardCallbacks,
    removable: boolean,
  ) {
    this.strat = s;
    const color = STRAT_COLORS[idx % STRAT_COLORS.length] ?? "#fff";
    this.input = h("input", {
      class: "strat-compact",
      value: toCompact(s),
      spellcheck: "false",
      "aria-label": `Stratégie ${letter(idx)}, notation compacte`,
      maxlength: 40,
    });
    this.input.addEventListener("input", () => {
      const r = parseCompact(this.input.value, this.strat.name);
      if (typeof r === "string") {
        this.showErr(r);
        return;
      }
      const v = validate(r, this.scen.laps);
      if (v) {
        this.showErr(v);
        return;
      }
      this.showErr("");
      this.strat = r;
      this.layout();
      this.cb.onChange(this.idx, r);
    });
    this.input.addEventListener("focus", () => {
      this.cb.onFocus(this.idx);
    });
    const rm = h("button", {
      class: "btn ghost",
      title: "retirer cette stratégie",
      "aria-label": "retirer",
      text: "✕",
    });
    rm.disabled = !removable;
    rm.addEventListener("click", () => {
      this.cb.onRemove(this.idx);
    });
    const plus = h("button", {
      class: "btn ghost",
      title: "ajouter un arrêt",
      "aria-label": "ajouter un arrêt",
      text: "+",
    });
    plus.addEventListener("click", () => {
      this.commit(addStop(this.strat, this.scen.laps));
    });
    const minus = h("button", {
      class: "btn ghost",
      title: "retirer le dernier arrêt",
      "aria-label": "retirer le dernier arrêt",
      text: "−",
    });
    minus.addEventListener("click", () => {
      const r = removeStop(this.strat);
      if (validate(r, this.scen.laps)) {
        // a 0-stop plan must still use two compounds: refuse with a hint
        this.showErr("règlement : deux gommes différentes minimum — impossible sans arrêt");
        return;
      }
      this.commit(r);
    });
    this.bar = h("div", { class: "stintbar", role: "group", "aria-label": "relais (glisser les arrêts)" });
    this.scale = h("div", { class: "lapscale" });
    this.foot = h("div", { class: "strat-foot" });
    this.err = h("div", { class: "err" });
    this.el = h(
      "div",
      { class: "strat" },
      h(
        "div",
        { class: "strat-head" },
        h("span", { class: "strat-id", text: letter(idx) }),
        this.input,
        plus,
        minus,
        rm,
      ),
      this.bar,
      this.scale,
      this.foot,
      this.err,
    );
    this.el.style.setProperty("--sc", color);
    this.el.addEventListener("pointerenter", () => {
      this.cb.onFocus(this.idx);
    });
    this.layout();
  }

  set(s: StrategyOut): void {
    this.strat = s;
    if (document.activeElement !== this.input) this.input.value = toCompact(s);
    this.layout();
  }

  private commit(s: StrategyOut): void {
    if (s === this.strat) return;
    this.strat = s;
    this.input.value = toCompact(s);
    this.showErr("");
    this.layout();
    this.cb.onChange(this.idx, s);
  }

  private showErr(m: string): void {
    this.err.textContent = m;
    this.input.classList.toggle("bad", m !== "");
  }

  private layout(): void {
    const laps = this.scen.laps;
    const pct = (lap: number) => `${(lap / laps) * 100}%`;
    const cliff = (c: Compound) => this.scen.tyres.find((t) => t.code === c)?.cliffLap ?? 99;
    this.bar.replaceChildren();
    stints(this.strat, laps).forEach((st, i) => {
      const len = st.to - st.from + 1;
      const el = h("div", {
        class: `stint ${st.compound}`,
        title: `relais ${i + 1} : ${len} tours en ${st.compound} — cliquer pour changer de gomme`,
        text: len >= 4 ? `${st.compound}·${len}` : "",
      });
      el.style.left = pct(st.from - 1);
      el.style.width = `calc(${pct(len)} - 1px)`;
      const over = len - cliff(st.compound);
      if (over > 0) {
        el.classList.add("over");
        el.style.setProperty("--over", `${Math.min(100, (over / len) * 100)}%`);
      }
      el.addEventListener("click", () => {
        const s = { ...this.strat, stops: this.strat.stops.map((x) => ({ ...x })) };
        if (i === 0) s.start = cycleCompound(s.start);
        else {
          const stop = s.stops[i - 1];
          if (stop) stop.compound = cycleCompound(stop.compound);
        }
        if (validate(s, laps)) {
          // cycle once more to keep two compounds
          if (i === 0) s.start = cycleCompound(s.start);
          else {
            const stop = s.stops[i - 1];
            if (stop) stop.compound = cycleCompound(stop.compound);
          }
        }
        if (!validate(s, laps)) this.commit(s);
      });
      this.bar.append(el);
    });
    this.strat.stops.forEach((stop, i) => {
      const hd = h(
        "div",
        {
          class: "stop-handle",
          role: "slider",
          tabindex: 0,
          "aria-label": `arrêt ${i + 1}`,
          "aria-valuenow": stop.lap,
          "aria-valuemin": 1,
          "aria-valuemax": laps - 1,
        },
        h("span", { text: `T${stop.lap}` }),
      );
      hd.style.left = pct(stop.lap);
      hd.addEventListener("pointerdown", (ev) => {
        ev.preventDefault();
        hd.setPointerCapture(ev.pointerId);
        hd.classList.add("drag");
        const rect = this.bar.getBoundingClientRect();
        let cur = this.strat;
        const move = (e: PointerEvent) => {
          const lap = ((e.clientX - rect.left) / Math.max(1, rect.width)) * laps;
          cur = moveStop(this.strat, i, lap, laps);
          const l = cur.stops[i]?.lap ?? stop.lap;
          hd.style.left = pct(l);
          const sp = hd.querySelector("span");
          if (sp) sp.textContent = `T${l}`;
        };
        const up = () => {
          hd.removeEventListener("pointermove", move);
          hd.removeEventListener("pointerup", up);
          hd.removeEventListener("pointercancel", up);
          hd.classList.remove("drag");
          this.commit(cur);
        };
        hd.addEventListener("pointermove", move);
        hd.addEventListener("pointerup", up);
        hd.addEventListener("pointercancel", up);
      });
      hd.addEventListener("keydown", (ev) => {
        const d = ev.key === "ArrowLeft" ? -1 : ev.key === "ArrowRight" ? 1 : 0;
        if (d) {
          ev.preventDefault();
          this.commit(moveStop(this.strat, i, stop.lap + d, laps));
          this.bar.querySelectorAll<HTMLElement>(".stop-handle")[i]?.focus();
        }
      });
      this.bar.append(hd);
    });
    this.scale.replaceChildren();
    const step = laps > 40 ? 10 : 5;
    for (let l = 0; l <= laps; l += step) {
      const sp = h("span", { text: String(l) });
      sp.style.left = pct(l);
      this.scale.append(sp);
    }
    const risk = cliffRisk(this.strat, laps, cliff);
    this.foot.replaceChildren(
      h("span", { text: `${this.strat.stops.length} ARRÊT${this.strat.stops.length > 1 ? "S" : ""}` }),
      h("span", { text: `MAX ${MAX_STOPS}` }),
      h("span", {
        class: `risk ${risk.level}`,
        title: `relais le plus long : ${Math.round(risk.ratio * 100)} % de la vie du pneu`,
        text: `${RISK_LABEL[risk.level]} · ${Math.round(risk.ratio * 100)}%`,
      }),
    );
  }
}

export const letter = (i: number): string => String.fromCharCode(65 + i);
