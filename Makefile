.PHONY: behavior-contract-check check contract-check contract-release-check coverage desktop dev format-check frontend-check install-playwright install-wails lint security storybook storybook-build test workflow-check

GO ?= GOWORK=off go
WAILS ?= $(shell GOWORK=off go env GOPATH)/bin/wails
WAILS_VERSION ?= $(shell GOWORK=off go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
WAILS_DEV_SERVER ?= 127.0.0.1:34116
CINEKO_LOCAL_DATA_DIR ?= $(if $(strip $(CINEKO_DATA_DIR)),$(CINEKO_DATA_DIR),$(HOME)/cineko)
PLAYWRIGHT_DRIVER_VERSION ?= $(shell bash scripts/playwright-version.sh driver)
PLAYWRIGHT_ROOT ?= $(CINEKO_LOCAL_DATA_DIR)/runtime/playwright
PLAYWRIGHT_DRIVER_DIR ?= $(PLAYWRIGHT_ROOT)/driver/$(PLAYWRIGHT_DRIVER_VERSION)
PLAYWRIGHT_BROWSERS_DIR ?= $(PLAYWRIGHT_ROOT)/browsers
CINEKO_TMP_DIR ?= $(CINEKO_LOCAL_DATA_DIR)/tmp
GOLANGCI_LINT_VERSION ?= v2.13.1
GOVULNCHECK_VERSION ?= v1.6.0
ACTIONLINT_VERSION ?= v1.7.10
NPM ?= npx --yes npm@12.0.2
VERSION ?= $(shell cat VERSION)
GO_FILES := $(shell find . -maxdepth 1 -name '*.go' -type f) $(shell find internal -name '*.go' -type f)

install-wails:
	@test -x "$(WAILS)" && "$(WAILS)" version | grep -q '$(WAILS_VERSION)' || \
		$(GO) install github.com/wailsapp/wails/v2/cmd/wails@$(WAILS_VERSION)

install-playwright:
	mkdir -p "$(PLAYWRIGHT_DRIVER_DIR)" "$(PLAYWRIGHT_BROWSERS_DIR)" "$(CINEKO_TMP_DIR)"
	PLAYWRIGHT_DRIVER_PATH="$(PLAYWRIGHT_DRIVER_DIR)" \
		PLAYWRIGHT_BROWSERS_PATH="$(PLAYWRIGHT_BROWSERS_DIR)" \
		TMPDIR="$(CINEKO_TMP_DIR)" \
		$(GO) run github.com/mxschmitt/playwright-go/cmd/playwright@$$(bash scripts/playwright-version.sh go) install chromium

desktop: install-wails
	$(WAILS) build -clean -trimpath -m -nosyncgomod -ldflags "-s -w -X main.desktopVersion=$(VERSION)"

dev: install-wails install-playwright
	mkdir -p "$(CINEKO_LOCAL_DATA_DIR)" "$(CINEKO_TMP_DIR)"
	CINEKO_DATA_DIR="$(CINEKO_LOCAL_DATA_DIR)" \
		CINEKO_PLAYWRIGHT_DRIVER_PATH="$(PLAYWRIGHT_DRIVER_DIR)" \
		PLAYWRIGHT_DRIVER_PATH="$(PLAYWRIGHT_DRIVER_DIR)" \
		PLAYWRIGHT_BROWSERS_PATH="$(PLAYWRIGHT_BROWSERS_DIR)" \
		TMPDIR="$(CINEKO_TMP_DIR)" \
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
	CINEKO_DATA_DIR="$(CINEKO_LOCAL_DATA_DIR)" \
		CINEKO_PLAYWRIGHT_DRIVER_PATH="$(PLAYWRIGHT_DRIVER_DIR)" \
		PLAYWRIGHT_DRIVER_PATH="$(PLAYWRIGHT_DRIVER_DIR)" \
		PLAYWRIGHT_BROWSERS_PATH="$(PLAYWRIGHT_BROWSERS_DIR)" \
		TMPDIR="$(CINEKO_TMP_DIR)" \
		$(GO) test -mod=vendor -race ./...

frontend-check:
	$(NPM) --prefix frontend run check

storybook:
	$(NPM) --prefix frontend run storybook

storybook-build:
	$(NPM) --prefix frontend run storybook:build

workflow-check:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml
	bash -n scripts/configure-ubuntu-mirror.sh scripts/package-client.sh scripts/package-playwright.sh scripts/playwright-browser-version.sh scripts/playwright-version.sh scripts/publish-release.sh scripts/publish-official-browser-release.sh scripts/publish-playwright-assets.sh scripts/publish-runtime-channel.sh scripts/register-browser-release.sh scripts/register-client-release.sh scripts/register-playwright-release.sh scripts/sign-notarize-macos-client.sh scripts/test-browser-release.sh scripts/test-publish-playwright-assets.sh scripts/verify-browser-release-identity.sh scripts/verify-macos-signing-workflow.sh
	shellcheck scripts/configure-ubuntu-mirror.sh scripts/package-client.sh scripts/package-playwright.sh scripts/playwright-browser-version.sh scripts/playwright-version.sh scripts/publish-release.sh scripts/publish-official-browser-release.sh scripts/publish-playwright-assets.sh scripts/publish-runtime-channel.sh scripts/register-browser-release.sh scripts/register-client-release.sh scripts/register-playwright-release.sh scripts/sign-notarize-macos-client.sh scripts/test-browser-release.sh scripts/test-publish-playwright-assets.sh scripts/verify-browser-release-identity.sh scripts/verify-macos-signing-workflow.sh
	bash scripts/verify-macos-signing-workflow.sh
	scripts/test-browser-release.sh
	scripts/test-publish-playwright-assets.sh
	node --test scripts/release-metadata.test.mjs

contract-check:
	@! grep -Eq '^replace github.com/cineko-org/(contracts/v3|probe/v2)' go.mod
	grep -Eq '^# github.com/cineko-org/contracts/v3 v3.7.0$$' vendor/modules.txt
	grep -Eq '^# github.com/cineko-org/probe/v2 v2.8.0$$' vendor/modules.txt

contract-release-check: contract-check

behavior-contract-check:
	bash scripts/verify-behavior-contract.sh

check: lint security coverage test frontend-check workflow-check contract-check behavior-contract-check
	node --check internal/interfaces/webui/assets/app.js
