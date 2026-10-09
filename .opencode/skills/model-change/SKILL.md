---
name: model-change
description: Checklist to add or change a physical/statistical model parameter of PIT WALL (tyres, fuel, traffic, overtaking, failures, weather) without breaking units, docs, determinism or performance.
---
# Changing a model

1. **Code** — `server/internal/model/model.go`: name the constant with its unit
   suffix (`…S` seconds, `…Kg`, `…M`, `…P` probability), comment `// unit, meaning  [lo, hi]`.
2. **Docs** — `docs/MODELS.md`: add the formula, the unit, the bounds and *why*
   (the hypothesis). Keep the parameter table sorted like the code.
3. **Randomness** — any new draw uses the car's stream `st.R[c]` (or a new
   `rng.Label…` sub-stream for a new channel). Never add a draw *before*
   existing ones in the same stream unless you accept that every result moves
   (then say so in the commit message).
4. **Tests** — in `internal/model`: monotonicity / bounds of the new function;
   in `internal/race`: the analytic single-car test must still match exactly
   (update the expected formula, not the tolerance); property tests unchanged.
5. **Determinism & perf** — `make determinism`, then `make bench`: `Step`
   must still allocate 0 and stay ≥ 20 000 races/s on one core.
6. **Balance** — `go run ./cmd/pitwall sim -seed NIGHT-42 M-25-H S-15-M-35-H H-30-M`:
   strategies must still be distinguishable (not all 0 % or 100 %).
