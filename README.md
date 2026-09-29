# Agent Conversation Handoff

[![Release](https://img.shields.io/github/v/release/tsai41/agent-conversation-handoff)](https://github.com/tsai41/agent-conversation-handoff/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**English summary.** `ach` is a macOS terminal tool for people who keep several Claude Code and Codex subscription accounts. When one account runs out of quota, it hands the current conversation over to another account or to the other tool, so you can keep going in a new session. The source session is only read, never modified. The menu UI is Traditional Chinese only, and only macOS (arm64 and amd64) is supported.

```bash
curl -fsSL https://raw.githubusercontent.com/tsai41/agent-conversation-handoff/main/install.sh | bash
```

適用情境：你有多個 Claude Code 或 Codex 訂閱帳號，其中一個額度用完時，把目前的對話交給另一個帳號或另一個工具，接著做下去。選單介面只有繁體中文，只支援 macOS。

這是 handoff，不是跨帳號或跨工具 `resume`：來源 session 永遠只讀，目標帳號會開啟新對話並讀取完整的本機交接資料（handoff artifact）。

輪替多個訂閱帳號是否符合各 provider 的服務條款，由使用者自行負責。

## 功能

- 支援任意數量的 Claude 與 Codex 帳號。
- 從一個 `ach` 選單直接啟動任一帳號，啟動前會先印出是哪個帳號（編號、alias、home）。
- 在目前專案內挑選來源對話，顯示對話建立時間、截斷的對話 ID 與第一句需求；知道對話 ID 的話也可以直接打進搜尋框。
- 新增帳號時可選擇與既有帳號共用同一份設定檔，之後改一次就全部生效。
- Claude ↔ Claude、Claude ↔ Codex、Codex ↔ Codex 均使用同一套交接流程。
- 交接前使用 provider 官方 CLI 驗證目標帳號登入狀態。
- alias 僅供選單辨識，不接觸 token、Keychain 或認證內容。

## 前置需求

- macOS（arm64 或 amd64）。不支援 Linux，`install.sh` 在非 macOS 上會直接拒絕。
- zsh。provider 一律透過 login zsh（`zsh -lic`）啟動，所以 `~/.zshrc` 裡替 `claude`、`codex` 設的 wrapper 或環境變數都會生效。只有 `CLAUDE_CONFIG_DIR` 與 `CODEX_HOME` 例外：ach 會在 `~/.zshrc` 跑完之後，依選定的帳號重新設定。
- `fzf`
- `curl`（macOS 內建）；若已安裝並登入 `gh`，安裝腳本會改用 gh 下載 release（選用）
- Claude Code CLI（`claude`）或 Codex CLI（`codex`）至少一個

不需要 Go、不需要 python3：安裝的是預先編譯好的單一執行檔。

macOS 可用 Homebrew 安裝：

```bash
brew install fzf
```

## 安裝

不需要 clone 整個 repo，直接抓 `install.sh` 執行即可：

```bash
curl -fsSL https://raw.githubusercontent.com/tsai41/agent-conversation-handoff/main/install.sh | bash
```

會依本機 arch 從最新的 GitHub Release 下載對應的 `ach` 二進位到 `~/bin/ach`，並用 release 附的 `SHA256SUMS` 驗證。過程每個步驟印一行，並標出實際安裝的版本；失敗時會說明卡在哪一步，以及沒有安裝任何東西。若要改安裝路徑，或用別的名字安裝（`COMMAND=<name>`）：

```bash
curl -fsSL https://raw.githubusercontent.com/tsai41/agent-conversation-handoff/main/install.sh | COMMAND=myname BIN_DIR=~/bin bash
```

若目標路徑已有同名檔案，安裝時會先備份成 `ach.bak-<日期>-<時間>`。例外：目標若是 symlink，會直接被取代，不備份。

macOS 預設的 PATH 不含 `~/bin`。安裝完若執行 `ach` 顯示 command not found，把它加進 PATH 並重新開啟終端（`install.sh` 偵測到 PATH 缺少時也會印出這行）：

```bash
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc
```

從原始碼建置（需要 Go）：

```bash
git clone https://github.com/tsai41/agent-conversation-handoff.git
cd agent-conversation-handoff
make build
```

`make build` 會在 `dist/` 產出 `ach-darwin-arm64` 與 `ach-darwin-amd64`。只要本機這一種 arch，也可以 `go build -o ~/bin/ach ./cmd/ach`。注意 `make install` 不會建置，它跟上面的 `install.sh` 一樣是下載最新 release。

## 第一次執行

在任意專案目錄執行：

```bash
ach
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

每個候選都必須由你確認後才會加入 registry，匯入時可選填 alias。

首次匯入會在所有候選都確認完成後才一次寫入 registry。若中途取消，不會留下空白或只完成一半的 registry；下次執行會重新開始確認。

若沒有偵測到任何既有帳號目錄，registry 會建成空的。進入主選單後，先到 `帳號設定` > `新增帳號` 建立第一個帳號。

## 主選單

主選單只問要做什麼，選完功能才選帳號。選單項目前面都有數字，按對應數字鍵直接選（不用按 Enter），最多支援 9 個快捷鍵。對話清單沒有數字快捷鍵，用搜尋框或方向鍵選。畫面上的文字如下，實際排版由 fzf 決定：

```text
主選單
1. 使用帳號對話
2. 接手對話
3. 帳號設定
4. 查看用量
選擇功能:
```

選 `使用帳號對話` 之後才列出帳號。每個 provider 只有一個帳號時，只顯示 provider 名稱，即使設了 alias 也不會顯示：

```text
主選單 > 使用帳號對話
1. Claude
2. Codex
選擇帳號:
```

同一 provider 有多個帳號時，顯示穩定編號；alias 有設定才附加：

```text
主選單 > 使用帳號對話
1. Claude · 1 · personal@example.com
2. Claude · 2 · work@example.com
3. Codex · 1
4. Codex · 2 · work
選擇帳號:
```

選定帳號後可選擇「新開對話」或「繼續既有對話」。後者會列出該帳號在目前專案的最近對話，也可直接輸入對話 ID；選定後以 provider 官方的 resume 指令開啟同一個 session。

按 ESC 會退回目前這個功能所屬的選單，不會直接離開：接手對話精靈是一次退一步（例如從選擇目標帳號退回選擇來源對話）；在帳號設定裡，確認畫面或子選單按 ESC 會退回帳號設定的動作選單，帳號設定本身按 ESC 則退回主選單；在主選單按 ESC 才會結束程式。

### 路徑列

每個選單上方都有一條路徑列，標示現在在哪一層：

```text
主選單 > 接手對話 > 來源 Agent：Claude · 1 > 選擇目標 Agent
```

需要用鍵盤打字的地方也會先印出同一條路徑。設定 alias 與用量資料目錄時都適用。

## 接手對話

選擇 `接手對話` 後：

1. 選擇來源帳號。所有已登記帳號都會列出，因為接手只讀來源帳號的檔案，不需要它的 CLI。
2. 依時間、對話 ID（截斷顯示，避免長 UUID 擠掉預覽文字）與第一句需求選擇來源對話。已知對話 ID 的做法見「直接輸入對話 ID」。
3. 選擇目標帳號。排除來源帳號本身，只列出 CLI 已安裝的。
4. 使用 `claude auth status` 或 `codex login status` 驗證目標登入狀態（每個帳號快取 5 分鐘，避免重複交接時每次都要等網路來回）。
5. 驗證通過後才建立交接資料。
6. 使用目標帳號開啟新對話並讀取交接資料。

若目標未登入，流程會在建立交接資料前停止並提示先登入。同一帳號不能同時作為來源與目標。

### 快捷模式

已經知道 Claude 對話 ID 時，可以跳過選單：

```bash
ach h bbbbbbbb
```

前提：

- 已經執行過一次 `ach`，registry 已存在，否則會回報 registry 不存在。
- 已登記至少一個 Codex 帳號，且它的 CLI 已安裝。快捷模式固定是 Claude → Codex：只搜尋已登記的 Claude 帳號，並交給 Codex 帳號；只有多個 Codex 帳號可用時才會顯示目標選單。其他方向請用選單。

`quick-handoff` 是同一功能的完整別名。

### 直接輸入對話 ID

對話清單只列目前專案、且只留最新 5 筆，所以你知道對話 ID 的那個對話常常不在清單裡。第 2 步直接在搜尋框把對話 ID 打進去就能指定，不用先選任何一列。

- 輸入對話 ID 的任一片段（例如 `bbbbbbbb`）即可，至少 4 碼，不分大小寫；多筆符合會依時間新到舊再讓你選一次。
- 會在所有已登記帳號裡找，來源帳號以實際找到的位置為準，不受第 1 步選的來源限制；目標帳號在來源確定之後才選。
- 不限專案。對話若是在別的目錄開的，會先印出原本的目錄提醒你，交接資料仍建立在目前專案。
- 專案完全沒有對話紀錄時一樣走得到。

## 交接資料

交接資料（handoff artifact）建立在目前專案內：

```text
.agent-handoffs/<UTC 時間>-<來源 SHA-256 前綴>/
├── manifest.json
├── source.jsonl
└── transcript.md
```

- `source.jsonl`：來源 session 的逐位元快照，保留未知事件與 `bridge-session`。
- `transcript.md`：供目標 agent 閱讀的 user／assistant 文字對話。
- `manifest.json`：來源類型、大小、SHA-256、建立時間與交接資料校驗值。

`.agent-handoffs` 權限為 `0700`，內容檔案為 `0600`，並由目錄內的 `.gitignore` 排除，不會出現在專案的 `git status`。

## 帳號設定

`帳號設定` 提供：

- 新增帳號：依序檢查 `~/.claude`、`~/.claude-2`、`~/.claude-3`⋯（Codex 是 `~/.codex`、`~/.codex-2`⋯），用第一個目錄還不存在、也沒登記過的當作新帳號的 home，然後啟動 provider 官方登入流程。home 後綴與選單中的穩定帳號編號彼此獨立。建立完會問要不要與第一個同 provider 帳號共用設定與能力（見下面「共用設定」一節），選了就逐項接 symlink，不選則留給 provider CLI 自己產生。
- 修改 alias：alias 選填，只影響選單顯示。
- 登入／重新登入：以選定帳號的 home 執行官方登入指令。
- 共用設定到所有帳號：見下面「共用設定」一節。
- 封存／解除封存帳號：見下面「封存帳號」一節。
- 從 ach 移除帳號：只取消 registry 登記，絕不刪除帳號目錄、session 或認證資料。取消登記後，其他帳號不會重新編號；新帳號也不會重用已用過的編號。
- 匯入既有帳號目錄：只匯入你明確選擇的候選。
- 設定用量資料目錄：見 [docs/usage-snapshot.md](docs/usage-snapshot.md)，設定後存在 registry 裡，不用每次都帶 `--usage-dir`。留空即清除設定，改用預設路徑。
- 同步專案信任到其他帳號：見 [docs/shared-settings.md](docs/shared-settings.md)。


### 封存帳號

不再常用、但想留著對話紀錄的帳號可以封存：它在所有清單裡（選帳號、接手來源與目標、查看用量）排到最後，標籤加上「（已封存）」（查看用量的表格改用 `狀態` 欄標示）。封存只改 `accounts.json` 裡的一個欄位，帳號目錄、認證與對話紀錄都不動，帳號編號也不變。封存後照樣能選、能繼續對話、能用 ID 搜尋，也能當接手的目標。`ach accounts list` 同樣把封存帳號列在最後並加上這個標籤。

到 `帳號設定` > `封存／解除封存帳號`，選帳號即切換狀態；要還原就對同一個帳號再做一次。命令列：

```sh
ach accounts archive --registry <路徑> --id claude-2
ach accounts unarchive --registry <路徑> --id claude-2
```

`ach accounts` 底下的子指令都要帶 `--registry`，沒有預設路徑。

### 共用設定

同一個 provider 的帳號可以共用設定與能力：`ach` 讓每個帳號的 `settings.json`、`skills/` 等項目成為指向第一個同 provider 帳號的 symlink，改一次全部生效。「第一個」指的是編號最小的帳號；封存不影響這個判斷，封存了編號最小的帳號，其他帳號仍然連到它。身分（token、Keychain）與對話（session、專案記錄）永遠各帳號獨立。

`同步專案信任到其他帳號` 則是另一件事：`.claude.json` 混著身分，不能用 symlink，只能把專案信任相關的白名單欄位一次性合併過去；跑之前請先關閉目標帳號的 Claude Code session。

哪些項目共用、既有檔案怎麼處理、備份與鎖的細節，見 [docs/shared-settings.md](docs/shared-settings.md)。

## 查看用量

`查看用量` 列出每個已登記帳號的 Claude 用量（5 小時與 7 天）。資料來自寫在磁碟上的 snapshot 檔，所以要先有寫入端，最簡單的是 `ach usage record`：在 Claude Code 的 [status line](https://code.claude.com/docs/en/statusline) 腳本裡，把 stdin 餵一份給它。

```bash
input=$(cat); printf '%s' "$input" | ~/bin/ach usage record
```

- 每個 Claude 帳號的 status line 都要有這一行。status line 設定在 `settings.json` 裡，帳號之間共用 `settings.json` 時（見〈共用設定〉）只要加一次。
- `ach usage record` 依 status line 行程的 `CLAUDE_CONFIG_DIR` 判斷是哪個帳號，沒設時算成 `~/.claude`。
- Codex 帳號也會列出，但不提供用量，兩個用量欄顯示 `不支援`，`更新` 欄顯示 `–`。
- `狀態` 欄只在有帳號已封存時才出現，沒有封存帳號時整欄不顯示。
- 每個視窗顯示百分比與多久後重置（例如 `45%  2 小時後重置`），重置時間已過就顯示 `已重置`；`更新` 欄是資料的新舊（`剛剛`、`3 小時前`），沒有資料時顯示 `沒有資料`。封存的帳號在 `狀態` 欄標 `已封存`。終端機寬度放不下表格時，改成每個帳號一組、逐行列出。
- status line 是由 Claude Code 啟動的子行程，它的 PATH 不一定含 `~/bin`，所以這裡寫絕對路徑。若你用 `COMMAND` 或 `BIN_DIR` 改過安裝位置，請換成實際路徑。

`rate_limits` 只有 Claude Pro／Max 訂閱帳號才有。目錄、檔案格式、多份 snapshot 的選擇規則見 [docs/usage-snapshot.md](docs/usage-snapshot.md)。

## 設定檔位置

帳號 registry 預設在 `~/.config/agent-conversation-handoff/accounts.json`，用量 snapshot 預設在同目錄的 `usage/`。設環境變數 `ACH_CONFIG_DIR` 可整個改放別處；`ach menu`（與直接執行 `ach` 相同）、`ach h`、`ach usage record` 與 `ach uninstall` 也接受 `--registry <路徑>`，`ach menu` 與 `ach usage record` 另有 `--usage-dir <目錄>`，優先於環境變數。

registry 寫入使用跨行程鎖與原子寫入，同時開多個 `ach` 不會互相覆蓋帳號異動。讀取時若 JSON 或結構損壞，工具會保留原檔、建立 `accounts.json.corrupt-<時間>` 備份並停止，不會自行覆寫重建。

## 安全邊界

- 不執行跨帳號或跨工具 `resume`。
- 不重用來源 session UUID。
- 不刪除 `bridge-session`。
- 不寫入或覆寫 provider 的 session 目錄。
- 不讀取或搬移 token、Keychain 與認證內容。
- 來源的 SHA-256 在交接資料發布前會再次驗證。
- 交接資料先在私有暫存目錄完成，成功後才原子發布。
- 建立失敗只清除暫存資料，來源 session 與既有交接資料都保留。

## 隱私

`source.jsonl` 與 `transcript.md` 是整段對話的完整副本，裡面可能有貼過的密鑰或敏感內容。它們會一直留在該專案的 `.agent-handoffs/`，直到你手動刪除。目錄內的 `.gitignore` 只擋 git，擋不住雲端同步工具，也擋不住 Docker build context。交接給另一個組織的帳號，就是把這段對話的內容分享給該組織。

## 解除安裝

先預覽，不會改動任何檔案：

```bash
ach uninstall
```

確認清單無誤後，先關掉其他正在跑的 ach，再執行：

```bash
ach uninstall --yes
```

預覽結尾與失敗後提示的重跑指令，會用你實際執行的指令名稱；有帶 `--registry` 時也會照樣帶上，直接複製就能用。

### 拒絕執行

下列情況直接停止，任何檔案都不動（包括共用連結與鎖定檔），以非零狀態結束：

- `--registry` 的檔名不是 `accounts.json`。
- registry 所在的設定目錄是家目錄、家目錄的上層，或（registry 讀得到時）裡面有任何帳號目錄。

### 處理順序

通過檢查後依序執行四個步驟，逐項印出結果。單項失敗會回報並繼續處理其餘項目，有失敗時以非零狀態結束。

1. 解除共用設定：第一個帳號以外的帳號裡，指向第一個同 provider 帳號的 symlink 換成實體複本。上次執行中斷留下的 `<名稱>.uninstall-link` 與 `.<名稱>.uninstall-*` 會先放回或清掉。
2. 列出共用前的原始設定備份。
3. 清理設定目錄（預設 `~/.config/agent-conversation-handoff/`，或 `ACH_CONFIG_DIR` 指定的目錄）。
4. 刪除 `ach` 執行檔。

registry 不存在時，步驟 1、2 沒有帳號可處理，直接略過。

### 會刪除

- 共用連結：換成實體複本。目錄保留原本的檔案權限，目錄裡的相對連結改成指向原本那個檔案的絕對路徑。
- 設定目錄裡 ach 自己的檔案：`accounts.json`、`accounts.json.lock`、`accounts.json.corrupt-*`、`auth-cache.json`，以及預設位置的 `usage/`。目錄清空後才刪掉目錄本身。
- 執行檔：以你呼叫它的路徑為準，連同同目錄的 `<指令名>.bak-<時間>`。後者是 `install.sh` 覆蓋安裝時留下的舊版執行檔。那個路徑是 symlink 時只刪連結。

### 只列出，不刪除

下列項目印在結尾的「仍需手動處理」，確認不需要後自行刪除：

- 共用前的原始設定備份：共用設定留下的 `<名稱>.bak-<時間>`，與同步專案信任留下的 `.claude.json.bak-*`（`~/.claude` 帳號在 `~/.claude.json.bak-*`）。這些是各帳號在共用前自己的設定，ach 不會刪。
- 第一個帳號沒有對應項目的共用連結，以及名稱不是共用項目的 `.<名稱>.uninstall-*`。
- `usage/`：`設定用量資料目錄` 指到設定目錄以外、或 `usage/` 裡有快照以外的東西時。
- registry 保留時，一併保留的 `accounts.json.corrupt-*`。
- 設定目錄裡 ach 以外的東西；有這些東西時目錄本身也保留。
- 執行檔是 symlink 時它指向的檔案；呼叫的路徑與實際在跑的執行檔不是同一個檔案時的兩個路徑；執行檔所在目錄不可寫入時的執行檔。

指向別處（不是第一個帳號）的 symlink、第一個帳號本身、`go run` 的暫存建置都不動。registry 不存在時也不刪 `usage/`。

### registry 保留時

下列情況 registry 與執行檔都會保留，好讓帳號資料還找得到：

- 步驟 1 有項目失敗。
- registry 讀不了或刪不掉。
- 無法鎖定 registry。

修正問題後重跑同一個指令，已完成的項目會略過。

### 不會動、要自己處理

不會動：帳號目錄本身、認證資料、session、各專案的 `.agent-handoffs/`，以及你的 status line。以下要自己處理：

- 刪除交接資料：`ach uninstall` 結束時會印出 `find` 指令，用來找出各專案底下的 `.agent-handoffs/`。
- 把 `ach usage record` 從 Claude Code 的 status line 腳本拿掉（沒設定過就跳過）。
- 只在 status line 用 `ach usage record --usage-dir` 指定、沒有存進 registry 的用量資料目錄，`ach uninstall` 不知道它在哪，不會刪也不會列出。
- `設定用量資料目錄` 指到設定目錄裡、但不是預設位置 `usage/` 的目錄：不會刪，也不會列在結尾的「仍需手動處理」，只在步驟 3 的輸出提到；它留在設定目錄裡，所以設定目錄本身也保留。要自己刪除。
- 曾經用 `從 ach 移除帳號` 取消登記編號最小的帳號時，共用來源會變成剩下帳號裡編號最小的那個，但其他帳號原本的連結仍指向被取消登記的帳號。`ach uninstall` 把這些連結當成指向別處，保持原狀，要自己換成實體檔。

### 沒有可用的 `ach` 時

先解除共用設定：找出指向第一個帳號的 symlink，刪掉後從來源複製實體檔案，目錄用 `cp -R`。`cp -R` 會把目錄裡的相對 symlink 原樣複製，它們在新位置可能指錯，要逐一檢查修正。不要先刪第一個帳號的 home。接著刪 `~/bin/ach`、`~/bin/ach.bak-*`，以及設定目錄裡上面列出的檔案。

## 開發

- Go 1.26.5 以上（見 `go.mod`）。
- `make build`：建置 `dist/ach-darwin-<arch>`。
- `go test ./...`：跑全部測試（`make test` 是同一件事）。

`skills/` 是維護者給 coding agent 用的開發、發版與安裝流程，使用工具不需要讀。

## 安全性回報

發現安全問題請用 GitHub 的 [Security Advisories](https://github.com/tsai41/agent-conversation-handoff/security/advisories/new)（private vulnerability reporting）私下回報，不要開公開 issue。

## 授權

MIT，見 [LICENSE](LICENSE)。
