.PHONY: test check build stage

test:
	GOTOOLCHAIN=go1.25.12 go test ./...
	node --test frontend-tests/*.test.mjs

check:
	GOTOOLCHAIN=go1.25.12 go test -race ./...
	GOTOOLCHAIN=go1.25.12 go vet ./...
	node --test frontend-tests/*.test.mjs
	bash -n package/ha500-toolbox/cmd/* scripts/build.sh

stage:
	./scripts/build.sh --stage-only

build:
	./scripts/build.sh
