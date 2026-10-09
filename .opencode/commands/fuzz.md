---
description: Run every Go fuzz target (/fuzz 60s; default 10s each)
agent: redteam
---
Run the fuzzers for $ARGUMENTS each:

!`make fuzz FUZZTIME=$ARGUMENTS 2>&1 | tail -40`

If a fuzzer found a crasher, the input is in `server/internal/*/testdata/fuzz/`. Explain the failing input, write a regular regression test reproducing it, and report which production file must change (you cannot edit production code).
