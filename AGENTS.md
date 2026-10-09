# AGENTS.md — PIT WALL

Source of truth for every agent (OpenCode, Claude, humans). If this file and the
code disagree, fix one of them in the same change.

## What this is

PIT WALL is a race-strategy command centre. A Go engine simulates thousands of
races (Monte Carlo) on procedurally generated circuits with 10–20 fictional
cars, finds and compares pit-stop strategies, streams the statistics over a
WebSocket, and a TypeScript/Canvas frontend shows them converging live, plays the
race on an animated track and explains decisions. Everything is fictional:
names, teams, circuits. No brand, no logo, no real championship. No generative
AI anywhere: simulation, statistics and deterministic templates only.

## Layout

| path | owner agent | content |
|---|---|---|
| `server/internal/rng` | engine | SplitMix64 value-type streams, `Derive(path…)` sub-streams |
| `server/internal/trackgen` | engine | circuits from a seed (geometry → speed profile → lap time, zones, wear, pit loss) |
| `server/internal/model` | engine | tyre / fuel / traffic / overtaking models — **documented in `docs/MODELS.md`** |
| `server/internal/race` | engine | `Scenario` (fixed per seed), `State` (value type = snapshot), `Step` |
| `server/internal/montecarlo` | engine | parallel runner, deterministic checkpoints, streaming aggregates |
| `server/internal/stats` | engine | Wilson, quantiles, median CI, CVaR |
| `server/internal/api` | engine | protocol v1 (`docs/PROTOCOL.md`), validation, sessions, limits |
| `server/cmd/pitwall` | engine | `serve` · `sim` · `verify` · `bench` |
| `web/src/net` | frontend | runtime decoders, WS client (reconnect + resume), store |
| `web/src/track`, `web/src/charts`, `web/src/ui`, `web/src/live` | frontend | Canvas/DOM rendering |
| `server/**/*_test.go`, `web/tests`, `web/e2e` | redteam (+ owners) | tests, fuzz corpora |

## Invariants (never break these)

1. **Determinism.** Same seed + same inputs ⇒ byte-identical results, with 1 or
   16 goroutines. All randomness comes from `internal/rng`; derive a sub-stream
   per (simulation, car, purpose). Forbidden in `server/internal`: `math/rand`,
   `time.Now()` in anything that influences a result, map iteration order in
   anything that influences a result. Proved by `make determinism`.
2. **Server-side validation.** Every inbound field is decoded strictly (unknown
   fields, trailing data, quoted numbers rejected) and bounds-checked. The
   client validates only for UX; the server is the authority.
3. **Zero panic.** No input can crash the server: every goroutine recovers,
   every error is a `ProtoError{code, message, field}` in French, clear and
   actionable. The client never throws on an unexpected server message.
4. **Units.** Every model parameter has a unit and bounds, in a comment next to
   it *and* in `docs/MODELS.md`. Time is seconds, mass kg, distance m,
   wear is a dimensionless fraction of tyre life.
5. **Race properties** (tested every lap on many seeds): positions are a
   permutation, cumulative time strictly increases, no negative time or fuel,
   at most one pit stop per car per lap, retired cars are classified last.
6. **Bounded resources.** ≤ 50 000 sims per strategy, ≤ 4 strategies, one
   search per session, server-wide job semaphore, 25 s budget per job,
   64 KiB per message, token-bucket rate limit, cancellation when the client
   leaves.
7. **Offline.** Nothing calls the Internet at runtime (fonts are bundled).
8. **Performance.** ≥ 20 000 races/s (20 cars × 50 laps) on one reasonable core;
   the hot loop (`race.Step`) allocates nothing. `make bench`.

## Commands

| command | what |
|---|---|
| `make dev` | Go :8080 + Vite :5173 with hot reload → http://localhost:5173/?seed=NIGHT-42 |
| `make run` | build everything, one binary on :8080 |
| `make verify` | lint + types + tests + determinism — **run before declaring any task done** |
| `make test` | `go test -race ./...` + `vitest run` |
| `make e2e` | Playwright against the real binary |
| `make fuzz FUZZTIME=30s` | all Go fuzz targets |
| `make determinism` | 1 vs 16 workers, byte-identical JSON |
| `make bench` / `make bench-check` | races per second / regression floor |
| `cd server && go run ./cmd/pitwall sim -seed X M-25-H S-15-M-35-H` | quick CLI comparison |

## Conventions

- Go: stdlib + `github.com/coder/websocket` only. `gofmt`, `golangci-lint`
  (`server/.golangci.yml`). Hot paths: fixed-size arrays, no allocation, no
  interface dispatch, no `math.Min/Max` (NaN handling costs).
- TypeScript: `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`,
  `typescript-eslint strictTypeChecked`, Prettier. No framework; d3 only for
  scales/shapes. DOM text is always inserted as text nodes (`ui/dom.ts#h`).
- Protocol changes: update `docs/PROTOCOL.md`, `api/protocol.go`,
  `web/src/net/protocol.ts` and both test suites in the same change. JSON
  slices are never `null` (initialise them).
- Model changes: use the `model-change` skill (docs + bounds + tests).
- UI language: French; code, identifiers and comments: English.
- Commits: one feature + its tests per commit, imperative subject.

## Forbidden

- Real names: drivers, teams, circuits, championships, brands, logos.
- Generative AI / LLM calls in the product.
- Disabling, skipping or weakening a test to get green; `//nolint` without a reason.
- `math/rand`, wall-clock time or goroutine scheduling influencing results.
- Trusting the client; `panic` as error handling; unbounded loops on input.
- Marking a task done without having run `make verify` (and `make e2e` for UI work).
