.PHONY: frontend-build build test test-cover test-race start dev e2e-real-codex e2e-real-codex-phases

GO_TEST_PACKAGES := ./cmd/... ./internal/... ./pkg/...
START_DB_PATH ?= $(HOME)/.maestro/maestro.db
START_PORT ?= 8787
START_REPO_PATH ?=
MAESTRO_BIN ?= ./maestro

frontend-build:
	npm --prefix frontend run build

build: frontend-build
	go build -o $(MAESTRO_BIN) ./cmd/maestro

test:
	go test $(GO_TEST_PACKAGES)

test-cover:
	./scripts/check_coverage.sh

test-race:
	go test -race $(GO_TEST_PACKAGES)

start: build
	@if [ -n "$(START_REPO_PATH)" ]; then \
		printf '\nMaestro production start\n'; \
		printf '  Repo:      %s\n' "$(START_REPO_PATH)"; \
		printf '  DB:        %s\n' "$(START_DB_PATH)"; \
		printf '  Dashboard: http://127.0.0.1:%s/\n' "$(START_PORT)"; \
		printf '  API:       http://127.0.0.1:%s/api/v1/\n' "$(START_PORT)"; \
		printf '  Mode:      embedded frontend via Go server\n'; \
		printf '  Stop:      Ctrl+C\n\n'; \
		$(MAESTRO_BIN) run --db "$(START_DB_PATH)" --port "$(START_PORT)" "$(START_REPO_PATH)"; \
	else \
		printf '\nMaestro production start\n'; \
		printf '  Repo:      all shared projects\n'; \
		printf '  DB:        %s\n' "$(START_DB_PATH)"; \
		printf '  Dashboard: http://127.0.0.1:%s/\n' "$(START_PORT)"; \
		printf '  API:       http://127.0.0.1:%s/api/v1/\n' "$(START_PORT)"; \
		printf '  Mode:      embedded frontend via Go server\n'; \
		printf '  Stop:      Ctrl+C\n\n'; \
		$(MAESTRO_BIN) run --db "$(START_DB_PATH)" --port "$(START_PORT)"; \
	fi

dev:
	./scripts/dev.sh

package-npm:
	./scripts/package_npm_release.sh $(VERSION)

e2e-real-codex:
	./scripts/e2e_real_codex.sh

e2e-real-codex-phases:
	./scripts/e2e_real_codex_phases.sh
