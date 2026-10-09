// Red-team attack tests on the frontend: decoders, playback, strategy
// helpers and the WebSocket client. Every test asserts the correct
// behaviour; a failing test is a finding.
import { afterEach, describe, expect, it, vi } from "vitest";
import { Playback } from "../src/live/playback";
import { PitwallClient, type SocketLike } from "../src/net/client";
import { decodeInbound, type LapView, type Track } from "../src/net/protocol";
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
import { lap, squareTrack } from "./fixtures";

const env = (type: string, data: unknown, extra: Record<string, unknown> = {}) =>
  JSON.stringify({ v: 1, type, id: "x", data, ...extra });

const limits = { maxSims: 50000, maxStrategies: 4, maxStops: 5, minSpeed: 1, maxSpeed: 240, maxCars: 20 };

function scenarioData(over: Record<string, unknown> = {}): Record<string, unknown> {
  const tr = squareTrack();
  const drivers = [0, 1].map((i) => ({
    name: `D${i}`,
    code: `D0${i}`,
    team: "T",
    teamIdx: 0,
    number: i + 2,
    paceS: 0,
    sigmaS: 0.2,
    mistakeP: 0.01,
    tyreSave: 1,
    defending: 1,
  }));
  return {
    seed: "X",
    cars: 2,
    laps: 3,
    pointsTop: 1,
    track: tr,
    drivers,
    teams: ["T"],
    grid: [0, 1],
    player: 0,
    suggested: { name: "P", start: "M", stops: [{ lap: 1, compound: "H" }] },
    tyres: [{ code: "M", name: "MEDIUM", paceS: 0, wearPerLap: 0.02, cliffLap: 30, curve: [0, 0.1] }],
    limits,
    ...over,
  };
}

let seed = 987654321;
const rnd = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31;

