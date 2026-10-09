// PIT WALL application: state, server wiring, layout and the frame loop.
import { PitwallClient, defaultWsUrl, type ConnStatus } from "./net/client";
import type { Inbound, LapView, Scenario, ServerError, SimProgress, StrategyOut } from "./net/protocol";
import { Store } from "./net/store";
import { ConvergenceChart, type Metric, type TrailPoint } from "./charts/convergence";
import { CliffChart } from "./charts/cliff";
import { TrackView, type CarDraw } from "./track/trackView";
import { Playback } from "./live/playback";
import { TimingTower } from "./ui/timing";
import { StrategyEditor, letter } from "./ui/strategyBoard";
import { renderCompare } from "./ui/compare";
import { radioForLap, type RadioMsg } from "./ui/radio";
import { h, panel } from "./ui/dom";
import { fmtInt, fmtLap, fmtRaceTime } from "./util/format";
import { validate } from "./util/strategy";

export interface AppState {
  conn: ConnStatus;
  latency: number | null;
  mode: "lab" | "live";
  seed: string;
  cars: number;
  scenario: Scenario | null;
  strategies: StrategyOut[];
  focus: number;
  sims: number;
  sim: {
    status: "idle" | "running" | "done";
    run: number;
    progress: SimProgress | null;
    trail: TrailPoint[];
    reqId: string | null;
  };
  race: {
    status: "idle" | "starting" | "running" | "paused" | "finished";
    speed: number;
    strategy: StrategyOut | null;
    epoch: number;
  };
  error: ServerError | null;
}

const SIM_STEPS = [200, 500, 1000, 2000, 5000, 10000, 20000, 50000];
const SEED_RE = /^[A-Za-z0-9_-]{1,32}$/;

export function readUrl(search: string): { seed: string; cars: number } {
  const q = new URLSearchParams(search);
  const seed = (q.get("seed") ?? "").trim();
  const carsN = Number(q.get("cars"));
  return {
    seed: SEED_RE.test(seed) ? seed.toUpperCase() : "NIGHT-42",
    cars: Number.isInteger(carsN) && carsN >= 2 && carsN <= 20 ? carsN : 10,
  };
}

export class App {
  readonly store: Store<AppState>;
  private client: PitwallClient;
  private playback: Playback | null = null;
  private playhead = 0;
  private lastFrame = 0;
  private radioQueue: RadioMsg[] = [];
  private radioShownLap = 0;
  private lastTowerLap: LapView | null = null;
  // components
  private track!: TrackView;
  private conv!: ConvergenceChart;
  private cliff!: CliffChart;
  private tower!: TimingTower;
  private editors: StrategyEditor[] = [];
  private els!: Record<string, HTMLElement>;

  constructor(private root: HTMLElement) {
    const { seed, cars } = readUrl(location.search);
    this.store = new Store<AppState>({
      conn: "connecting",
      latency: null,
      mode: "lab",
      seed,
      cars,
      scenario: null,
      strategies: [],
      focus: 0,
      sims: 5000,
      sim: { status: "idle", run: 0, progress: null, trail: [], reqId: null },
      race: { status: "idle", speed: 30, strategy: null, epoch: 0 },
      error: null,
    });
    this.client = new PitwallClient(defaultWsUrl(), {
      onMessage: (m) => {
        this.onMessage(m);
      },
      onStatus: (s, info) => {
        this.store.set({ conn: s, latency: info.latencyMs });
      },
      onReady: (resumed) => {
        this.onReady(resumed);
      },
    });
    this.build();
    this.store.subscribe((s, prev) => {
      this.render(s, prev);
    });
    this.render(this.store.get(), null);
  }

  start(): void {
    this.client.connect();
    const loop = (t: number) => {
      this.frame(t);
      requestAnimationFrame(loop);
    };
    requestAnimationFrame(loop);
    setInterval(() => {
      const c = this.els.clock;
      if (c) c.textContent = new Date().toLocaleTimeString("fr-FR", { hour12: false });
    }, 1000);
  }

  // ---------------------------------------------------------------- server

