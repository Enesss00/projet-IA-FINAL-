# PIT WALL — one entry point for humans, agents and CI.
SHELL := /bin/bash
GO    ?= go
NPM   ?= npm
PORT  ?= 8080
CHROMIUM ?= $(firstword $(wildcard /opt/pw-browsers/chromium-*/chrome-linux/chrome))

.PHONY: help dev run build web server deps test test-go test-web e2e lint lint-go lint-web \
        fmt typecheck verify harness-check fuzz determinism bench bench-check demo docker clean

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[33m%-14s\033[0m %s\n", $$1, $$2}'

deps: ## install Go and npm dependencies
	cd server && $(GO) mod download
	cd web && $(NPM) ci

web/node_modules: web/package-lock.json
	cd web && $(NPM) ci
	@touch web/node_modules

dev: web/node_modules ## Go server :8080 + Vite dev server :5173 (hot reload) — open http://localhost:5173/?seed=NIGHT-42
	@trap 'kill 0' EXIT INT TERM; \
	(cd server && $(GO) run ./cmd/pitwall serve -addr 127.0.0.1:$(PORT)) & \
	(cd web && PITWALL_BACKEND=http://127.0.0.1:$(PORT) $(NPM) run dev -- --host 127.0.0.1) & \
	wait

run: build ## production-like: one binary serving the embedded frontend on :8080
	./server/bin/pitwall serve -addr :$(PORT)

web: web/node_modules ## build the frontend into server/internal/webui/dist
	cd web && $(NPM) run build

server: ## build the Go binary (embeds whatever frontend is built)
	cd server && CGO_ENABLED=0 $(GO) build -trimpath -o bin/pitwall ./cmd/pitwall

build: web server ## frontend + binary

# ---------------------------------------------------------------- quality
fmt: web/node_modules ## format Go and TS
	cd server && gofmt -w . && golangci-lint fmt ./... || true
	cd web && npx prettier --write .

lint-go:
	cd server && $(GO) vet ./... && golangci-lint run ./...
lint-web: web/node_modules
	cd web && npx eslint . && npx prettier --check .
lint: lint-go lint-web ## all linters

typecheck: web/node_modules ## tsc --noEmit
	cd web && npx tsc --noEmit

test-go:
	cd server && $(GO) test -race -count=1 ./...
test-web: web/node_modules
	cd web && npx vitest run
test: test-go test-web ## unit, property and statistical tests

e2e: build ## Playwright end-to-end against the real binary
	@set -e; ./server/bin/pitwall serve -addr 127.0.0.1:8099 & pid=$$!; trap "kill $$pid" EXIT; \
	for i in $$(seq 50); do curl -sf 127.0.0.1:8099/healthz >/dev/null && break; sleep 0.1; done; \
	cd web && PITWALL_URL=http://127.0.0.1:8099 PW_CHROMIUM=$(CHROMIUM) npx playwright test

FUZZTIME ?= 10s
FT := $(or $(FUZZTIME),10s)
fuzz: ## every Go fuzz target for FUZZTIME (default 10s each)
	cd server && for t in "FuzzDecode ./internal/api" "FuzzValidateStrategy ./internal/race" \
	  "FuzzParseStrategy ./internal/race" "FuzzGenerate ./internal/trackgen"; do \
	  set -- $$t; echo "== $$1"; $(GO) test -run '^$$' -fuzz "^$$1$$" -fuzztime $(FT) $$2 || exit 1; done

determinism: ## 1 vs 16 workers must give byte-identical results
	cd server && $(GO) run ./cmd/pitwall verify -seeds 4 -sims 2000 -workers 16
	cd server && $(GO) test -count=1 -run 'Determinism|Snapshot|Deterministic' ./...

bench: ## races per second (20 cars x 50 laps), single core and all cores
	cd server && $(GO) run ./cmd/pitwall bench -dur 3s

BENCH_MIN ?= 8000
bench-check: ## fail if single-core throughput drops below BENCH_MIN races/s
	cd server && $(GO) run ./cmd/pitwall bench -dur 2s -min $(BENCH_MIN)

harness-check: ## OpenCode quality-gate plugin logic + config JSON
	node --experimental-strip-types --no-warnings .opencode/test/quality-gate.test.ts
	@python3 -c "import json;json.load(open('opencode.json'))" && echo "opencode.json: valid JSON"

verify: lint typecheck test determinism harness-check ## everything an agent must run before saying "done"
	@echo -e "\033[32mverify: OK\033[0m"

demo: build ## start the server and print the demo URL
	@echo "PIT WALL → http://localhost:$(PORT)/?seed=NIGHT-42   (script: README.md#démo-2-minutes)"
	./server/bin/pitwall serve -addr :$(PORT)

docker: ## build the production image
	docker build -t pitwall:latest .

clean:
	rm -rf server/bin web/dist web/test-results web/playwright-report
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
