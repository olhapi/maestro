.PHONY: build test dev e2e-real-codex e2e-real-codex-phases

build:
	go build -o ./maestro ./cmd/maestro

test:
	go test ./...

dev:
	./scripts/dev.sh

package-npm:
	./scripts/package_npm_release.sh $(VERSION)

e2e-real-codex:
	./scripts/e2e_real_codex.sh

e2e-real-codex-phases:
	./scripts/e2e_real_codex_phases.sh