  private onReady(resumed: boolean): void {
    const s = this.store.get();
    this.client.send({ type: "scenario.get", data: { seed: s.seed, cars: s.cars } });
    // A Monte Carlo run does not survive a disconnection: relaunch it.
    if (s.sim.status === "running") this.runSim();
    // A live race survives (race.sync) only if the session was resumed.
    if (
      !resumed &&
      (s.race.status === "running" || s.race.status === "paused" || s.race.status === "starting")
    ) {
      this.store.set({ race: { ...s.race, status: "idle" } });
      this.toast({
        code: "resync",
        message: "Connexion rétablie : session expirée, la course en direct a été perdue.",
        field: "",
      });
    }
  }

  private onMessage(m: Inbound): void {
    const s = this.store.get();
    switch (m.type) {
      case "scenario": {
        if (m.data.seed.toUpperCase() !== s.seed.toUpperCase() || m.data.cars !== s.cars) return; // stale answer
        const keep =
          s.scenario?.seed === m.data.seed && s.scenario.cars === m.data.cars && s.strategies.length > 0;
        const strategies = keep ? s.strategies : defaultStrategies(m.data);
        this.store.set({ scenario: m.data, strategies });
        return;
      }
      case "sim.started":
        if (m.id === s.sim.reqId) this.store.set({ sim: { ...s.sim, run: m.data.run } });
        return;
      case "sim.progress":
      case "sim.done": {
        if (m.data.run !== s.sim.run || m.id !== s.sim.reqId) return;
        const trail =
          m.data.done > 0
            ? [...s.sim.trail.filter((t) => t.n < m.data.done), { n: m.data.done, stats: m.data.strategies }]
            : s.sim.trail;
        this.store.set({
          sim: { ...s.sim, progress: m.data, trail, status: m.type === "sim.done" ? "done" : "running" },
        });
        if (m.type === "sim.done" && !m.data.truncated) this.say(simVerdict(m.data));
        return;
      }
      case "sim.cancelled":
        return;
      case "race.started":
        this.newPlayback();
        this.store.set({
          race: { ...s.race, status: "running", speed: m.data.speed, strategy: m.data.strategy },
        });
        return;
      case "race.lap":
        if (!this.playback) this.newPlayback();
        if (this.playback && !this.playback.add(m.data)) {
          // gap in the feed: ask the server for the full race again
          this.client.send({
            type: "hello",
            data: { session: this.client.session, client: "pitwall-web/1" },
          });
        }
        return;
      case "race.state": {
        const st = m.data.status === "stopped" ? "idle" : m.data.status;
        this.store.set({ race: { ...s.race, status: st, speed: m.data.speed } });
        return;
      }
      case "race.sync": {
        this.newPlayback();
        for (const lv of m.data.laps) this.playback?.add(lv);
        const last = m.data.laps[m.data.laps.length - 1];
        // resume one lap behind the newest data, like before the cut
        this.playhead = this.playback ? this.playback.leaderEnd(Math.max(0, (last?.lap ?? 1) - 2)) : 0;
        this.radioShownLap = Math.max(0, (last?.lap ?? 1) - 2);
        const st = m.data.state.status === "stopped" ? "idle" : m.data.state.status;
        this.store.set({
          mode: "live",
          race: { ...s.race, status: st, speed: m.data.state.speed, strategy: m.data.strategy },
        });
        if (m.data.state.autoPaused) this.client.send({ type: "race.control", data: { action: "resume" } });
        return;
      }
      case "error":
        this.toast(m.data);
        if (m.id && m.id === s.sim.reqId) this.store.set({ sim: { ...s.sim, status: "idle" } });
        if (s.race.status === "starting") this.store.set({ race: { ...s.race, status: "idle" } });
        return;
      case "welcome":
      case "pong":
        return;
    }
  }

  private newPlayback(): void {
    const sc = this.store.get().scenario;
    if (!sc) return;
    const gridPos: number[] = [];
    sc.grid.forEach((c, p) => (gridPos[c] = p));
    this.playback = new Playback(sc.track, sc.cars, gridPos);
    this.playhead = 0;
    this.radioShownLap = 0;
    this.lastTowerLap = null;
    this.track.resetTrails();
    this.store.set((s) => ({ race: { ...s.race, epoch: s.race.epoch + 1 } }));
  }

