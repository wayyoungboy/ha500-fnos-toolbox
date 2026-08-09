.PHONY: test check build stage

test:
	GOTOOLCHAIN=go1.25.12 go test ./...

check:
	GOTOOLCHAIN=go1.25.12 go test -race ./...
	GOTOOLCHAIN=go1.25.12 go vet ./...
	bash -n package/ha500-toolbox/cmd/* scripts/build.sh

stage:
	./scripts/build.sh --stage-only

build:
	./scripts/build.sh

