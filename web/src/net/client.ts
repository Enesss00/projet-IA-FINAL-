// WebSocket client: reconnection with capped exponential backoff, session
// resume (the server replays the live race with race.sync), latency probe.
// Inbound frames are decoded defensively: anything unexpected is dropped.
import { decodeInbound, encode, type Inbound, type Outbound } from "./protocol";

export type ConnStatus = "connecting" | "open" | "closed";

export interface ClientHandlers {
  onMessage: (m: Inbound) => void;
  onStatus: (s: ConnStatus, info: { attempt: number; latencyMs: number | null }) => void;
  /** Called after every successful (re)connection, once `welcome` arrived. */
  onReady: (resumed: boolean) => void;
}

export interface SocketLike {
  readyState: number;
  onopen: ((ev: Event) => unknown) | null;
  onclose: ((ev: CloseEvent) => unknown) | null;
  onerror: ((ev: Event) => unknown) | null;
  onmessage: ((ev: MessageEvent) => unknown) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

const SESSION_KEY = "pitwall.session";

export class PitwallClient {
  private ws: SocketLike | null = null;
  private attempt = 0;
  private seq = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private pingSent = new Map<string, number>();
  private stopped = false;
  latencyMs: number | null = null;
  session = "";

  constructor(
    private readonly url: string,
    private readonly h: ClientHandlers,
    private readonly factory: (url: string) => SocketLike = (u) => new WebSocket(u),
    private readonly storage: Pick<Storage, "getItem" | "setItem"> | null = safeSessionStorage(),
  ) {
    this.session = this.storage?.getItem(SESSION_KEY) ?? "";
  }

  connect(): void {
    this.stopped = false;
    this.open();
  }

  close(): void {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.ws?.close(1000, "bye");
    this.ws = null;
  }

  get isOpen(): boolean {
    return this.ws?.readyState === 1;
  }

  /** Sends a message; returns its id, or null when offline (callers re-send
   * what matters from onReady). */
  send(msg: Outbound): string | null {
    if (!this.ws || this.ws.readyState !== 1) return null;
    const id = `c${++this.seq}`;
    try {
      this.ws.send(encode(msg, id));
    } catch {
      return null;
    }
    return id;
  }

  private open(): void {
    this.h.onStatus("connecting", { attempt: this.attempt, latencyMs: null });
    let ws: SocketLike;
    try {
      ws = this.factory(this.url);
    } catch {
      this.schedule();
      return;
    }
    this.ws = ws;
    ws.onopen = () => {
      this.send({ type: "hello", data: { session: this.session, client: "pitwall-web/1" } });
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data !== "string") return;
      let m: Inbound | null = null;
      try {
        m = decodeInbound(ev.data);
      } catch {
        m = null;
      }
      if (!m) {
        console.warn("pitwall: ignored unexpected server message");
        return;
      }
      if (m.type === "welcome") {
        this.session = m.data.session;
        this.storage?.setItem(SESSION_KEY, this.session);
        this.attempt = 0;
        this.h.onStatus("open", { attempt: 0, latencyMs: this.latencyMs });
        this.startPing();
        safe(() => {
          this.h.onReady(m.data.resumed);
        });
      }
      if (m.type === "pong") {
        const t0 = this.pingSent.get(m.id);
        if (t0 !== undefined) {
          this.latencyMs = Math.round(performance.now() - t0);
          this.pingSent.delete(m.id);
          this.h.onStatus("open", { attempt: 0, latencyMs: this.latencyMs });
        }
      }
      safe(() => {
        this.h.onMessage(m);
      });
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.pingTimer) clearInterval(this.pingTimer);
      this.h.onStatus("closed", { attempt: this.attempt, latencyMs: null });
      if (!this.stopped) this.schedule();
    };
    ws.onerror = () => {
      /* onclose follows */
    };
  }

  private schedule(): void {
    if (this.stopped) return;
    const base = Math.min(5000, 250 * 2 ** this.attempt);
    const delay = base / 2 + Math.random() * (base / 2); // jitter: no thundering herd
    this.attempt++;
    this.timer = setTimeout(() => {
      this.open();
    }, delay);
  }

  private startPing(): void {
    if (this.pingTimer) clearInterval(this.pingTimer);
    const ping = () => {
      const id = this.send({ type: "ping", data: {} });
      if (id) this.pingSent.set(id, performance.now());
      if (this.pingSent.size > 20) this.pingSent.clear();
    };
    ping();
    this.pingTimer = setInterval(ping, 5000);
  }
}

function safe(fn: () => void): void {
  try {
    fn();
  } catch (e) {
    console.error("pitwall: handler error", e);
  }
}

function safeSessionStorage(): Storage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    return null;
  }
}

export function defaultWsUrl(): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}/ws`;
}