// ---------------------------------------------------------------------------
describe("redteam: decodeInbound", () => {
  it("accepts the reference scenario (sanity)", () => {
    expect(decodeInbound(env("scenario", scenarioData()))?.type).toBe("scenario");
  });

  it("rejects huge arrays quickly and without throwing", () => {
    const cars = Array.from({ length: 200_000 }, () => 1);
    const t0 = performance.now();
    expect(decodeInbound(env("race.lap", { lap: 1, laps: 2, flag: "green", cars, events: [] }))).toBeNull();
    expect(performance.now() - t0).toBeLessThan(2000);
  });

  it("survives deeply nested JSON", () => {
    for (const depth of [1_000, 100_000, 1_000_000]) {
      const deep = "[".repeat(depth) + "]".repeat(depth);
      expect(() => decodeInbound(env("pong", null).replace("null", deep))).not.toThrow();
      expect(() => decodeInbound(`{"v":1,"type":"race.lap","data":${deep}}`)).not.toThrow();
      expect(() => decodeInbound("[".repeat(depth))).not.toThrow();
    }
  });

  it("does not let __proto__ keys pollute prototypes or forge messages", () => {
    const forged = [
      '{"__proto__":{"v":1,"type":"error","data":{"code":"x","message":"y"}}}',
      '{"v":1,"type":"error","data":{"__proto__":{"code":"x","message":"y"}}}',
      '{"v":1,"type":"race.state","data":{"__proto__":{"status":"running","speed":1,"lap":1,"laps":2,"playhead":0,"autoPaused":false}}}',
      '{"v":1,"type":"__proto__","data":{}}',
      '{"v":1,"type":"constructor","data":{}}',
      '{"v":1,"type":"hasOwnProperty","data":{}}',
      '{"v":1,"type":"valueOf","data":{}}',
    ];
    for (const f of forged) expect(decodeInbound(f)).toBeNull();
    decodeInbound('{"v":1,"type":"error","data":{"code":"x","message":"y","__proto__":{"polluted":true}}}');
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  it("rejects numbers as strings, NaN / Infinity via 1e999, and non-integers for ints", () => {
    const st = { status: "running", speed: 30, lap: 3, laps: 50, playhead: 12, autoPaused: false };
    const bad = [
      env("race.state", { ...st, speed: "30" }),
      env("race.state", { ...st, lap: "3" }),
      env("race.state", { ...st, lap: 3.5 }),
      env("race.state", { ...st, autoPaused: 0 }),
      env("race.state", { ...st, status: "RUNNING" }),
      env("race.state", { ...st, speed: "NaN" }),
      env("race.state", { ...st, speed: null }),
      env("race.state", st).replace('"speed":30', '"speed":1e999'),
      env("race.state", st).replace('"speed":30', '"speed":-1e999'),
      env("race.state", st).replace('"lap":3', '"lap":1e999'),
    ];
    for (const b of bad) expect(decodeInbound(b)).toBeNull();
  });

  // The scenario size drives allocations (Playback creates one array per car,
  // the convergence chart one bin per position): an absurd or incoherent
  // grid size must be refused at the decoding boundary.
  it("rejects scenarios with an absurd or incoherent grid size", () => {
    for (const over of [
      { cars: 1e9 },
      { cars: -1 },
      { cars: 0 },
      { cars: 3 }, // 2 drivers
      { player: 99 },
      { player: -1 },
      { grid: [0, 0] },
      { grid: [0, 5] },
    ]) {
      expect.soft(decodeInbound(env("scenario", scenarioData(over))), JSON.stringify(over)).toBeNull();
    }
  });

  it("rejects race.started / race.sync with absurd grid sizes", () => {
    const strat = { name: "P", start: "M", stops: [{ lap: 1, compound: "H" }] };
    expect
      .soft(decodeInbound(env("race.started", { seed: "X", cars: 1e9, laps: 3, speed: 30, strategy: strat })))
      .toBeNull();
    expect
      .soft(decodeInbound(env("race.started", { seed: "X", cars: 2, laps: -3, speed: 30, strategy: strat })))
      .toBeNull();
  });

  it("never throws on random structural mutations of every message type", () => {
    const st = { status: "running", speed: 30, lap: 3, laps: 50, playhead: 12, autoPaused: false };
    const bases: [string, unknown][] = [
      ["scenario", scenarioData()],
      [
        "race.lap",
        lap(1, 3, [
          { car: 0, time: 80 },
          { car: 1, time: 81, pitted: true },
        ]),
      ],
      ["race.state", st],
      [
        "race.sync",
        { seed: "X", cars: 2, strategy: { name: "A", start: "M", stops: [] }, state: st, laps: [] },
      ],
      ["welcome", { session: "s", protocol: 1, resumed: true, limits }],
      ["error", { code: "c", message: "m", field: "f" }],
      ["sim.started", { run: 1, sims: 10, strategies: 1 }],
    ];
    const weird: unknown[] = [
      null,
      undefined,
      0,
      -0,
      1e308,
      -1e308,
      2 ** 53 + 1,
      "",
      "1",
      [],
      {},
      true,
      "__proto__",
      [[[]]],
      { __proto__: null },
    ];
    const mutate = (v: unknown, depth: number): unknown => {
      if (depth > 6) return v;
      if (rnd() < 0.08) return weird[Math.floor(rnd() * weird.length)];
      if (Array.isArray(v)) {
        const a = v.map((x) => mutate(x, depth + 1));
        if (rnd() < 0.1) a.reverse();
        if (rnd() < 0.05) a.push(...a);
        return a;
      }
      if (v && typeof v === "object") {
        const o: Record<string, unknown> = {};
        for (const [k, x] of Object.entries(v)) {
          if (rnd() < 0.05) continue;
          o[rnd() < 0.02 ? "__proto__" : k] = mutate(x, depth + 1);
        }
        return o;
      }
      return v;
    };
    for (let i = 0; i < 4000; i++) {
      const [type, data] = bases[i % bases.length] ?? ["pong", {}];
      const raw = env(type, mutate(data, 0));
      expect(() => decodeInbound(raw)).not.toThrow();
    }
  });
});

// ---------------------------------------------------------------------------
describe("redteam: Playback with hostile lap feeds", () => {
  const finitePose = (pb: Playback, cars: number[], times: number[]) => {
    for (const c of cars)
      for (const T of times) {
        const p = pb.pose(c, T);
        expect(
          Number.isFinite(p.x) && Number.isFinite(p.y) && Number.isFinite(p.lap),
          `car ${c} T ${T}`,
        ).toBe(true);
      }
  };
  const Ts = [-1e9, -1, 0, 0.5, 40, 80, 81, 159, 160, 1e6, 1e300, Number.MAX_VALUE];

  it("out-of-order, missing and duplicated laps", () => {
    const pb = new Playback(squareTrack(), 3, [0, 1, 2]);
    expect(() => {
      pb.add(lap(3, 5, [{ car: 0, time: 240 }]));
      pb.add(lap(1, 5, [{ car: 0, time: 80 }])); // car 1 and 2 missing
      pb.add(lap(1, 5, [{ car: 0, time: 80 }]));
      pb.add(lap(0, 5, [{ car: 0, time: 0 }]));
      pb.add(lap(-7, 5, [{ car: 0, time: 0 }]));
      pb.add(
        lap(2, 5, [
          { car: 2, time: 170 },
          { car: 1, time: 165 },
        ]),
      );
      pb.add(lap(1e9, 5, []));
    }).not.toThrow();
    finitePose(pb, [0, 1, 2, -1, 3, 1e9], Ts);
    expect(() => pb.lapAt(Number.NaN)).not.toThrow();
    expect(Number.isFinite(pb.available)).toBe(true);
  });

  it("negative, decreasing and zero times; duplicate and unknown car ids", () => {
    const pb = new Playback(squareTrack(), 2, [0, 1]);
    expect(() => {
      pb.add(
        lap(1, 4, [
          { car: 0, time: -50 },
          { car: 1, time: 0 },
          { car: 0, time: 10 },
          { car: 99, time: 5 },
          { car: -3, time: 5 },
        ]),
      );
      pb.add(
        lap(2, 4, [
          { car: 0, time: -100 },
          { car: 1, time: 0, pitted: true },
        ]),
      );
      pb.add(
        lap(3, 4, [
          { car: 0, time: 1e308 },
          { car: 1, time: 1e308, pitted: true },
        ]),
      );
      pb.add(
        lap(4, 4, [
          { car: 0, time: 1e308, out: true },
          { car: 1, time: -1e308 },
        ]),
      );
    }).not.toThrow();
    finitePose(pb, [0, 1], Ts);
    expect(Number.isFinite(pb.available)).toBe(true);
  });

  it("pit events with absurd stationary times", () => {
    const pb = new Playback(squareTrack(), 1, [0]);
    const lv: LapView = lap(1, 3, [{ car: 0, time: 90, pitted: true }]);
    lv.events = [
      { lap: 1, kind: "pit", car: 0, other: -1, value: 1e9, compound: "H" },
      { lap: 1, kind: "pit", car: 0, other: -1, value: -1e9, compound: "H" },
    ];
    pb.add(lv);
    finitePose(pb, [0], [0, 10, 45, 60, 89.9, 90, 100]);
  });

  it("degenerate tracks (empty arrays, no pit lane)", () => {
    const tr: Track = { ...squareTrack(), points: [], distFrac: [], timeFrac: [], pitLane: [], speedKmh: [] };
    const pb = new Playback(tr, 2, []);
    pb.add(
      lap(1, 2, [
        { car: 0, time: 80, pitted: true },
        { car: 1, time: 81 },
      ]),
    );
    pb.add(
      lap(2, 2, [
        { car: 0, time: 160 },
        { car: 1, time: 0, out: true },
      ]),
    );
    finitePose(pb, [0, 1], Ts);
    const one: Track = {
      ...squareTrack(),
      points: [{ x: 0.3, y: 0.3 }],
      distFrac: [0],
      timeFrac: [0],
      pitLane: [{ x: 0, y: 0 }],
    };
    const pb2 = new Playback(one, 1, [0]);
    pb2.add(lap(1, 2, [{ car: 0, time: 80, pitted: true }]));
    finitePose(pb2, [0], Ts);
  });

  it("zero cars and NaN / Infinity times never throw", () => {
    const pb = new Playback(squareTrack(), 0, []);
    expect(() => {
      pb.add(lap(1, 2, [{ car: 0, time: 80 }]));
      for (const T of [Number.NaN, Infinity, -Infinity]) pb.pose(0, T);
    }).not.toThrow();
    const pb2 = new Playback(squareTrack(), 1, [0]);
    pb2.add(lap(1, 3, [{ car: 0, time: Number.NaN }]));
    expect(() => {
      for (const T of [Number.NaN, 0, 50, Infinity]) pb2.pose(0, T);
      pb2.lapAt(50);
    }).not.toThrow();
  });
});

// ---------------------------------------------------------------------------
describe("redteam: strategy helpers", () => {
  it("parseCompact on hostile text never throws and only returns integer laps", () => {
    const inputs = [
      "",
      "-",
      "M-".repeat(5000) + "H",
      "M-1-H".repeat(2000),
      "x".repeat(1_000_000),
      "M-1٣-H",
      "M-１-H",
      "ſ-10-h",
      "M-007-H",
      "M-999-H",
      "M-1e1-H",
      "M-0x1-H",
      "M- 1-H",
      "\u0000M-1-H",
      "M-1-H‮",
      "🏁-1-H",
      "M-1-H-2-S-3-M-4-H-5-S",
    ];
    for (const s of inputs) {
      const r = parseCompact(s, "n"); // a throw fails the test
      if (typeof r !== "string") {
        expect(r.stops.length).toBeLessThanOrEqual(5);
        for (const st of r.stops) expect(Number.isInteger(st.lap)).toBe(true);
        expect(parseCompact(toCompact(r), "n")).toEqual(r);
      }
    }
  });

  it("validate refuses impossible inputs", () => {
    const s = { name: "", start: "M" as const, stops: [{ lap: 5, compound: "H" as const }] };
    for (const laps of [0, -10, 1, 5, Number.NaN, Infinity]) {
      expect.soft(validate(s, laps), `laps=${laps}`).not.toBeNull();
    }
    for (const lapV of [Number.NaN, Infinity, -0.5, 2.5, -1]) {
      expect
        .soft(validate({ ...s, stops: [{ lap: lapV, compound: "H" }] }, 50), `lap=${lapV}`)
        .not.toBeNull();
    }
  });

  it("moveStop always yields an integer lap inside the race", () => {
    const s = {
      name: "",
      start: "M" as const,
      stops: [
        { lap: 10, compound: "H" as const },
        { lap: 20, compound: "S" as const },
      ],
    };
    for (const target of [Number.NaN, Infinity, -Infinity, -5, 0, 1e300, 15.7]) {
      const r = moveStop(s, 0, target, 50);
      const l = r.stops[0]?.lap ?? -1;
      expect.soft(Number.isInteger(l) && l >= 1 && l < 20, `target ${target} -> ${l}`).toBe(true);
    }
    expect(moveStop(s, 9, 3, 50)).toBe(s);
    expect(moveStop(s, -1, 3, 50)).toBe(s);
  });

  it("add/remove stops and stints with absurd race lengths never throw", () => {
    let s: ReturnType<typeof addStop> = { name: "", start: "M", stops: [] };
    for (const laps of [-5, 0, 1, 2, 3, 1e6]) {
      expect(() => {
        for (let i = 0; i < 8; i++) s = addStop(s, laps);
        stints(s, laps);
        cliffRisk(s, laps, () => 0);
        cliffRisk(s, laps, () => Number.NaN);
        for (let i = 0; i < 8; i++) s = removeStop(s);
      }).not.toThrow();
      expect(s.stops.length).toBe(0);
    }
  });

  it("cliffRisk is not 'low' when the cliff data is unusable", () => {
    const s = { name: "", start: "M" as const, stops: [{ lap: 25, compound: "H" as const }] };
    expect(cliffRisk(s, 50, () => Number.NaN).level).not.toBe("low");
  });
});

// ---------------------------------------------------------------------------
class EvilSocket implements SocketLike {
  static all: EvilSocket[] = [];
  readyState = 0;
  onopen: ((ev: Event) => unknown) | null = null;
  onclose: ((ev: CloseEvent) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  onmessage: ((ev: MessageEvent) => unknown) | null = null;
  constructor(
    private readonly throwOnSend = false,
    private readonly throwOnClose = false,
  ) {
    EvilSocket.all.push(this);
  }
  send(): void {
    if (this.throwOnSend) throw new Error("send failed");
  }
  close(): void {
    if (this.throwOnClose) throw new Error("close failed");
    this.readyState = 3;
    this.onclose?.({} as CloseEvent);
  }
  open(): void {
    this.readyState = 1;
    this.onopen?.({} as Event);
  }
  recv(data: unknown): void {
    this.onmessage?.({ data } as MessageEvent);
  }
}

const welcome = (session: string) => env("welcome", { session, protocol: 1, resumed: false, limits });
const noop = { onMessage: () => undefined, onStatus: () => undefined, onReady: () => undefined };

describe("redteam: PitwallClient with hostile sockets and storage", () => {
  afterEach(() => {
    EvilSocket.all = [];
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("a factory that throws schedules a retry instead of throwing", () => {
    vi.useFakeTimers();
    let calls = 0;
    const c = new PitwallClient(
      "ws://x",
      noop,
      () => {
        calls++;
        throw new Error("blocked");
      },
      null,
    );
    expect(() => {
      c.connect();
    }).not.toThrow();
    vi.advanceTimersByTime(60_000);
    expect(calls).toBeGreaterThan(3);
    c.close();
    const before = calls;
    vi.advanceTimersByTime(60_000);
    expect(calls).toBe(before);
  });

  it("a socket that throws on send: hello / ping never escape", () => {
    vi.useFakeTimers();
    const c = new PitwallClient("ws://x", noop, () => new EvilSocket(true), null);
    c.connect();
    const s = EvilSocket.all[0];
    if (!s) throw new Error("no socket");
    expect(() => {
      s.open();
    }).not.toThrow();
    expect(() => {
      s.recv(welcome("a"));
    }).not.toThrow();
    expect(() => vi.advanceTimersByTime(20_000)).not.toThrow();
    expect(c.send({ type: "ping", data: {} })).toBeNull();
    c.close();
  });

  it("close() does not throw when the socket's close throws", () => {
    const c = new PitwallClient("ws://x", noop, () => new EvilSocket(false, true), null);
    c.connect();
    expect(() => {
      c.close();
    }).not.toThrow();
  });

  it("a storage that throws on setItem (quota) does not break the welcome", () => {
    vi.useFakeTimers();
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const ready: boolean[] = [];
    const c = new PitwallClient(
      "ws://x",
      { ...noop, onReady: (r) => ready.push(r) },
      () => new EvilSocket(),
      {
        getItem: () => null,
        setItem: () => {
          throw new DOMException("quota", "QuotaExceededError");
        },
      },
    );
    c.connect();
    const s = EvilSocket.all[0];
    if (!s) throw new Error("no socket");
    s.open();
    expect(() => {
      s.recv(welcome("a".repeat(100_000)));
    }).not.toThrow();
    expect(ready).toEqual([false]);
    c.close();
  });

  it("a storage that throws on getItem does not break the constructor", () => {
    expect(
      () =>
        new PitwallClient("ws://x", noop, () => new EvilSocket(), {
          getItem: () => {
            throw new Error("SecurityError");
          },
          setItem: () => undefined,
        }),
    ).not.toThrow();
  });

  it("a throwing onStatus handler is contained like onMessage / onReady", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const c = new PitwallClient(
      "ws://x",
      {
        ...noop,
        onStatus: (st) => {
          if (st === "open") throw new Error("ui bug");
        },
      },
      () => new EvilSocket(),
      null,
    );
    c.connect();
    const s = EvilSocket.all[0];
    if (!s) throw new Error("no socket");
    s.open();
    expect(() => {
      s.recv(welcome("a"));
    }).not.toThrow();
    c.close();
  });

  it("a late welcome on a closed client does not restart timers", () => {
    vi.useFakeTimers();
    const c = new PitwallClient("ws://x", noop, () => new EvilSocket(), null);
    c.connect();
    const s = EvilSocket.all[0];
    if (!s) throw new Error("no socket");
    s.open();
    c.close();
    s.recv(welcome("late"));
    expect(vi.getTimerCount()).toBe(0);
  });

  it("reconnect storm: backoff stays bounded and close() stops it", () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.999);
    const c = new PitwallClient("ws://x", noop, () => new EvilSocket(), null);
    c.connect();
    for (let i = 0; i < 40; i++) {
      const s = EvilSocket.all[EvilSocket.all.length - 1];
      if (!s) throw new Error("no socket");
      s.close(); // closes before open, every time
      vi.advanceTimersByTime(6000); // backoff is capped at 5 s
    }
    expect(EvilSocket.all.length).toBe(41);
    c.close();
    vi.advanceTimersByTime(60_000);
    expect(EvilSocket.all.length).toBe(41);
  });

  it("garbage frames of every kind are dropped without throwing", () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const got: string[] = [];
    const c = new PitwallClient(
      "ws://x",
      { ...noop, onMessage: (m) => got.push(m.type) },
      () => new EvilSocket(),
      null,
    );
    c.connect();
    const s = EvilSocket.all[0];
    if (!s) throw new Error("no socket");
    s.open();
    const frames: unknown[] = [
      undefined,
      null,
      42,
      {},
      new Blob(["x"]),
      new Uint8Array(3),
      "",
      "\ud800",
      "[".repeat(200_000),
      env("pong", {}, { id: { toString: 1 } }),
      env("pong", {}, { id: "__proto__" }),
      env("welcome", { session: 5 }),
      env("error", { code: "x", message: "y" }, { v: "1" }),
    ];
    for (const f of frames)
      expect(() => {
        s.recv(f);
      }).not.toThrow();
    expect(got).toEqual(["pong", "pong"]);
    c.close();
  });
});
