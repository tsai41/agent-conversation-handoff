# Agent Conversation Handoff

[![Release](https://img.shields.io/github/v/release/tsai41/agent-conversation-handoff)](https://github.com/tsai41/agent-conversation-handoff/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

在 Claude Code 與 Codex 的多個帳號之間安全交接對話。

這是 handoff，不是跨帳號或跨工具 `resume`：來源 session 永遠只讀，目標帳號會開啟新對話並讀取完整的本機交接資料。

## 功能

- 支援任意數量的 Claude 與 Codex 帳號。
- 從一個 `ccs` 選單直接啟動任一帳號。
- 在目前專案內挑選來源對話，顯示對話建立時間、截斷 id 與第一句需求。
- Claude ↔ Claude、Claude ↔ Codex、Codex ↔ Codex 均使用同一套安全交接流程。
- 交接前使用 provider 官方 CLI 驗證目標帳號登入狀態。
- alias 僅供選單辨識，不接觸 token、Keychain 或認證內容。

## 前置需求

- `fzf`
- `gh`（GitHub CLI，已 `gh auth login`）——此 repo 為 private，安裝時需要用 gh 認證抓 release
- Claude Code CLI（`claude`）或 Codex CLI（`codex`）至少一個
- macOS（arm64 或 amd64）

不需要 Go、不需要 python3——安裝的是預先編譯好的單一執行檔。

macOS 可用 Homebrew 安裝：

```bash
brew install fzf gh
gh auth login
```

## 安裝

不需要 clone 整個 repo，直接抓 `install.sh` 執行即可：

```bash
gh api repos/tsai41/agent-conversation-handoff/contents/install.sh -H "Accept: application/vnd.github.raw" | bash
```

會依本機 arch 從最新的 GitHub Release 下載對應的 `ach` 二進位到 `~/bin/ccs`。若要改指令名稱或安裝路徑：

```bash
COMMAND=myalias BIN_DIR=~/bin gh api repos/tsai41/agent-conversation-handoff/contents/install.sh -H "Accept: application/vnd.github.raw" | bash
```

若 `~/bin` 已有同名指令，安裝時會先建立時間戳備份。

只有要維護這個 repo（改程式、發新版）才需要 clone 並安裝 Go 工具鏈：

```bash
git clone git@github.com:tsai41/agent-conversation-handoff.git ~/go/src/agent-conversation-handoff
cd ~/go/src/agent-conversation-handoff
make install COMMAND=ccs
```

## 第一次執行

在任意專案目錄執行：

```bash
ccs
```

第一次執行會偵測常見既有帳號目錄，例如：

```text
~/.claude
~/.claude-2
~/.claude-3
~/.codex
~/.codex-2
~/.codex-3
```

舊式 `~/.codex-homes/*` 只會列為可匯入候選。每個候選都必須由你確認後才會加入 registry；不會看到目錄就擅自當成帳號。匯入時可選填 alias。

首次匯入會在所有候選都確認完成後才一次寫入 registry。若中途取消，不會留下空白或只完成一半的 registry；下次執行會重新開始確認。

## 主選單

單一 provider 帳號只顯示 provider 名稱；每個項目前面有數字，按對應數字鍵直接選（不用按 Enter），最多支援 9 個快捷鍵：

```text
1. Claude
2. Codex
3. 接手既有對話
4. 帳號設定
```

同一 provider 有多個帳號時，顯示穩定編號；alias 有設定才附加：

```text
1. Claude · 1 · personal@example.com
2. Claude · 2 · work@example.com
3. Codex · 1
4. Codex · 2 · work
5. 接手既有對話
6. 帳號設定
```

取消登記後，其他帳號不會重新編號；新帳號也不會重用已用過的編號。

## 帳號設定

`帳號設定` 提供：

- 新增帳號：選擇第一個尚未存在、也未登記的標準 home，然後啟動 provider 官方登入流程。home 後綴與選單中的穩定帳號編號彼此獨立。
- 修改 alias：alias 選填，只影響選單顯示。
- 登入／重新登入：以選定帳號的 home 執行官方登入指令。
- 從 ccs 移除帳號：只取消 registry 登記，絕不刪除帳號目錄、session 或認證資料。
- 匯入既有帳號目錄：只匯入你明確選擇的候選。

帳號 registry 位於：

```text
~/.config/agent-conversation-handoff/accounts.json
```

registry 使用跨程序鎖與原子寫入，避免同時開啟多個 `ccs` 時互相覆蓋帳號異動。讀取時會驗證版本、必要欄位、帳號唯一性與下一個穩定編號；若 JSON 或結構損壞，工具會保留原檔、建立 `accounts.json.corrupt-<時間>` 備份並停止，不會自行覆寫重建。

## 接手既有對話

選擇 `接手既有對話` 後：

1. 選擇接力方向（例如「Claude · 1」→「Codex」），只列出目前專案有歷史紀錄、且目標帳號 CLI 已安裝的組合。
2. 依時間、id（截斷顯示，避免長 UUID 擠掉預覽文字）與第一句需求選擇來源對話。
3. 使用 `claude auth status` 或 `codex login status` 驗證目標登入狀態（每個帳號快取 5 分鐘，避免重複交接時每次都要等網路來回）。
4. 驗證通過後才建立 handoff artifact。
5. 使用目標帳號開啟新對話並讀取 artifact。

若目標未登入，流程會在建立 artifact 前停止並提示先登入。同一帳號不能同時作為來源與目標。

## Handoff artifact

交接資料建立在目前專案內：

```text
.agent-handoffs/<UTC 時間>-<來源 SHA-256 前綴>/
├── manifest.json
├── source.jsonl
└── transcript.md
```

- `source.jsonl`：來源 session 的逐位元快照，保留未知事件與 `bridge-session`。
- `transcript.md`：供目標 agent 閱讀的 user／assistant 文字對話。
- `manifest.json`：來源類型、大小、SHA-256、建立時間與 artifact 校驗值。

`.agent-handoffs` 權限為 `0700`，內容檔案為 `0600`，並由目錄內的 `.gitignore` 排除，不會出現在專案的 `git status`。

## 安全邊界

- 不執行跨帳號或跨工具 `resume`。
- 不重用來源 session UUID。
- 不刪除 `bridge-session`。
- 不寫入或覆寫 provider 的 session 目錄。
- 不讀取或搬移 token、Keychain 與認證內容。
- source hash 在 artifact 發布前會再次驗證。
- artifact 先在私有暫存目錄完成，成功後才原子發布。
- 建立失敗只清除暫存資料，來源 session 與既有 artifact 都保留。

## 測試

```bash
make test
```
