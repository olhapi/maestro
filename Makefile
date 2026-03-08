.PHONY: build test dev e2e-real-codex

build:
	go build -o ./symphony ./cmd/symphony

test:
	go test ./...

dev:
	./scripts/dev.sh

package-npm:
	./scripts/package_npm_release.sh $(VERSION)

e2e-real-codex:
	./scripts/e2e_real_codex.sh
