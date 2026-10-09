// Strategy comparison table with confidence intervals, and a plain-language
// verdict that uses the paired comparison (same simulated universes).
import type { SimProgress } from "../net/protocol";
import { STRAT_COLORS } from "../charts/surface";
import { fmtPct, fmtSigned } from "../util/format";
import { h } from "./dom";
import { letter } from "./strategyBoard";

export function renderCompare(parent: HTMLElement, p: SimProgress | null): void {
  parent.replaceChildren();
  if (!p || p.strategies.length === 0 || p.done === 0) {
    parent.append(h("div", { class: "empty", text: "Lancez une simulation pour comparer les stratégies" }));
    return;
  }
  const best = p.strategies.reduce((b, s, i, a) => ((a[b]?.meanPos.p ?? Infinity) <= s.meanPos.p ? b : i), 0);
  const bestTime = Math.min(...p.strategies.map((s) => (s.timeMed.p > 0 ? s.timeMed.p : Infinity)));
  const pm = (lo: number, hi: number) =>
    h("span", { class: "pm", text: `±${(((hi - lo) / 2) * 100).toFixed(1)}` });
  const table = h(
    "table",
    { class: "cmp" },
    h(
      "thead",
      {},
      h(
        "tr",
        {},
        ...["PLAN", "VICT.", "PODIUM", `TOP ${p.pointsTop}`, "E[POS]", "P95", "CVaR5", "Δ T̃"].map((t) =>
          h("th", { text: t }),
        ),
      ),
    ),
  );
  const tb = h("tbody");
  p.strategies.forEach((s, i) => {
    const tag = h("span", { class: "tag", text: letter(i) });
    tag.style.setProperty("--sc", STRAT_COLORS[i % STRAT_COLORS.length] ?? "#fff");
    tb.append(
      h(
        "tr",
        { class: i === best ? "best" : "" },
        h("td", {}, tag, s.plan),
        h("td", {}, fmtPct(s.win.p), pm(s.win.lo, s.win.hi)),
        h("td", {}, fmtPct(s.podium.p), pm(s.podium.lo, s.podium.hi)),
        h("td", {}, fmtPct(s.points.p), pm(s.points.lo, s.points.hi)),
        h("td", { text: s.meanPos.p.toFixed(2) }),
        h("td", { text: s.p95Pos.toFixed(0) }),
        h("td", { text: s.cvarPos.toFixed(1) }),
        h("td", {
          text:
            s.timeMed.p > 0
              ? s.timeMed.p - bestTime < 0.05
                ? "réf."
                : fmtSigned(s.timeMed.p - bestTime, 1) + "s"
              : "—",
        }),
      ),
    );
  });
  table.append(tb);
  parent.append(table);

  const b = p.strategies[best];
  if (b && p.strategies.length > 1) {
    const others = p.strategies
      .map((s, i) => ({ s, i }))
      .filter((x) => x.i !== best)
      .map(
        (x) =>
          `${letter(x.i)} dans ${fmtPct(b.ahead[x.i] ?? 0, 0)} des univers (derrière dans ${fmtPct(x.s.ahead[best] ?? 0, 0)})`,
      );
    const v = h(
      "div",
      { class: "verdict" },
      h("b", { text: `Recommandation : ${letter(best)} (${b.plan})` }),
      ` — meilleure position moyenne ${b.meanPos.p.toFixed(2)} [${b.meanPos.lo.toFixed(2)} ; ${b.meanPos.hi.toFixed(2)}]. Devant ${others.join(", ")}. Pire cas observé : P${b.worstPos > p.strategies.length + 30 ? "—" : b.worstPos}.`,
    );
    parent.append(v);
  }
  if (p.truncated)
    parent.append(
      h("div", {
        class: "err",
        text: `Résultat partiel (${p.reason || "interrompu"}) sur ${p.done} courses.`,
      }),
    );
}
