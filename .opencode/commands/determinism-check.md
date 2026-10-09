---
description: Prove that 1 and 16 workers give byte-identical Monte Carlo results
agent: reviewer
---
!`make determinism 2>&1 | tail -30`

Report PASS/FAIL per seed. On FAIL, inspect `git diff` for: new randomness outside `internal/rng`, map iteration or wall-clock time influencing results, aggregation that depends on completion order (it must only use the contiguous prefix of finished batches).
