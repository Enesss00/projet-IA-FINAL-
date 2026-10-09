import { afterEach, describe, expect, it, vi } from "vitest";
import { PitwallClient, type SocketLike } from "../src/net/client";

class FakeSocket implements SocketLike {
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: ((ev: Event) => unknown) | null = null;
  onclose: ((ev: CloseEvent) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  onmessage: ((ev: MessageEvent) => unknown) | null = null;
  constructor() {
    FakeSocket.all.push(this);
  }
  send(d: string) {
    this.sent.push(d);
  }
  close() {
    this.readyState = 3;
    this.onclose?.({} as CloseEvent);
  }
  open() {
    this.readyState = 1;
    this.onopen?.({} as Event);
  }
  recv(data: unknown) {
    this.onmessage?.({ data } as MessageEvent);
  }
}

const welcome = (session: string, resumed = false) =>
  JSON.stringify({
    v: 1,
    type: "welcome",
    data: {
      session,
      protocol: 1,
      resumed,
      limits: { maxSims: 1, maxStrategies: 4, maxStops: 5, minSpeed: 1, maxSpeed: 240, maxCars: 20 },
    },
  });

afterEach(() => {
  FakeSocket.all = [];
  vi.useRealTimers();
});

describe("PitwallClient", () => {
  it("says hello, resumes its session after a cut, and never throws", () => {
    vi.useFakeTimers();
    const store = new Map<string, string>();
    const ready: boolean[] = [];
    const msgs: string[] = [];
    const c = new PitwallClient(
      "ws://x",
      {
        onMessage: (m) => {
          msgs.push(m.type);
          if (m.type === "error") throw new Error("handler bug");
        },
        onStatus: () => undefined,
        onReady: (r) => ready.push(r),
      },
      () => new FakeSocket(),
      { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) },
    );
    c.connect();
    const s1 = FakeSocket.all[0];
    if (!s1) throw new Error("no socket");
    s1.open();
    expect((JSON.parse(s1.sent[0] ?? "{}") as { type: string }).type).toBe("hello");
    s1.recv(welcome("abc"));
    expect(ready).toEqual([false]);
    // garbage and handler exceptions are contained
    s1.recv("not json");
    s1.recv(new ArrayBuffer(4));
    s1.recv(JSON.stringify({ v: 1, type: "error", data: { code: "x", message: "y" } }));
    expect(msgs).toContain("error");
    // connection drops: reconnect with the same session id
    s1.close();
    vi.advanceTimersByTime(10_000);
    const s2 = FakeSocket.all[1];
    if (!s2) throw new Error("no reconnect");
    s2.open();
    expect((JSON.parse(s2.sent[0] ?? "{}") as { data: { session: string } }).data.session).toBe("abc");
    s2.recv(welcome("abc", true));
    expect(ready).toEqual([false, true]);
    c.close();
  });

  it("returns null when sending offline", () => {
    const c = new PitwallClient(
      "ws://x",
      { onMessage: () => undefined, onStatus: () => undefined, onReady: () => undefined },
      () => new FakeSocket(),
      null,
    );
    expect(c.send({ type: "sim.cancel", data: {} })).toBeNull();
  });
});
