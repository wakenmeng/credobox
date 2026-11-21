# The Guest Agent runs inside the Docker VM, so it is built for Linux on the
# VM's architecture, statically.
DOCKER_ARCH := $(shell docker version --format '{{.Server.Arch}}' 2>/dev/null || go env GOARCH)

.PHONY: all build guestagent proto test test-docker

all: build

build: guestagent
	go build -o bin/ ./cmd/controlplane ./cmd/hostagent

guestagent:
	GOOS=linux GOARCH=$(DOCKER_ARCH) CGO_ENABLED=0 go build -o bin/linux/guestagent ./cmd/guestagent

proto:
	buf generate

test:
	go test ./...

# The end-to-end suite against real Docker.
test-docker:
	SANDBOX_DOCKER_TESTS=1 go test -count=1 ./e2e/
