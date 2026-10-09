type Attrs = Record<string, string | number | boolean | undefined>;

/** Creates an element. Text children are always inserted as text nodes
 * (never as HTML), so server strings cannot inject markup. */
export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Attrs = {},
  ...children: (Node | string | null | undefined | false)[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === false) continue;
    if (k === "class") el.className = String(v);
    else if (k === "text") el.textContent = String(v);
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    el.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return el;
}

export function panel(
  idx: string,
  title: string,
  aside?: Node | string,
): { root: HTMLElement; body: HTMLElement; aside: HTMLElement } {
  const asideEl = h("span", { class: "aside" }, aside ?? "");
  const body = h("div", { class: "pb" });
  const root = h(
    "section",
    { class: "panel", "aria-label": title },
    h("header", { class: "ph" }, h("span", { class: "idx", text: idx }), h("h2", { text: title }), asideEl),
    body,
  );
  return { root, body, aside: asideEl };
}

export function tyreDot(c: string): HTMLElement {
  return h("span", {
    class: `tyre-dot ${c}`,
    title: c === "S" ? "tendre" : c === "M" ? "medium" : "dure",
    text: c,
  });
}
