---
description: Lint + types + tests + determinism (the gate before "done")
---
Run the full quality gate and report it.

!`make verify 2>&1 | tail -60`

If anything failed: identify the root cause from the output above, fix it (within your permissions), and run `make verify` again until it passes. Never weaken or skip a test. Finish with a one-line summary: lint / types / Go tests / web tests / determinism.
