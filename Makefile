MAIN_PKG=./cmd/codarr
BINARY=codarr

GO_LINT_CONFIG     ?= .build/golangci.yaml
CANONICAL_LINT_URL := https://raw.githubusercontent.com/yama6a/gha/v2/.golangci.yaml
IMAGE              ?= ghcr.io/yama6a/codarr

# Local dev database and listen address. `?=` so an exported value wins.
CODARR_DB?=./data/codarr.db
CODARR_LISTEN?=:8080
export CODARR_DB
export CODARR_LISTEN

.PHONY: lint-config generate fmt fmt-check lint vet test cover vuln tidy tidy-check \
	generate-check mod image ci

lint-config:
	mkdir -p .build
	curl -fsSL $(CANONICAL_LINT_URL) -o .build/canonical-golangci.yaml
	if [ -f .golangci.local.yaml ]; then \
		yq eval-all '. as $$item ireduce ({}; . *+ $$item)' \
			.build/canonical-golangci.yaml .golangci.local.yaml > $(GO_LINT_CONFIG); \
	else \
		cp .build/canonical-golangci.yaml $(GO_LINT_CONFIG); \
	fi

generate:
	go generate ./...

fmt: lint-config
	golangci-lint fmt -c $(GO_LINT_CONFIG)

fmt-check: lint-config
	golangci-lint fmt --diff -c $(GO_LINT_CONFIG)

lint: lint-config
	golangci-lint run ./... -c $(GO_LINT_CONFIG)

vet:
	go vet ./...

test:
	go test ./... -race -count=1

cover:
	go test ./... -coverprofile=cover.out -covermode=atomic
	go tool cover -func=cover.out | tail -1

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

tidy:
	go mod tidy

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

generate-check: generate
	git diff --exit-code

mod:
	go get -u -t ./...
	go mod tidy

image:
	docker buildx build -f .build/Dockerfile -t $(IMAGE) --load .

ci: tidy-check generate-check fmt-check lint vet test vuln

.PHONY: cover-core run build web-deps web web-dev web-ci

# decide and ffmpeg carry the encoding decisions, so their coverage is tracked on its own.
cover-core:
	go test -coverprofile=.build/cover-core.out ./internal/decide/... ./internal/ffmpeg/...
	go tool cover -func=.build/cover-core.out | tail -1

run:
	go run $(MAIN_PKG)

# go:embed of internal/web/dist needs the frontend built first.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BINARY) $(MAIN_PKG)

web-deps:
	cd web && npm ci

web:
	cd web && npm run build

web-dev:
	cd web && npm run dev

web-ci:
	cd web && npm run ci
