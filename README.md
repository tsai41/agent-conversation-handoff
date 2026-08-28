# Agent Conversation Handoff

[![Release](https://img.shields.io/github/v/release/tsai41/agent-conversation-handoff)](https://github.com/tsai41/agent-conversation-handoff/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

在 Claude Code 與 Codex 的多個帳號之間安全交接對話。

這是 handoff，不是跨帳號或跨工具 `resume`：來源 session 永遠只讀，目標帳號會開啟新對話並讀取完整的本機交接資料。

## 功能

- 支援任意數量的 Claude 與 Codex 帳號。
- 從一個 `ccs` 選單直接啟動任一帳號：先選功能，再選帳號，每層都有數字快捷鍵與路徑列。
- 在目前專案內挑選來源對話，顯示對話建立時間、截斷 id 與第一句需求；知道 id 的話也可以直接打進搜尋框。
- 新增帳號時可選擇與既有帳號共用同一份設定檔，之後改一次就全部生效。
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

如果已經知道 Claude 對話 ID，推薦直接使用快捷模式：

```bash
ccs h 019fcb8e
```

快捷模式會自動搜尋所有已登記的 Claude 帳號，並交給已安裝的 Codex 帳號；只有多個 Codex 帳號可用時才會顯示目標選單。`quick-handoff` 是同一功能的完整別名。

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

主選單只問要做什麼，選完功能才選帳號。每一層的項目前面都有數字，按對應數字鍵直接選（不用按 Enter），最多支援 9 個快捷鍵：

```text
1. 使用帳號對話
2. 接手對話
3. 帳號設定
```

選 `使用帳號對話` 之後才列出帳號。單一 provider 帳號只顯示 provider 名稱：

```text
1. Claude
2. Codex
```

同一 provider 有多個帳號時，顯示穩定編號；alias 有設定才附加：

```text
1. Claude · 1 · personal@example.com
2. Claude · 2 · work@example.com
3. Codex · 1
4. Codex · 2 · work
```

取消登記後，其他帳號不會重新編號；新帳號也不會重用已用過的編號。

在第二層按 ESC 會退回主選單，不會直接離開。

## 路徑列

每個選單上方都有一條路徑列，標示現在在哪一層：

```text
主選單 > 接手對話 > Claude · 1
```

需要用鍵盤打字的地方也會先印出同一條路徑。設定 alias 與輸入對話編號時都適用。

## 帳號設定

`帳號設定` 提供：

- 新增帳號：選擇第一個尚未存在、也未登記的標準 home，然後啟動 provider 官方登入流程。home 後綴與選單中的穩定帳號編號彼此獨立。建立完會問要不要與第一個同 provider 帳號共用設定，選了就接 symlink，不選則留給 provider CLI 自己產生。
- 修改 alias：alias 選填，只影響選單顯示。
- 登入／重新登入：以選定帳號的 home 執行官方登入指令。
- 共用設定到所有帳號：見下節。
- 從 ccs 移除帳號：只取消 registry 登記，絕不刪除帳號目錄、session 或認證資料。
- 匯入既有帳號目錄：只匯入你明確選擇的候選。

### 共用設定

同一個 provider 的帳號通常只差在登入的是哪個 token，設定本身希望一致。`ccs` 的做法是讓每個帳號的設定檔都是指向第一個同 provider 帳號的 symlink，所以改一次就全部生效，不需要事後同步。

| provider | 共用的檔案 | 不共用（各帳號獨立） |
|---|---|---|
| Claude | `settings.json` | `.credentials.json`、`.claude.json`、Keychain |
| Codex | `config.toml` | `auth.json` |

憑證都不在共用的那個檔案裡，所以共用設定不會讓帳號互相踩到登入狀態。

`共用設定到所有帳號` 會把每個非第一順位的帳號都接過去，也是連結被弄斷之後的修復手段。各種情況的處置：

| 帳號目前的設定檔 | 處置 |
|---|---|
| 沒有 | 直接接上 |
| 已經指向來源 | 略過 |
| 自己一份，內容與來源不同 | 搬到 `<檔名>.bak-<時間>` 再接，並印出備份路徑 |
| 自己一份，內容與來源相同 | 直接接上，不留備份（那份備份保存不了任何東西） |
| 指向第三個地方的 symlink | 拒絕改動並回報 |
| 是個目錄 | 拒絕改動並回報 |

備份不會互相覆蓋：同一秒內再接一次會換一個不重複的檔名。接的過程若在中途失敗，會把原檔搬回去，不會留下一個沒有設定檔的帳號。

兩點要知道：

- **有東西以「寫暫存檔再 rename」的方式存檔時，symlink 會被換成實體檔**，該帳號就靜默地不再共用。這種狀態跟「從來沒共用過」在檔案上看起來一樣，工具分不出來——重跑一次 `共用設定到所有帳號` 就能接回去。
- **共用的是同一個實體檔案，所以 `chmod` 也是共用的。** 設定檔裡若有密鑰之類的東西，記得把權限收成 `600`。

帳號 registry 位於：

```text
~/.config/agent-conversation-handoff/accounts.json
```

registry 使用跨程序鎖與原子寫入，避免同時開啟多個 `ccs` 時互相覆蓋帳號異動。讀取時會驗證版本、必要欄位、帳號唯一性與下一個穩定編號；若 JSON 或結構損壞，工具會保留原檔、建立 `accounts.json.corrupt-<時間>` 備份並停止，不會自行覆寫重建。

## 接手對話

選擇 `接手對話` 後：

1. 選擇來源帳號。所有已登記帳號都會列出，因為接手只讀來源帳號的檔案，不需要它的 CLI。
2. 依時間、id（截斷顯示，避免長 UUID 擠掉預覽文字）與第一句需求選擇來源對話。也可以直接在搜尋框輸入對話 ID，不必先選任何一列。
3. 選擇接手帳號。排除來源帳號本身，只列出 CLI 已安裝的。
4. 使用 `claude auth status` 或 `codex login status` 驗證目標登入狀態（每個帳號快取 5 分鐘，避免重複交接時每次都要等網路來回）。
5. 驗證通過後才建立 handoff artifact。
6. 使用目標帳號開啟新對話並讀取 artifact。

若目標未登入，流程會在建立 artifact 前停止並提示先登入。同一帳號不能同時作為來源與目標。

### 直接輸入對話 ID

對話清單只列目前專案、且只留最新 5 筆，所以你知道 id 的那個對話常常不在清單裡。第 2 步直接在搜尋框把 id 打進去就能指定，不用先選任何一列。清單最後那一列「✎ 輸入對話 ID」走的是同一條路徑，留著只是讓人知道有這個用法。

- 可以只打前綴（例如 `019fcb8e`），大小寫不拘，至少 4 碼；多筆符合會依時間新到舊再讓你選一次。
- 會在所有已登記帳號裡找，來源帳號以實際找到的位置為準，不受第 1 步選的來源限制；接手帳號在來源確定之後才選。
- 不限專案。對話若是在別的目錄開的，會先印出原本的目錄提醒你，交接資料仍建立在目前專案。
- 專案完全沒有對話紀錄時一樣走得到。

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
