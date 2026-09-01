MAIN_PKG=./cmd/codarr
BINARY=codarr
IMAGE=codarr:dev

# Local dev database and listen address. `?=` so an exported value wins.
CODARR_DB?=./data/codarr.db
CODARR_LISTEN?=:8080
export CODARR_DB
export CODARR_LISTEN

# Nothing in the module needs cgo, and modernc.org/sqlite is pure Go. Pin it off
# so a cgo dependency fails here rather than passing CI and only breaking the
# image build.
CGO_ENABLED=0
export CGO_ENABLED

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development
.PHONY: lint
lint: assert_golangci_lint_installed ## Run code linters.
	golangci-lint run ./... --concurrency 2 -c .golangci.yaml

.PHONY: vet
vet: assert_go_installed ## Run go vet.
	go vet ./...

.PHONY: vuln
vuln: assert_govulncheck_installed ## Run govulncheck.
	govulncheck ./...

.PHONY: test
test: assert_go_installed ## Run tests. ffmpeg-dependent tests skip when ffmpeg is absent.
	go test ./...

.PHONY: cover
cover: assert_go_installed ## Report branch coverage for the three packages that must stay near 100%.
	go test -coverprofile=.build/cover.out ./internal/decide/... ./internal/ffmpeg/...
	go tool cover -func=.build/cover.out | tail -1

.PHONY: ci
ci: fumpt generate lint vet vuln test ## Run all checks.

.PHONY: generate
generate: assert_go_installed ## Run code generation (oapi-codegen, moq).
	go generate ./...

.PHONY: fumpt
fumpt: assert_gofumpt_installed ## Format with gofumpt.
	gofumpt -w .

.PHONY: build
build: web assert_go_installed ## Build the binary with the frontend embedded.
	go build -trimpath -ldflags='-s -w' -o $(BINARY) $(MAIN_PKG)

.PHONY: mod
mod: assert_go_installed ## Update go modules.
	go get -u -t ./...
	go mod tidy

.PHONY: run
run: assert_go_installed ## Run the server locally against CODARR_DB.
	go run $(MAIN_PKG)

##@ Frontend
.PHONY: web-deps
web-deps: assert_npm_installed ## Install frontend dependencies.
	cd web && npm ci

.PHONY: web
web: assert_npm_installed ## Build the frontend into internal/web/dist.
	cd web && npm run build

.PHONY: web-dev
web-dev: assert_npm_installed ## Run the Vite dev server, proxying /api to CODARR_LISTEN.
	cd web && npm run dev

.PHONY: web-ci
web-ci: assert_npm_installed ## Run the frontend checks.
	cd web && npm run ci

##@ Container
.PHONY: image
image: assert_docker_installed ## Build the amd64 image. QSV and the Intel VAAPI driver are amd64 only.
	docker buildx build --platform linux/amd64 -f .build/Dockerfile -t $(IMAGE) --load .

##@ Assertions
.PHONY: assert_go_installed
assert_go_installed: ## Assert go is installed.
	@if ! command -v go >/dev/null 2>&1; then \
		echo "go is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi

.PHONY: assert_golangci_lint_installed
assert_golangci_lint_installed: ## Assert golangci-lint is installed.
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi

.PHONY: assert_gofumpt_installed
assert_gofumpt_installed: ## Assert gofumpt is installed.
	@if ! command -v gofumpt >/dev/null 2>&1; then \
		echo "gofumpt is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi

.PHONY: assert_govulncheck_installed
assert_govulncheck_installed: ## Assert govulncheck is installed.
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi

.PHONY: assert_npm_installed
assert_npm_installed: ## Assert npm is installed.
	@if ! command -v npm >/dev/null 2>&1; then \
		echo "npm is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi

.PHONY: assert_docker_installed
assert_docker_installed: ## Assert docker is installed.
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "docker is not installed; you need to install it in order to run this command"; \
		exit 1; \
	fi
