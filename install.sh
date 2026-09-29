#!/usr/bin/env bash
# Installs the ach binary from the latest GitHub Release. No git clone,
# no Go toolchain -- only fzf and curl. If gh is installed and authenticated
# it is used for the download; otherwise curl fetches the public release.
set -Eeuo pipefail

REPO="tsai41/agent-conversation-handoff"
BIN_DIR="${BIN_DIR:-$HOME/bin}"
COMMAND="${COMMAND:-ach}"
DEST="$BIN_DIR/$COMMAND"

[ "$(uname -s)" = "Darwin" ] || {
	echo "ach 只支援 macOS，目前系統是 $(uname -s)。" >&2
	exit 1
}

command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 || {
	echo "找不到 claude 或 codex。請先安裝至少一個 CLI。" >&2
	exit 1
}
command -v fzf >/dev/null 2>&1 || {
	echo "缺少 fzf。macOS 可執行：brew install fzf" >&2
	exit 1
}
USE_GH=0
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	USE_GH=1
else
	command -v curl >/dev/null 2>&1 || {
		echo "缺少 curl，且 gh 未安裝或未登入。請安裝 curl 或執行 gh auth login" >&2
		exit 1
	}
fi

case "$(uname -m)" in
arm64) ASSET="ach-darwin-arm64" ;;
*) ASSET="ach-darwin-amd64" ;;
esac

step() { echo "• $*"; }

STEP=""
backup=""
on_err() {
	local rc=$?
	trap - ERR
	{
		echo ""
		case "$STEP" in
		resolve | download)
			echo "✗ 下載失敗：找不到 release，或網路無法連線。"
			echo "  請確認 repo 是公開的，或已登入 gh（gh auth login），並檢查網路。"
			;;
		verify)
			echo "✗ checksum 驗證失敗：下載的檔案不完整或被竄改，已中止。"
			;;
		*)
			echo "✗ 無法寫入 ${BIN_DIR}：沒有寫入權限，或路徑不可用。"
			echo "  請把 BIN_DIR 設成可寫入的目錄，例如 BIN_DIR=\$HOME/bin。"
			;;
		esac
		if [ -n "$backup" ]; then
			echo "  舊指令已備份為 ${backup}，新版尚未安裝。"
		else
			echo "  沒有安裝任何東西，$DEST 維持原狀。"
		fi
	} >&2
	exit "$rc"
}
trap on_err ERR

STEP=resolve
tag=""
if [ "$USE_GH" = 1 ]; then
	tag=$(trap - ERR; gh release view --repo "$REPO" --json tagName --jq .tagName 2>/dev/null) || tag=""
else
	url=$(trap - ERR; curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null) || url=""
	case "$url" in
	*/releases/tag/*) tag="${url##*/}" ;;
	esac
fi
VERSION="${tag:-最新版}"
step "解析版本：${VERSION}"

STEP=prepare
mkdir -p "$BIN_DIR"
tmpdir=$(trap - ERR; mktemp -d "$BIN_DIR/.${COMMAND}.download.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT

STEP=download
step "下載 ${ASSET}（${VERSION}）"
if [ "$USE_GH" = 1 ]; then
	gh release download --repo "$REPO" --pattern "$ASSET" --output "$tmpdir/$ASSET" --clobber
	gh release download --repo "$REPO" --pattern "SHA256SUMS" --output "$tmpdir/SHA256SUMS" --clobber
else
	BASE_URL="https://github.com/$REPO/releases/latest/download"
	curl -fsSL "$BASE_URL/$ASSET" -o "$tmpdir/$ASSET"
	curl -fsSL "$BASE_URL/SHA256SUMS" -o "$tmpdir/SHA256SUMS"
fi

STEP=verify
step "驗證 SHA256"
# bash 3.2 skips the parent ERR trap for a subshell that cleared its own,
# so let the subshell fail plainly and report once through `|| false`.
(cd "$tmpdir" && grep " $ASSET\$" SHA256SUMS | shasum -a 256 -c - >/dev/null) || false
chmod +x "$tmpdir/$ASSET"

STEP=backup
if [ -e "$DEST" ] && [ ! -L "$DEST" ]; then
	bak="$DEST.bak-$(date +%Y%m%d-%H%M%S)"
	mv "$DEST" "$bak"
	backup="$bak"
	step "備份舊指令 → ${backup}"
fi

STEP=install
step "安裝到 $DEST"
mv "$tmpdir/$ASSET" "$DEST"
STEP=""
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
