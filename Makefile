.PHONY: behavior-contract-check check contract-check contract-release-check coverage desktop dev format-check frontend-check install-playwright install-wails lint security storybook storybook-build test workflow-check

GO ?= GOWORK=off go
WAILS ?= $(shell GOWORK=off go env GOPATH)/bin/wails
WAILS_VERSION ?= $(shell GOWORK=off go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
WAILS_DEV_SERVER ?= 127.0.0.1:34116
GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION ?= v1.6.0
ACTIONLINT_VERSION ?= v1.7.10
NPM ?= npx --yes npm@12.0.2
VERSION ?= $(shell cat VERSION)
GO_FILES := $(shell find . -maxdepth 1 -name '*.go' -type f) $(shell find internal -name '*.go' -type f)

install-wails:
	@test -x "$(WAILS)" && "$(WAILS)" version | grep -q '$(WAILS_VERSION)' || \
		$(GO) install github.com/wailsapp/wails/v2/cmd/wails@$(WAILS_VERSION)

install-playwright:
	$(GO) run github.com/mxschmitt/playwright-go/cmd/playwright@$$(bash scripts/playwright-version.sh go) install chromium

desktop: install-wails
	$(WAILS) build -clean -trimpath -m -nosyncgomod -ldflags "-s -w -X main.desktopVersion=$(VERSION)"

dev: install-wails install-playwright
	mkdir -p .cineko/dev
	CINEKO_DATA_DIR="$${CINEKO_DATA_DIR:-$(CURDIR)/.cineko/dev}" \
		$(WAILS) dev -m -nosyncgomod -devserver "$(WAILS_DEV_SERVER)" \
		-assetdir internal/interfaces/webui/assets \
		-reloaddirs internal/interfaces/webui/assets

format-check:
	@test -z "$$(gofmt -l $(GO_FILES))" || (gofmt -l $(GO_FILES) && exit 1)

lint: format-check
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

security:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	$(NPM) --prefix frontend audit --audit-level=moderate

coverage:
	bash scripts/unit-coverage.sh

test: install-playwright
	$(GO) test -mod=vendor -race ./...

frontend-check:
	$(NPM) --prefix frontend run check

storybook:
	$(NPM) --prefix frontend run storybook

storybook-build:
	$(NPM) --prefix frontend run storybook:build

workflow-check:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml
	bash -n scripts/configure-ubuntu-mirror.sh scripts/package-client.sh scripts/package-playwright.sh scripts/playwright-browser-version.sh scripts/playwright-version.sh scripts/post-release-registry.sh scripts/publish-release.sh scripts/publish-official-browser-release.sh scripts/publish-playwright-assets.sh scripts/register-browser-release.sh scripts/register-client-release.sh scripts/register-playwright-release.sh scripts/sign-notarize-macos-client.sh scripts/test-browser-release.sh scripts/test-post-release-registry.sh scripts/test-publish-playwright-assets.sh scripts/test-register-client-release.sh scripts/test-register-playwright-release.sh scripts/verify-macos-signing-workflow.sh
	shellcheck scripts/configure-ubuntu-mirror.sh scripts/package-client.sh scripts/package-playwright.sh scripts/playwright-browser-version.sh scripts/playwright-version.sh scripts/post-release-registry.sh scripts/publish-release.sh scripts/publish-official-browser-release.sh scripts/publish-playwright-assets.sh scripts/register-browser-release.sh scripts/register-client-release.sh scripts/register-playwright-release.sh scripts/sign-notarize-macos-client.sh scripts/test-browser-release.sh scripts/test-post-release-registry.sh scripts/test-publish-playwright-assets.sh scripts/test-register-client-release.sh scripts/test-register-playwright-release.sh scripts/verify-macos-signing-workflow.sh
	bash scripts/verify-macos-signing-workflow.sh
	scripts/test-browser-release.sh
	scripts/test-post-release-registry.sh
	scripts/test-publish-playwright-assets.sh
	scripts/test-register-playwright-release.sh
	bash scripts/test-register-client-release.sh
	node --test scripts/release-metadata.test.mjs

contract-check:
	grep -Eq '^# github.com/cineko-org/contracts/v3 v3.5.3( => ../contracts)?$$' vendor/modules.txt

contract-release-check:
	@! grep -Eq '^[[:space:]]*replace([[:space:]]|\()' go.mod
	@grep -Eq '^[[:space:]]*github.com/cineko-org/contracts/v3 v3.5.3$$' go.mod
	@grep -Eq '^# github.com/cineko-org/contracts/v3 v3.5.3$$' vendor/modules.txt
	@grep -Eq '^github.com/cineko-org/contracts/v3 v3.5.3 h1:' go.sum

behavior-contract-check:
	bash scripts/verify-behavior-contract.sh

check: lint security coverage test frontend-check workflow-check contract-check behavior-contract-check
	node --check internal/interfaces/webui/assets/app.js
	grep -Eq '^# github.com/cineko-org/probe/v2 v2.7.0( => ../probe)?$$' vendor/modules.txt
