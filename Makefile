VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PNPM ?= pnpm
GOFLAGS_BUILD = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build frontend backend backend-test sqlc test e2e lint dev docker clean

build: frontend backend

frontend:
	cd frontend && $(PNPM) install --frozen-lockfile && $(PNPM) build
	rm -rf backend/internal/web/dist
	cp -R frontend/build backend/internal/web/dist

backend:
	cd backend && CGO_ENABLED=0 go build $(GOFLAGS_BUILD) -o ../bin/filet ./cmd/filet

# Server built with the fault-injection middleware for end-to-end tests.
backend-test:
	cd backend && CGO_ENABLED=0 go build -tags filettest $(GOFLAGS_BUILD) -o ../bin/filet-test ./cmd/filet

sqlc:
	cd backend && sqlc generate

test:
	cd backend && go test -tags filettest ./...
	cd frontend && $(PNPM) vitest run
	cd frontend && $(PNPM) check

e2e: frontend backend-test
	cd frontend && FILET_BIN=../bin/filet-test $(PNPM) playwright test

lint:
	cd backend && golangci-lint run
	cd frontend && $(PNPM) check

# Runs the filet:dev image (build it with `make docker VERSION=dev`).
dev:
	mkdir -p .dev/data .dev/files
	docker run --rm -it --name filet \
		-e FILET_USERNAME=test \
		-e FILET_PASSWORD=test \
		-v "$(CURDIR)/deploy/config.dev.yaml:/config/config.yaml:ro" \
		-v "$(CURDIR)/.dev/data:/.dev/data" \
		-v "$(CURDIR)/.dev/files:/.dev/files" \
		-p 127.0.0.1:8080:8080 \
		filet:dev

docker:
	docker build --build-arg VERSION=$(VERSION) -t filet:$(VERSION) .

clean:
	rm -rf bin frontend/build frontend/.svelte-kit
	rm -rf backend/internal/web/dist && mkdir -p backend/internal/web/dist && touch backend/internal/web/dist/.gitkeep
