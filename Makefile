ARCH ?= amd64

.PHONY: build test deploy

build:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags "-s -w" -o dist/dumper ./cmd/dumper

test:
	go test ./...

deploy: build
	@test -n "$(HOST)" || { echo 'HOST is required, e.g. make deploy HOST=user@host'; exit 1; }
	scp dist/dumper "$(HOST):/tmp/dumper"
	ssh -t "$(HOST)" 'sudo /tmp/dumper install && rm -f /tmp/dumper'