  // ---------------------------------------------------------------- actions

  setSeed(seed: string, cars = this.store.get().cars): void {
    const clean = seed.trim().toUpperCase();
    if (!SEED_RE.test(clean)) {
      this.toast({
        code: "invalid",
        message: "Seed invalide : 1 à 32 caractères parmi A-Z, 0-9, '-' et '_'.",
        field: "seed",
      });
      return;
    }
    const s = this.store.get();
    if (s.sim.status === "running") this.client.send({ type: "sim.cancel", data: {} });
    if (s.race.status !== "idle") this.client.send({ type: "race.control", data: { action: "stop" } });
    this.playback = null;
    this.conv.reset();
    this.store.set({
      seed: clean,
      cars,
      scenario: null,
      strategies: [],
      mode: "lab",
      sim: { status: "idle", run: 0, progress: null, trail: [], reqId: null },
      race: { ...s.race, status: "idle", strategy: null },
    });
    const url = new URL(location.href);
    url.searchParams.set("seed", clean);
    if (cars !== 10) url.searchParams.set("cars", String(cars));
    else url.searchParams.delete("cars");
    history.replaceState(null, "", url);
    this.client.send({ type: "scenario.get", data: { seed: clean, cars } });
  }

  runSim(): void {
    const s = this.store.get();
    if (!s.scenario) return;
    const bad = s.strategies.map((x) => validate(x, s.scenario?.laps ?? 0)).find((x) => x);
    if (bad) {
      this.toast({ code: "invalid", message: bad, field: "" });
      return;
    }
    this.conv.reset();
    const id = this.client.send({
      type: "sim.start",
      data: { seed: s.seed, cars: s.cars, strategies: s.strategies, sims: s.sims, pace: "live" },
    });
    if (!id) {
      this.toast({
        code: "offline",
        message: "Hors ligne : la simulation partira à la reconnexion.",
        field: "",
      });
    }
    this.store.set({ sim: { status: "running", run: -1, progress: null, trail: [], reqId: id } });
  }

  cancelSim(): void {
    this.client.send({ type: "sim.cancel", data: {} });
    const s = this.store.get();
    this.store.set({ sim: { ...s.sim, status: "idle" } });
  }

  startRace(): void {
    const s = this.store.get();
    const strat = s.strategies[s.focus] ?? s.strategies[0];
    if (!s.scenario || !strat) return;
    const id = this.client.send({
      type: "race.start",
      data: { seed: s.seed, cars: s.cars, strategy: strat, speed: s.race.speed },
    });
    if (!id) {
      this.toast({ code: "offline", message: "Hors ligne : impossible de lancer la course.", field: "" });
      return;
    }
    this.store.set({ mode: "live", race: { ...s.race, status: "starting", strategy: strat } });
  }

  raceControl(action: "pause" | "resume" | "stop"): void {
    this.client.send({ type: "race.control", data: { action } });
  }

  setSpeed(v: number): void {
    const s = this.store.get();
    this.store.set({ race: { ...s.race, speed: v } });
    if (s.race.status !== "idle")
      this.client.send({ type: "race.control", data: { action: "speed", speed: v } });
  }

  private toast(e: ServerError): void {
    this.store.set({ error: e });
    const el = this.els.toast;
    if (!el) return;
    el.replaceChildren(
      h("span", { class: "code", text: e.code.toUpperCase() }),
      e.field ? `[${e.field}] ` : "",
      e.message,
    );
    el.classList.remove("hidden");
    clearTimeout(this.toastTimer);
    this.toastTimer = setTimeout(() => {
      el.classList.add("hidden");
    }, 6000);
  }
  private toastTimer: ReturnType<typeof setTimeout> | undefined;

  private say(text: string, alert = false, lap = 0): void {
    this.radioQueue.push({ text, alert, lap });
    this.flushRadio();
  }

  private flushRadio(): void {
    const m = this.radioQueue.shift();
    const el = this.els.radioMsg;
    const bar = this.els.radio;
    if (!m || !el || !bar) return;
    el.replaceChildren(m.lap ? h("em", { text: `T${m.lap}` }) : "", m.text);
    el.classList.toggle("alert", m.alert);
    bar.classList.remove("flash");
    requestAnimationFrame(() => {
      bar.classList.add("flash");
    });
  }

