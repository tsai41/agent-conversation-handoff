ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR ?= $(HOME)/bin
COMMAND ?= ccs
LAUNCHER := $(ROOT)/bin/launcher
DEST := $(BIN_DIR)/$(COMMAND)

.PHONY: install test

install:
	@command -v python3 >/dev/null 2>&1 || { echo "缺少 python3。請先安裝 Python 3。" >&2; exit 1; }
	@command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 || { echo "找不到 claude 或 codex。請先安裝至少一個 CLI。" >&2; exit 1; }
	@command -v fzf >/dev/null 2>&1 || { echo "缺少 fzf。macOS 可執行：brew install fzf" >&2; exit 1; }
	@mkdir -p "$(BIN_DIR)"
	@if [ -e "$(DEST)" ] || [ -L "$(DEST)" ]; then \
		if [ -L "$(DEST)" ] && [ "$$(readlink "$(DEST)")" = "$(LAUNCHER)" ]; then \
			echo "✓ $(DEST) 已指向此 repo"; \
		else \
			backup="$(DEST).bak-$$(date +%Y%m%d-%H%M%S)"; \
			mv "$(DEST)" "$$backup"; \
			echo "✓ 已備份舊指令 → $$backup"; \
		fi; \
	fi
	@ln -sfn "$(LAUNCHER)" "$(DEST)"
	@echo "✓ 已安裝 $(DEST) → $(LAUNCHER)"
	@case ":$$PATH:" in \
		*":$(BIN_DIR):"*) ;; \
		*) echo ""; echo "請把 ~/bin 加入 PATH，然後重新開啟終端："; echo 'export PATH="$$HOME/bin:$$PATH"' ;; \
	esac
	@echo ""
	@echo "完成後，在任意專案目錄執行：$(COMMAND)"

test:
	@python3 -m unittest discover -s tests -v
