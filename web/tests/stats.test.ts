import { describe, expect, it } from "vitest";
import { spring, wilson } from "../src/util/stats";
import { fmtInt, fmtLap, fmtRaceTime } from "../src/util/format";

describe("stats & format", () => {
  it("wilson interval matches the server", () => {
    const w = wilson(50, 100);
    expect(w.lo).toBeCloseTo(0.4038, 3);
    expect(w.hi).toBeCloseTo(0.5962, 3);
    expect(wilson(0, 0)).toEqual({ p: 0, lo: 0, hi: 1 });
  });
  it("spring converges", () => {
    let x = 0,
      v = 0;
    for (let i = 0; i < 200; i++) [x, v] = spring(x, v, 1, 1 / 60);
    expect(x).toBeCloseTo(1, 3);
  });
  it("formats timing values", () => {
    expect(fmtLap(81.546)).toBe("1:21.546");
    expect(fmtLap(NaN)).toBe("—");
    expect(fmtRaceTime(3725.4)).toBe("1:02:05.4");
    expect(fmtInt(1234567)).toBe("1 234 567");
  });
});
