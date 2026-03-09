.PHONY: build test test-cover test-race dev e2e-real-codex e2e-real-codex-phases

GO_TEST_PACKAGES := ./cmd/... ./internal/... ./pkg/...

build:
	go build -o ./maestro ./cmd/maestro

test:
	go test $(GO_TEST_PACKAGES)

test-cover:
	./scripts/check_coverage.sh

test-race:
	go test -race $(GO_TEST_PACKAGES)

dev:
	./scripts/dev.sh

package-npm:
	./scripts/package_npm_release.sh $(VERSION)

e2e-real-codex:
	./scripts/e2e_real_codex.sh

e2e-real-codex-phases:
	./scripts/e2e_real_codex_phases.sh
