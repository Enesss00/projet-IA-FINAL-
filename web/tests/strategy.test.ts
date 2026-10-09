import { describe, expect, it } from "vitest";
import {
  addStop,
  cliffRisk,
  moveStop,
  parseCompact,
  removeStop,
  stints,
  toCompact,
  validate,
} from "../src/util/strategy";

describe("strategy helpers", () => {
  it("round-trips the compact notation", () => {
    const s = parseCompact("s-14-m-35-h", "A");
    expect(typeof s).toBe("object");
    if (typeof s === "string") return;
    expect(toCompact(s)).toBe("S-14-M-35-H");
  });

  it("rejects absurd input with a message", () => {
    for (const bad of ["", "X", "M-", "M-a-H", "M-12", "M-12-Q", "M-1000-H", "M-1-H-2-S-3-M-4-H-5-S-6-M"]) {
      expect(typeof parseCompact(bad, "A")).toBe("string");
    }
  });

  it("validates like the server", () => {
    expect(validate({ name: "A", start: "M", stops: [{ lap: 20, compound: "H" }] }, 50)).toBeNull();
    expect(validate({ name: "A", start: "M", stops: [] }, 50)).toMatch(/deux gommes/);
    expect(validate({ name: "A", start: "M", stops: [{ lap: 0, compound: "H" }] }, 50)).not.toBeNull();
    expect(validate({ name: "A", start: "M", stops: [{ lap: 50, compound: "H" }] }, 50)).not.toBeNull();
    expect(
      validate(
        {
          name: "A",
          start: "M",
          stops: [
            { lap: 20, compound: "H" },
            { lap: 20, compound: "S" },
          ],
        },
        50,
      ),
    ).not.toBeNull();
    expect(validate({ name: "A", start: "M", stops: [{ lap: 2.5, compound: "H" }] }, 50)).not.toBeNull();
  });

  it("keeps stops ordered when dragging", () => {
    const s = {
      name: "A",
      start: "S" as const,
      stops: [
        { lap: 10, compound: "M" as const },
        { lap: 30, compound: "H" as const },
      ],
    };
    expect(moveStop(s, 0, 45, 50).stops[0]?.lap).toBe(29);
    expect(moveStop(s, 1, -5, 50).stops[1]?.lap).toBe(11);
    expect(moveStop(s, 1, 999, 50).stops[1]?.lap).toBe(49);
  });

  it("computes stints and cliff risk", () => {
    const s = { name: "A", start: "S" as const, stops: [{ lap: 25, compound: "H" as const }] };
    expect(stints(s, 50)).toEqual([
      { compound: "S", from: 1, to: 25 },
      { compound: "H", from: 26, to: 50 },
    ]);
    const r = cliffRisk(s, 50, (c) => (c === "S" ? 15 : 35));
    expect(r.level).toBe("high");
    expect(r.worst?.compound).toBe("S");
  });

  it("adds and removes stops within limits", () => {
    let s = { name: "A", start: "M" as const, stops: [{ lap: 25, compound: "H" as const }] } as Parameters<
      typeof addStop
    >[0];
    for (let i = 0; i < 10; i++) s = addStop(s, 50);
    expect(s.stops.length).toBe(5);
    expect(validate(s, 50)).toBeNull();
    expect(removeStop(s).stops.length).toBe(4);
  });
});
