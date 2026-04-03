.PHONY: frontend-build ensure-dashboard-dist build test test-cover test-race start dev e2e-real-codex e2e-real-codex-phases e2e-real-codex-issue-images e2e-real-claude e2e-retry-safety

GO_TEST_PACKAGES := ./cmd/... ./internal/... ./pkg/...
START_DB_PATH ?= $(HOME)/.maestro/maestro.db
START_PORT ?= 8787
START_REPO_PATH ?=
MAESTRO_BIN ?= ./maestro

frontend-build:
	pnpm run frontend:build

ensure-dashboard-dist:
	./scripts/ensure_dashboard_dist.sh

build: ensure-dashboard-dist
	go build -o $(MAESTRO_BIN) ./cmd/maestro

test: ensure-dashboard-dist
	go test $(GO_TEST_PACKAGES)

test-cover: ensure-dashboard-dist
	./scripts/check_coverage.sh

test-race: ensure-dashboard-dist
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

e2e-real-codex-issue-images:
	./scripts/e2e_real_codex_issue_images.sh

e2e-real-claude:
	./scripts/e2e_real_claude.sh

e2e-retry-safety:
	./scripts/e2e_retry_safety.sh
