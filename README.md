# Agent Conversation Handoff

[![Release](https://img.shields.io/github/v/release/tsai41/agent-conversation-handoff)](https://github.com/tsai41/agent-conversation-handoff/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

在 Claude Code 與 Codex 的多個帳號之間安全交接對話。

這是 handoff，不是跨帳號或跨工具 `resume`：來源 session 永遠只讀，目標帳號會開啟新對話並讀取完整的本機交接資料。

## 功能

- 支援任意數量的 Claude 與 Codex 帳號。
- 從一個 `ccs` 選單直接啟動任一帳號：先選功能，再選帳號，每層都有數字快捷鍵與路徑列。啟動前會先印一行是哪個帳號（編號、alias、home），session 結束後往上捲還看得到。
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
4. 查看用量
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

- 新增帳號：選擇第一個尚未存在、也未登記的標準 home，然後啟動 provider 官方登入流程。home 後綴與選單中的穩定帳號編號彼此獨立。建立完會問要不要與第一個同 provider 帳號共用設定與能力（見下節），選了就逐項接 symlink，不選則留給 provider CLI 自己產生。
- 修改 alias：alias 選填，只影響選單顯示。
- 登入／重新登入：以選定帳號的 home 執行官方登入指令。
- 共用設定到所有帳號：見下節。
- 從 ccs 移除帳號：只取消 registry 登記，絕不刪除帳號目錄、session 或認證資料。
- 匯入既有帳號目錄：只匯入你明確選擇的候選。
- 設定用量資料目錄：見下面「查看用量」一節，設定後存在 registry 裡，不用每次都帶 `--usage-dir`。留空即清除設定，改用預設路徑。
- 狀態列設定：見下面「查看用量」一節「讓 status line 寫入 snapshot」小節。
- 同步專案信任到其他帳號：見下面「同步專案信任到其他帳號」一節。

### 共用設定

同一個 provider 的帳號通常只差在登入的是哪個 token，設定與能力本身希望一致。`ccs` 的做法是讓每個帳號的這些項目都是指向第一個同 provider 帳號的 symlink，所以改一次就全部生效，不需要事後同步 —— 沒有複製，也沒有「同步時機」。

需要各自獨立的只有兩類：身分（登入的是誰）與對話（session、專案記錄）。其餘的設定、權限、skill、agent 都共用。

| provider | 共用的項目 | 不共用（各帳號獨立） |
|---|---|---|
| Claude | `settings.json`、`settings.local.json`、`skills/`、`commands/`、`agents/`、`plugins/` | `.credentials.json`、`.claude.json`、Keychain、`projects/`、`sessions/` |
| Codex | `config.toml`、`skills/` | `auth.json`、`sessions/` |

來源帳號沒有的項目就沒有東西可共用，會跳過。目錄項目連結的是整個目錄，不是逐檔比對。

`plugins/` 共用後，每個 plugin 在所有帳號只會有一個安裝版本（裝在哪個帳號都一樣）。Claude Code 對這個目錄裡的安裝記錄沒有跨程序鎖，兩個帳號同時安裝或更新 plugin 時，後寫入的會蓋掉前一個的記錄；同一時間只在一個 session 裡裝 plugin 就不會碰到。

### 同步專案信任到其他帳號

`.claude.json` 存放每個專案的信任與權限狀態（是否已通過信任對話框、`allowedTools`、MCP 伺服器允許清單等），但這份檔案跟帳號身分（`oauthAccount`、`userID`）、上線狀態與逐 session 統計資料是同一份，沒辦法像 `settings.json` 那樣用 symlink 共用——共用了身分也等於共用了登入。

`帳號設定 > 同步專案信任到其他帳號` 改用「合併」而不是連結：選一個來源帳號，把它 `.claude.json` 裡下列白名單欄位，逐專案補進其他每個 Claude 帳號缺的地方——`hasTrustDialogAccepted`、`allowedTools`、`enabledMcpjsonServers`、`disabledMcpjsonServers`、`mcpContextUris`、`hasClaudeMdExternalIncludesApproved`。只補目標沒有的，既有值（包含明確的 `false`／`[]`／`{}`）一律不覆蓋，白名單以外的欄位、其他專案、與帳號身分完全不碰。

這是一次性的合併，不是即時連結：來源帳號之後又信任了新專案，要再跑一次才會補到其他帳號。

寫入前一樣先備份成 `.claude.json.bak-<時間>`。目標帳號還沒執行過 Claude Code（沒有 `.claude.json`）或正有 session 在跑（`.claude.json.lock` 存在）時會略過該帳號並回報原因，不會建立檔案或搶寫。**跑之前請先關閉目標帳號的 Claude Code session**，否則它結束時寫回的內容會把這次合併蓋掉。

憑證都不在共用的那些項目裡，所以共用設定不會讓帳號互相踩到登入狀態。

`共用設定到所有帳號` 會把每個非第一順位的帳號都接過去，也是連結被弄斷之後的修復手段。

每個共用項目各自判斷、各自成敗，一項失敗不會擋住其他項：

| 帳號目前的該項目 | 處置 |
|---|---|
| 沒有 | 直接接上 |
| 已經指向來源 | 略過 |
| 自己一份，內容與來源不同 | 搬到 `<名稱>.bak-<時間>` 再接，並印出備份路徑 |
| 自己一份的檔案，內容與來源相同 | 直接接上，不留備份（那份備份保存不了任何東西）|
| 自己一份的目錄 | 一律保留備份，不做遞迴比對 |
| 指向第三個地方的 symlink | 拒絕改動並回報 |
| 種類不符（該是檔案卻是目錄，或反之）| 拒絕改動並回報 |

備份不會互相覆蓋：同一秒內再接一次會換一個不重複的檔名。接的過程若在中途失敗，會把原本那份搬回去，不會留下一個少了設定檔或 skill 目錄的帳號。

兩點要知道：

- **有東西以「寫暫存檔再 rename」的方式存檔時，symlink 會被換成實體檔**，該帳號就靜默地不再共用。這種狀態跟「從來沒共用過」在檔案上看起來一樣，工具分不出來——重跑一次 `共用設定到所有帳號` 就能接回去。
- **共用的是同一個實體檔案，所以 `chmod` 也是共用的。** 設定檔裡若有密鑰之類的東西，記得把權限收成 `600`。

帳號 registry 位於：

```text
~/.config/agent-conversation-handoff/accounts.json
```

registry 使用跨程序鎖與原子寫入，避免同時開啟多個 `ccs` 時互相覆蓋帳號異動。讀取時會驗證版本、必要欄位、帳號唯一性與下一個穩定編號；若 JSON 或結構損壞，工具會保留原檔、建立 `accounts.json.corrupt-<時間>` 備份並停止，不會自行覆寫重建。

## 查看用量

`查看用量` 列出每個已登記帳號的 Claude 用量（5 小時與 7 天兩個視窗）。`ccs` 本身不查任何 API、不碰認證資料、也不會為此開啟 session——它只讀取另一支獨立程式寫在下面這個目錄的 snapshot 檔（每個 Claude config 目錄一份，副檔名 `.json`，檔名不拘）：

```text
~/.config/agent-conversation-handoff/usage
```

可用 `--usage-dir` 覆蓋，用法與 `--registry` 相同；也可以在 `帳號設定 > 設定用量資料目錄` 直接設定，設定後存進 registry，不用每次開 `ccs` 都重打一次旗標。優先順序是：這次執行明確帶 `--usage-dir` 就用它，否則用 registry 裡存的設定，都沒有才用預設路徑。畫面上每次都會印出目前實際讀的是哪個目錄，設定完馬上能對照。

snapshot 的 `config_dir` 是用檔案系統（而非字串比對）去對應到帳號的 home，跟共用設定時判斷兩個路徑是不是同一份檔案的做法一致，所以透過 symlink 或不同拼法指到的帳號目錄一樣認得出來。單一 snapshot 檔讀不到、格式錯誤、`config_dir`／`checked_at` 這類決定身分與新鮮度的欄位解析不出來、或 `version` 是這個版本不認得的號碼，都只會讓那個帳號顯示「沒有資料」，不會讓其他帳號的資料也不見；完全沒有 snapshot 的帳號一樣會列出來，同樣標示沒有資料。`version` 目前只認得 `1`：往後新增選填欄位不必跳號，會改變既有欄位意義的變更才需要跳號，屆時舊版讀到新檔一樣視為沒有資料，不會誤讀。用量目錄本身讀不到（權限不足、或路徑其實是個檔案）不會讓整個選單跟著失敗，畫面會先說明那個目錄讀不到，再照常把每個帳號列成沒有資料，選完照樣回到主選單。目錄不存在（寫入程式從沒跑過）或是空的（跑過但這批帳號還沒有資料）都算正常狀態，同樣列出沒有資料而不是報錯；這種情況下畫面底下還會補一行提示，說這份資料由 status line 程式寫入、要在那邊啟用寫入才會出現。

**這份資料的新鮮度只到那個帳號上一次開 session 的時候，可能是好幾天前。** 因此每一列都會附上資料的時間與相對新舊（例如「3 分鐘前」）；若 `checked_at` 比現在還晚，代表寫入那台機器的時鐘不準，畫面會直接說明時間異常，不會誤判成「剛剛」。某個視窗的 `resets_at` 若已經到了或已經過了，代表那個視窗早就重置過，畫面會直接說「已重置」而不是繼續顯示那個過期的百分比；snapshot 沒帶 `resets_at` 時，只要資料時間已經超過那個視窗自己的長度（5 小時視窗超過 5 小時、7 天視窗超過 7 天），一樣視為已重置。單一視窗物件存在但沒有 `used_percentage`（例如 `{}`）也視為沒有資料。缺值的視窗一律顯示為 `–`，不會顯示成 `0%`。

### 讓 status line 寫入 snapshot

上面這些 snapshot 檔是另一支獨立程式寫的，而它只有在 Claude Code 的 `statusLine.command` 帶了 `--usage-dir <目錄>` 時才會寫。單獨設定用量目錄（上一節）並不會自動接上這一段——`帳號設定 > 狀態列設定` 補的就是這個缺口：檢查 Claude 的 `settings.json` 現在的 `statusLine.command` 指向哪支執行檔、有沒有帶 `--usage-dir`、帶的值跟 `ccs` 現在讀的目錄是不是同一個，並在需要時代為加上或移除這個參數。

這份 `settings.json` 是該 provider 所有帳號共用的一份（symlink，見「共用設定」一節），所以在這裡的變更會套用到所有帳號，不是只影響選單裡當時選的那一個。

行為與限制：

- 只動 `statusLine.command` 這一個字串欄位，檔案裡其他每一個位元組都不變——不會把整份文件解析後重新編碼（那樣做會因為 map key 排序而打亂手動維護過的檔案格式），而是直接在原始位元組上做外科手術式取代。
- 寫入前一律先備份成 `settings.json.bak-<時間>`，用的是既有的備份命名與原子寫入機制，跟共用設定失敗時的備份是同一套。
- 已經帶了正確的 `--usage-dir` 時再按一次「啟用」不會重複加上；已經沒有這個參數時按「停用」也只會照實回報未變動。
- 找不到 `statusLine`、`command` 不是純字串，或整體格式不是預期形狀時一律拒絕修改，並印出可以手動貼上的設定片段（執行檔路徑一律留白讓你自己填，因為 `ccs` 沒有任何辦法知道那支程式實際裝在哪裡）。
- 既有的 `--usage-dir` 若指向別的目錄，會照實回報不一致，並讓你選擇要不要取代，不會自動覆蓋。

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
