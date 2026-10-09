---
description: Races per second (20 cars × 50 laps), single core and all cores
---
!`make bench 2>&1 | tail -10`

Compare with the target in AGENTS.md (≥ 20 000 races/s on one core). If below, profile:
`cd server && go test -run XXX -bench Race20x50 -benchmem -cpuprofile /tmp/cpu.out ./internal/race && go tool pprof -top /tmp/cpu.out | head -30`
and propose the change with the measured gain. Allocations in `Step` must stay at 0.