  // ---------------------------------------------------------------- layout

  private build(): void {
    const seedInput = h("input", {
      "aria-label": "seed",
      spellcheck: "false",
      maxlength: 32,
      value: this.store.get().seed,
    });
    seedInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") this.setSeed(seedInput.value);
    });
    const dice = h("button", { class: "btn ghost", title: "nouvelle course (seed aléatoire)", text: "⟳" });
    dice.addEventListener("click", () => {
      const seed = `${pickWord()}-${Math.floor(Math.random() * 900 + 100)}`;
      seedInput.value = seed;
      this.setSeed(seed);
    });
    const share = h("button", { class: "btn ghost", title: "copier le lien de cette course", text: "LIEN" });
    share.addEventListener("click", () => {
      void navigator.clipboard.writeText(location.href).then(
        () => {
          this.say("Lien copié : même seed, même course pour tout le monde.");
        },
        () => {
          this.say(location.href);
        },
      );
    });
    const tabLab = h(
      "button",
      { class: "tab", role: "tab", "aria-selected": "true" },
      h("span", { class: "n", text: "01" }),
      "Strategy Lab",
    );
    const tabLive = h(
      "button",
      { class: "tab", role: "tab", "aria-selected": "false" },
      h("span", { class: "n", text: "02" }),
      "Live Race",
    );
    const tabPost = h(
      "button",
      { class: "tab", role: "tab", "aria-selected": "false", title: "palier 3", disabled: true },
      h("span", { class: "n", text: "03" }),
      "Post-Race",
    );
    tabLab.addEventListener("click", () => {
      this.store.set({ mode: "lab" });
    });
    tabLive.addEventListener("click", () => {
      this.store.set({ mode: "live" });
    });
    const led = h("span", { class: "led connecting" });
    const linkTxt = h("span", { text: "CONNEXION…" });
    const clock = h("span", { class: "clock", text: "--:--:--" });
    const evName = h("div", { class: "name", text: "Chargement du circuit…" });
    const evMeta = h("div", { class: "meta" });

    const top = h(
      "header",
      { class: "topbar" },
      h("div", { class: "brand" }, h("i"), "PIT WALL", h("small", { text: "v1" })),
      h("div", { class: "event" }, evName, evMeta),
      h("div", { class: "seedbox" }, h("span", { class: "label", text: "Seed" }), seedInput, dice, share),
      h("nav", { class: "tabs", role: "tablist" }, tabLab, tabLive, tabPost),
      h("div", { class: "link" }, led, h("div", {}, linkTxt, h("br"), clock)),
    );

    // left: timing
    const pTiming = panel("01", "Chronométrage");
    pTiming.body.style.display = "flex";
    pTiming.body.style.flexDirection = "column";
    this.tower = new TimingTower(pTiming.body);

    // centre top: track
    const pTrack = panel("02", "Circuit");
    const trackArea = h("div", { class: "pb" });
    pTrack.body.style.display = "flex";
    pTrack.body.style.flexDirection = "column";
    const kv = h("div", { class: "kv" });
    const playBtn = h("button", { class: "btn", text: "❚❚" });
    playBtn.addEventListener("click", () => {
      const st = this.store.get().race.status;
      if (st === "running") this.raceControl("pause");
      else if (st === "paused") this.raceControl("resume");
      else this.startRace();
    });
    const speed = h("input", { type: "range", min: 0, max: 100, step: 1, "aria-label": "vitesse" });
    const speedTxt = h("span", { text: "×30" });
    speed.addEventListener("input", () => {
      const v = Math.round(
        Math.exp(Math.log(1) + (Number(speed.value) / 100) * (Math.log(240) - Math.log(1))),
      );
      speedTxt.textContent = `×${v}`;
      this.setSpeed(v);
    });
    const lapBig = h("div", { class: "lapbig" });
    const flag = h("span", { class: "flag green", text: "VERT" });
    const raceClock = h("span", { class: "clock" });
    const restart = h("button", { class: "btn", text: "Recommencer" });
    restart.addEventListener("click", () => {
      this.startRace();
    });
    const racectl = h(
      "div",
      { class: "racectl" },
      playBtn,
      lapBig,
      flag,
      raceClock,
      h("span", { class: "grow" }),
      h("span", { class: "speed" }, "VITESSE", speed, speedTxt),
      restart,
    );
    pTrack.body.append(trackArea, kv, racectl);
    this.track = new TrackView(trackArea);

    // centre bottom: convergence + comparison
    const metricSel = h("span", {});
    (["win", "podium", "points"] as Metric[]).forEach((m) => {
      const b = h("button", {
        class: "btn ghost",
        "data-m": m,
        text: m === "win" ? "VICT." : m === "podium" ? "PODIUM" : "POINTS",
      });
      b.addEventListener("click", () => {
        this.conv.metric = m;
        metricSel.querySelectorAll("button").forEach((x) => (x.style.color = x === b ? "var(--amber)" : ""));
      });
      if (m === "podium") b.style.color = "var(--amber)";
      metricSel.append(b);
    });
    const pConv = panel("03", "Convergence Monte Carlo", metricSel);
    pConv.body.style.display = "flex";
    pConv.body.style.flexDirection = "column";
    const convArea = h("div", { class: "pb" });
    const counterBig = h("div", { class: "big", text: "0" });
    const counterSub = h("div", { class: "sub" });
    convArea.append(h("div", { class: "counter" }, counterBig, counterSub));
    const cmp = h("div", { class: "pad" });
    cmp.style.flex = "none";
    cmp.style.padding = "4px 12px 10px";
    cmp.style.maxHeight = "42%";
    cmp.style.overflow = "auto";
    pConv.body.append(convArea, cmp);
    this.conv = new ConvergenceChart(convArea);

    // right: strategies + cliff
    const pStrat = panel("04", "Stratégies");
    const board = h("div", { class: "pb pad scroll" });
    const editorsBox = h("div");
    const addBtn = h("button", { class: "btn", text: "+ Stratégie" });
    addBtn.addEventListener("click", () => {
      const s = this.store.get();
      if (!s.scenario || s.strategies.length >= s.scenario.limits.maxStrategies) return;
      const base = s.strategies[s.strategies.length - 1] ?? s.scenario.suggested;
      this.store.set({
        strategies: [
          ...s.strategies,
          { ...base, name: letter(s.strategies.length), stops: base.stops.map((x) => ({ ...x })) },
        ],
      });
    });
    const engBtn = h("button", {
      class: "btn",
      title: "remettre A sur le plan de l'ingénieur",
      text: "Plan ingénieur",
    });
    engBtn.addEventListener("click", () => {
      const s = this.store.get();
      if (!s.scenario) return;
      const st = [...s.strategies];
      st[0] = { ...s.scenario.suggested, name: "A" };
      this.store.set({ strategies: st });
    });
    const simsRange = h("input", {
      type: "range",
      min: 0,
      max: SIM_STEPS.length - 1,
      step: 1,
      "aria-label": "nombre de simulations",
    });
    simsRange.value = String(SIM_STEPS.indexOf(5000));
    const simsTxt = h("span", { text: "5 000" });
    simsRange.addEventListener("input", () => {
      const v = SIM_STEPS[Number(simsRange.value)] ?? 5000;
      simsTxt.textContent = fmtInt(v);
      this.store.set({ sims: v });
    });
    const runBtn = h("button", { class: "btn primary", text: "Simuler" });
    runBtn.addEventListener("click", () => {
      if (this.store.get().sim.status === "running") this.cancelSim();
      else this.runSim();
    });
    const raceBtn = h("button", { class: "btn", text: "▶ Course" });
    raceBtn.addEventListener("click", () => {
      this.startRace();
    });
    board.append(
      editorsBox,
      h("div", { class: "row" }, addBtn, engBtn),
      h(
        "div",
        { class: "simctl" },
        h("span", { class: "label", text: "Courses / strat." }),
        simsRange,
        simsTxt,
      ),
      h("div", { class: "row", style: "margin-top:8px" }, runBtn, raceBtn),
    );
    pStrat.body.append(board);

    const pCliff = panel("05", "Falaise de pneus");
    const cliffArea = h("div", { class: "pb" });
    pCliff.body.style.display = "flex";
    pCliff.body.style.flexDirection = "column";
    pCliff.body.append(cliffArea);
    this.cliff = new CliffChart(cliffArea);

    const radioMsg = h("span", {
      class: "msg",
      text: "Radio prête. Choisissez vos stratégies et lancez la simulation.",
    });
    const radio = h("footer", { class: "radio" }, h("span", { class: "tagr", text: "RADIO" }), radioMsg);
    const toast = h("div", { class: "toast hidden", role: "alert" });

    const main = h(
      "main",
      { class: "main" },
      h("div", { class: "col left" }, pTiming.root),
      h("div", { class: "col center" }, pTrack.root, pConv.root),
      h(
        "div",
        { class: "col right", style: "grid-template-rows: minmax(0,1fr) 250px" },
        pStrat.root,
        pCliff.root,
      ),
    );
    this.root.replaceChildren(h("div", { class: "shell" }, top, main, radio), toast);
    this.els = {
      led,
      linkTxt,
      clock,
      evName,
      evMeta,
      seedInput,
      tabLab,
      tabLive,
      kv,
      racectl,
      playBtn,
      lapBig,
      flag,
      raceClock,
      speed,
      speedTxt,
      counterBig,
      counterSub,
      cmp,
      editorsBox,
      addBtn,
      runBtn,
      raceBtn,
      simsRange,
      simsTxt,
      radio,
      radioMsg,
      toast,
      trackTitle: pTrack.aside,
      stratAside: pStrat.aside,
    };
  }

  // ---------------------------------------------------------------- render

  private render(s: AppState, prevState: AppState | null): void {
    const prev: Partial<AppState> = prevState ?? {};
    const e = this.els;
    // connection
    e.led?.setAttribute("class", `led ${s.conn}`);
    if (e.linkTxt)
      e.linkTxt.textContent =
        s.conn === "open"
          ? `LIAISON ${s.latency ?? "…"} ms`
          : s.conn === "connecting"
            ? "CONNEXION…"
            : "HORS LIGNE";

    // mode
    e.tabLab?.setAttribute("aria-selected", String(s.mode === "lab"));
    e.tabLive?.setAttribute("aria-selected", String(s.mode === "live"));
    this.track.setMode(s.mode);
    e.kv?.classList.toggle("hidden", s.mode !== "lab");
    e.racectl?.classList.toggle("hidden", s.mode !== "live");

    // scenario
    if (s.scenario !== prev.scenario) {
      const sc = s.scenario;
      this.track.setTrack(sc?.track ?? null);
      if (sc) {
        this.track.drivers = sc.drivers;
        this.track.player = sc.player;
        this.tower.setDrivers(sc.drivers, sc.player, sc.grid);
        this.cliff.set(sc.tyres, sc.laps);
        this.conv.setGrid(sc.cars, sc.pointsTop);
        const t = sc.track;
        const d = sc.drivers[sc.player];
        if (e.evName) e.evName.textContent = `${t.name} — ${t.region}`;
        if (e.evMeta) {
          e.evMeta.replaceChildren(
            h("b", { text: `${t.laps} TOURS` }),
            ` · ${(t.lengthM / 1000).toFixed(3)} KM · `,
            h("b", { text: d ? `#${d.number} ${d.name}` : "" }),
            ` · ${d?.team ?? ""} · GRILLE P${sc.grid.indexOf(sc.player) + 1}/${sc.cars}`,
          );
        }
        if (e.kv) {
          const cell = (v: string, k: string) => h("div", {}, h("b", { text: v }), h("span", { text: k }));
          e.kv.replaceChildren(
            cell(fmtLap(t.baseLapS), "tour de réf."),
            cell(`${t.pitLossS.toFixed(1)} s`, "perte aux stands"),
            cell(`×${t.wearFactor.toFixed(2)}`, "usure pneus"),
            cell(`${t.zones.length} · ${t.overtakeEase.toFixed(2)}`, "zones · dépassement"),
            cell(`${t.corners.length}`, "virages"),
            cell(`${t.fuelPerLapKg.toFixed(2)} kg`, "carburant / tour"),
          );
        }
        if (e.seedInput instanceof HTMLInputElement) e.seedInput.value = s.seed;
      }
    }
    if (e.trackTitle)
      e.trackTitle.textContent =
        s.mode === "lab" ? "profil de vitesse · zones de dépassement" : "course en direct";

    // strategies
    if (s.strategies !== prev.strategies || s.scenario !== prev.scenario) this.renderEditors(s);
    if (s.focus !== prev.focus || s.strategies !== prev.strategies) {
      this.cliff.setStrategy(s.strategies[s.focus] ?? null, s.focus);
    }
    const max = s.scenario?.limits.maxStrategies ?? 4;
    if (e.addBtn instanceof HTMLButtonElement) e.addBtn.disabled = !s.scenario || s.strategies.length >= max;
    if (e.stratAside)
      e.stratAside.textContent = `${s.strategies.length}/${max} · course avec ${letter(s.focus)}`;
    if (e.runBtn) {
      e.runBtn.textContent = s.sim.status === "running" ? "■ Arrêter" : "Simuler";
      e.runBtn.className = s.sim.status === "running" ? "btn danger" : "btn primary";
      (e.runBtn as HTMLButtonElement).disabled = !s.scenario;
    }
    if (e.raceBtn instanceof HTMLButtonElement) {
      e.raceBtn.disabled = !s.scenario;
      e.raceBtn.textContent = `▶ Course avec ${letter(s.focus)}`;
    }

    // simulation
    if (s.sim !== prev.sim) {
      const p = s.sim.progress;
      if (p) this.conv.update(p.strategies, s.sim.trail, p.total);
      if (e.counterBig) e.counterBig.textContent = fmtInt(p?.races ?? 0);
      if (e.counterSub) {
        const rate = p && p.elapsedMs > 0 ? (p.races / p.elapsedMs) * 1000 : 0;
        e.counterSub.replaceChildren(
          s.sim.status === "running"
            ? "courses simulées · "
            : s.sim.status === "done"
              ? "courses simulées · terminé · "
              : "courses simulées",
          s.sim.status !== "idle" && p ? h("span", { class: "rate", text: `${fmtInt(rate)}/s` }) : "",
          p ? ` · ${fmtInt(p.done)}/${fmtInt(p.total)} univers` : "",
        );
      }
      if (e.cmp) renderCompare(e.cmp, p);
    }

    // race
    if (s.race !== prev.race && e.playBtn) {
      e.playBtn.textContent = s.race.status === "running" ? "❚❚" : "▶";
      if (e.speed instanceof HTMLInputElement && document.activeElement !== e.speed) {
        e.speed.value = String(Math.round((Math.log(s.race.speed) / Math.log(240)) * 100));
        if (e.speedTxt) e.speedTxt.textContent = `×${Math.round(s.race.speed)}`;
      }
    }
  }

  private renderEditors(s: AppState): void {
    const box = this.els.editorsBox;
    if (!box || !s.scenario) {
      box?.replaceChildren();
      this.editors = [];
      return;
    }
    const sc = s.scenario;
    if (this.editors.length === s.strategies.length && this.editorsScenario === sc) {
      this.editors.forEach((ed, i) => {
        const st = s.strategies[i];
        if (st) ed.set(st);
      });
      return;
    }
    this.editorsScenario = sc;
    this.editors = s.strategies.map(
      (st, i) =>
        new StrategyEditor(
          i,
          st,
          sc,
          {
            onChange: (idx, ns) => {
              const cur = this.store.get();
              const arr = [...cur.strategies];
              arr[idx] = { ...ns, name: letter(idx) };
              this.store.set({ strategies: arr, focus: idx });
            },
            onRemove: (idx) => {
              const cur = this.store.get();
              if (cur.strategies.length <= 1) return;
              const arr = cur.strategies
                .filter((_, k) => k !== idx)
                .map((x, k) => ({ ...x, name: letter(k) }));
              this.store.set({ strategies: arr, focus: 0 });
            },
            onFocus: (idx) => {
              if (this.store.get().focus !== idx) this.store.set({ focus: idx });
            },
          },
          s.strategies.length > 1,
        ),
    );
    box.replaceChildren(...this.editors.map((x) => x.el));
  }
  private editorsScenario: Scenario | null = null;

  // ---------------------------------------------------------------- frame

  private frame(now: number): void {
    const dt = this.lastFrame ? Math.min(0.1, (now - this.lastFrame) / 1000) : 0;
    this.lastFrame = now;
    const s = this.store.get();
    const cars: CarDraw[] = [];
    if (s.mode === "live" && this.playback && s.scenario) {
      const pb = this.playback;
      if (s.race.status === "running" || s.race.status === "finished") {
        this.playhead = Math.min(this.playhead + dt * s.race.speed, pb.available);
      }
      const lap = pb.lapAt(this.playhead);
      const order = new Map<number, number>();
      lap?.cars.forEach((c) => order.set(c.car, c.pos));
      for (let c = 0; c < s.scenario.cars; c++) {
        const p = pb.pose(c, this.playhead);
        cars.push({ ...p, pos: order.get(c) ?? s.scenario.grid.indexOf(c) + 1 });
      }
      if (lap && lap !== this.lastTowerLap) {
        this.lastTowerLap = lap;
        this.tower.update(lap);
        for (let k = this.radioShownLap + 1; k <= lap.lap; k++) {
          const lv = pb.laps[k - 1];
          if (lv)
            for (const m of radioForLap(lv, s.scenario.drivers, s.scenario.player)) this.radioQueue.push(m);
        }
        this.radioShownLap = lap.lap;
        this.flushRadio();
      }
      const e = this.els;
      const cur = lap?.lap ?? 0;
      const total = s.scenario.laps;
      if (e.lapBig)
        e.lapBig.replaceChildren(
          `T${Math.min(total, cur + (lap?.flag === "chequered" ? 0 : 1))}`,
          h("small", { text: `/${total}` }),
        );
      if (e.flag) {
        const chequered = lap?.flag === "chequered";
        e.flag.className = `flag ${chequered ? "chequered" : "green"}`;
        e.flag.textContent = chequered ? "DAMIER" : s.race.status === "paused" ? "PAUSE" : "VERT";
      }
      if (e.raceClock) e.raceClock.textContent = fmtRaceTime(this.playhead);
    } else if (s.mode === "lab" && this.lastTowerLap && s.scenario) {
      this.lastTowerLap = null;
      this.tower.showGrid(s.scenario.grid);
    }
    this.track.draw(now, cars);
    this.conv.draw(now);
    this.cliff.draw();
  }
}

