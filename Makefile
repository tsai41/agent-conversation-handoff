ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR ?= $(HOME)/bin
COMMAND ?= ccs
DEST := $(BIN_DIR)/$(COMMAND)
DIST := $(ROOT)/dist
REPO := tsai41/agent-conversation-handoff

ifeq ($(shell uname -m),arm64)
  ASSET := ach-darwin-arm64
else
  ASSET := ach-darwin-amd64
endif

.PHONY: install test build build-darwin-arm64 build-darwin-amd64

build: build-darwin-arm64 build-darwin-amd64

build-darwin-arm64:
	@mkdir -p "$(DIST)"
	GOOS=darwin GOARCH=arm64 go build -o "$(DIST)/ach-darwin-arm64" ./cmd/ach

build-darwin-amd64:
	@mkdir -p "$(DIST)"
	GOOS=darwin GOARCH=amd64 go build -o "$(DIST)/ach-darwin-amd64" ./cmd/ach

# Downloads the latest release binary for this machine's arch -- no Go
# toolchain needed on the machine running `make install`. Only the
# maintainer's machine needs Go, to `make build` and push a release tag.
install:
	@command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 || { echo "找不到 claude 或 codex。請先安裝至少一個 CLI。" >&2; exit 1; }
	@command -v fzf >/dev/null 2>&1 || { echo "缺少 fzf。macOS 可執行：brew install fzf" >&2; exit 1; }
	@command -v curl >/dev/null 2>&1 || { echo "缺少 curl。" >&2; exit 1; }
	@mkdir -p "$(BIN_DIR)"
	@if [ -e "$(DEST)" ] && [ ! -L "$(DEST)" -o "$$(readlink "$(DEST)" 2>/dev/null)" = "" ]; then \
		backup="$(DEST).bak-$$(date +%Y%m%d-%H%M%S)"; \
		mv "$(DEST)" "$$backup"; \
		echo "✓ 已備份舊指令 → $$backup"; \
	fi
	@url=$$(curl -fsSL "https://api.github.com/repos/$(REPO)/releases/latest" \
		| grep -o '"browser_download_url": *"[^"]*$(ASSET)"' \
		| head -n1 | cut -d '"' -f4); \
	if [ -z "$$url" ]; then \
		echo "在最新 release 找不到 $(ASSET)，請先 push 一個 v* tag 觸發 release workflow。" >&2; \
		exit 1; \
	fi; \
	curl -fsSL -o "$(DEST)" "$$url"
	@chmod +x "$(DEST)"
	@echo "✓ 已安裝 $(DEST)（$(ASSET)，來自最新 GitHub Release）"
	@case ":$$PATH:" in \
		*":$(BIN_DIR):"*) ;; \
		*) echo ""; echo "請把 ~/bin 加入 PATH，然後重新開啟終端："; echo 'export PATH="$$HOME/bin:$$PATH"' ;; \
	esac
	@echo ""
	@echo "完成後，在任意專案目錄執行：$(COMMAND)"

test:
	@go test ./...
