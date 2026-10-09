import { describe, expect, it } from "vitest";
import { decodeInbound, encode } from "../src/net/protocol";

const env = (type: string, data: unknown) => JSON.stringify({ v: 1, type, id: "x", data });

describe("decodeInbound", () => {
  it("decodes a valid error and welcome", () => {
    const e = decodeInbound(env("error", { code: "invalid", message: "nope", field: "sims" }));
    expect(e?.type).toBe("error");
    const w = decodeInbound(
      env("welcome", {
        session: "s",
        protocol: 1,
        resumed: false,
        limits: { maxSims: 1, maxStrategies: 4, maxStops: 5, minSpeed: 1, maxSpeed: 240, maxCars: 20 },
      }),
    );
    expect(w?.type).toBe("welcome");
  });

  it("rejects malformed frames without throwing", () => {
    const bad = [
      "",
      "null",
      "[]",
      "{",
      '{"v":2,"type":"error","data":{}}',
      env("error", { code: 1, message: "x" }),
      env("unknown.type", {}),
      env("race.lap", { lap: "1" }),
      env("race.lap", { lap: 1, laps: 2, flag: "blue", cars: [], events: [] }),
      env("sim.progress", { run: 1, done: NaN }),
      env("__proto__", {}),
      env("constructor", {}),
      env("toString", {}),
    ];
    for (const b of bad) expect(decodeInbound(b)).toBeNull();
  });

  it("survives random garbage and random mutations (fuzz)", () => {
    let seed = 12345;
    const rnd = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31;
    const base = env("race.state", {
      status: "running",
      speed: 30,
      lap: 3,
      laps: 50,
      playhead: 12,
      autoPaused: false,
    });
    for (let i = 0; i < 5000; i++) {
      const chars = base.split("");
      for (let k = 0; k < 3; k++)
        chars[Math.floor(rnd() * chars.length)] = String.fromCharCode(Math.floor(rnd() * 128));
      expect(() => decodeInbound(chars.join(""))).not.toThrow();
      const junk = Array.from({ length: Math.floor(rnd() * 64) }, () =>
        String.fromCharCode(Math.floor(rnd() * 65535)),
      ).join("");
      expect(() => decodeInbound(junk)).not.toThrow();
    }
  });

  it("ignores extra fields (forward compatible)", () => {
    const m = decodeInbound(env("error", { code: "x", message: "y", future: [1, 2, 3] }));
    expect(m?.type).toBe("error");
  });

  it("encodes versioned envelopes", () => {
    const s = encode({ type: "sim.cancel", data: {} }, "c1");
    expect(JSON.parse(s)).toEqual({ v: 1, type: "sim.cancel", id: "c1", data: {} });
  });
});