function defaultStrategies(sc: Scenario): StrategyOut[] {
  const a: StrategyOut = {
    name: "A",
    start: sc.suggested.start,
    stops: sc.suggested.stops.map((x) => ({ ...x })),
  };
  const L = sc.laps;
  const b: StrategyOut = {
    name: "B",
    start: "S",
    stops: [
      { lap: Math.round(L * 0.3), compound: "M" },
      { lap: Math.round(L * 0.65), compound: "H" },
    ],
  };
  return validate(b, L) ? [a] : [a, b];
}

function simVerdict(p: SimProgress): string {
  const best = p.strategies.reduce((b, s, i, a) => ((a[b]?.meanPos.p ?? Infinity) <= s.meanPos.p ? b : i), 0);
  const s = p.strategies[best];
  if (!s) return "Simulation terminée.";
  return `${fmtInt(p.races)} courses : ${letter(best)} (${s.plan}) — podium ${(s.podium.p * 100).toFixed(0)} %, victoire ${(s.win.p * 100).toFixed(1)} %, pire 5 % : P${s.cvarPos.toFixed(0)}.`;
}

const WORDS = ["NIGHT", "EMBER", "ORBIT", "DELTA", "NOVA", "RIDGE", "VAPOR", "QUARTZ", "SABLE", "HALO"];
function pickWord(): string {
  return WORDS[Math.floor(Math.random() * WORDS.length)] ?? "NIGHT";
}
