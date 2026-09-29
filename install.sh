#!/usr/bin/env bash
# Installs the ach/ccs binary from the latest GitHub Release. No git clone,
# no Go toolchain -- only fzf and gh (repo is private, so gh auth is what
# lets the release download succeed).
set -euo pipefail

REPO="tsai41/agent-conversation-handoff"
BIN_DIR="${BIN_DIR:-$HOME/bin}"
COMMAND="${COMMAND:-ccs}"
DEST="$BIN_DIR/$COMMAND"

command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 || {
	echo "找不到 claude 或 codex。請先安裝至少一個 CLI。" >&2
	exit 1
}
command -v fzf >/dev/null 2>&1 || {
	echo "缺少 fzf。macOS 可執行：brew install fzf" >&2
	exit 1
}
command -v gh >/dev/null 2>&1 || {
	echo "缺少 gh (GitHub CLI)。repo 為 private，需要 gh 才能抓 release。macOS 可執行：brew install gh，然後 gh auth login" >&2
	exit 1
}

case "$(uname -m)" in
arm64) ASSET="ach-darwin-arm64" ;;
*) ASSET="ach-darwin-amd64" ;;
esac

mkdir -p "$BIN_DIR"
tmpdir=$(mktemp -d "$BIN_DIR/.${COMMAND}.download.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT
gh release download --repo "$REPO" --pattern "$ASSET" --output "$tmpdir/$ASSET" --clobber
gh release download --repo "$REPO" --pattern "SHA256SUMS" --output "$tmpdir/SHA256SUMS" --clobber
(cd "$tmpdir" && grep " $ASSET\$" SHA256SUMS | shasum -a 256 -c -)
chmod +x "$tmpdir/$ASSET"

if [ -e "$DEST" ] && [ ! -L "$DEST" ]; then
	backup="$DEST.bak-$(date +%Y%m%d-%H%M%S)"
	mv "$DEST" "$backup"
	echo "✓ 已備份舊指令 → $backup"
fi

mv "$tmpdir/$ASSET" "$DEST"
echo "✓ 已安裝 ${DEST}（${ASSET}，來自最新 GitHub Release）"

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*)
	echo ""
	echo "請把 $BIN_DIR 加入 PATH，然後重新開啟終端："
	echo "export PATH=\"$BIN_DIR:\$PATH\""
	;;
esac

echo ""
echo "完成後，在任意專案目錄執行：$COMMAND"
