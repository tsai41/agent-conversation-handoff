# bin/ach: Python → Go 單一執行檔改寫

日期：2026-07-20
狀態：設計已核准，待寫實作計畫

## 動機

`bin/ach` 目前是一個 bash wrapper（`bin/ach`）：`exec python3 - "$@" 3<&0 <<'PY' ... PY`，
把整份邏輯以 heredoc 塞給 `python3 -`。這代表每台要用 `ccs` 的機器都得裝 `python3`，
即使那台機器完全不做 Python 開發。

直接動機是一起修過的 stdin bug：heredoc 把 fd 0 佔走、真終端機存在 fd 3，三處
`os.execvpe` 換到 `claude`/`codex` 時忘記把 fd 3 dup 回 fd 0，導致子行程繼承一個
已耗盡、非 tty 的 stdin，子行程自己 check 到就报 `Error: stdin is not a terminal`。
已用 `os.dup2(3, 0)` 修好（見 git log），但這整類問題的根源是 heredoc trick 本身——
換成原生編譯執行檔後，entrypoint 就是二進位本身，不需要任何 fd 轉接把戲。

第二動機是使用者原話：「不想要每台電腦都要裝 python，又用不到 python 開發卻要因此安裝」。
這是合理的分發需求，不是效能問題（`codex_session_candidates` 的效能瓶頸已證實是演算法
問題——早退機制修完 python 版本本身就有 340 倍加速——跟語言無關）。

## 目標 / 非目標

**目標**
- 拿掉 `python3` 這個 runtime 依賴，改成單一原生二進位。
- 功能完全對等（1:1 移植所有 subcommand、registry.json schema、CLI 行為）。
- 保留現有黑盒測試風格（spawn 執行檔、PATH 塞假 fzf/claude/codex 腳本斷言行為）。
- macOS 兩種 arch（arm64 + amd64）透過 GitHub Release 分發，別台機器不用裝任何開發工具鏈。

**非目標（out of scope，這次不做）**
- Linux/Windows 支援。
- `jq`/純 shell 版本（已在設計討論中比較過，選定 Go）。
- 改變 registry.json 的資料格式或既有安裝的相容性（見下方「相容性」）。

## 架構

```
cmd/ach/main.go        CLI 入口、argparse 對應（Go 標準庫 flag 手刻 subcommand 分派）
internal/registry/      registry.go（load/save/lock/schema 驗證/account CRUD）
internal/session/       session.go（Claude/Codex session 掃描與 preview 產生）
internal/handoff/       handoff.go（create：sha256 快照、transcript 產生、manifest）
internal/menu/          menu.go（互動選單：pick_key/pick_session、numbered bind、方向清單）
internal/provider/      provider.go（claude/codex 共用邏輯：CLAUDE_CONFIG_DIR/CODEX_HOME、
                         auth-status、launch/login exec）
```

零外部 Go module 依賴：`encoding/json`、`os/exec`、`flag`、`crypto/sha256`、`syscall`
（`flock` 對應 `fcntl.flock`）全部標準庫解決。`go.mod` 不拉 third-party package，
build 環境單純、無 supply-chain 疑慮。

fzf 互動邏輯不變：一樣用 `exec.Command("fzf", ...)`、把候選清單塞進 fzf 的 stdin、
讀 stdout 取得選擇。這層完全不用改設計，只是語言換了。

## 資料模型與相容性

registry.json schema **原封不動**，既有安裝（`~/.config/agent-conversation-handoff/accounts.json`）
不需要遷移：

```json
{
  "version": 1,
  "next_number": {"claude": N, "codex": N},
  "accounts": [
    {"id": "claude-1", "provider": "claude", "number": 1, "home": "...", "alias": "..."}
  ]
}
```

Go 版沿用相同驗證規則（`registry_schema_problem` 對應邏輯：version 必須是 1、
next_number 必須同時有 claude/codex 且大於已用過的最大 number、account id 格式
`{provider}-{number}`、home 不可重複、alias 必須是字串）。

**檔案鎖與原子寫入**：python 版用 `fcntl.flock` 排他鎖 + `tempfile.mkstemp` 寫暫存檔
+ `fsync` + `os.replace` 原子換檔。Go 版用 `syscall.Flock`（同樣是 flock，行為一致）+
`os.CreateTemp` + `f.Sync()` + `os.Rename`，語意對等。

**交接 artifact**（`.agent-handoffs/<timestamp>-<hash12>/`）：sha256 完整性檢查
（複製前後來源檔案 hash 一致才 `os.replace` 發布，否則整個放棄並刪除暫存目錄）、
權限位元（目錄 0700、內容檔案 0600）、`.gitignore` 內容 `*\n` 全部照搬，Go 版用
`os.Chmod`/`os.MkdirAll(..., 0700)` 對應。

## Session 掃描（沿用已修好的效能設計）

- Claude：`<home>/projects/<project-id>/*.jsonl`，本來就是 per-project 目錄，效能沒問題。
- Codex：`<home>/sessions/**/*.jsonl` 全掃 + 逐行判斷 `cwd`。**沿用剛修好的 early-break
  邏輯**：讀到 `session_meta` 就檢查 `cwd`，不符合立刻 `break` 換下一個檔案，不要把整份
  歷史檔案讀完（python 版本此修復已驗證 1.26s → 0.0037s，Go 版必須維持同樣的提前中止邏輯，
  不能退化回全檔案掃描）。Go 用 `bufio.Scanner` 逐行讀取，同樣不要 `ReadFile` 整檔進記憶體。

## 互動選單（沿用剛做的三項 UX 改動）

以下三項是這次改寫前才剛做的 UX 修正，Go 版必須保留，不是可以重新討論的空間：

1. **接力方向**：「來源」「目標」合併成一個 `pos(N)+accept` 選項，文字用箭頭格式
   `「來源」→「目標」`（不是「用 X 接續 Y」，已依使用者回饋改過一次）。
2. **主選單數字鍵直選**：`pick_key` 的 numbered 模式，候選清單加 `1. 2. 3...` 前綴，
   fzf `--bind=1:pos(1)+accept,2:pos(2)+accept,...`，上限 9 個（只套在主選單，不套子選單）。
3. **session_id 截斷**：UUID 太長擠掉 preview 文字，顯示改成頭 8 碼 + `…` + 尾 4 碼。

## Provider exec（不再需要 fd 轉接）

`launch_account`、`launch_account_session`、`login_account` 三處對應的 Go 版用
`syscall.Exec`（取代行程本體，語意對等 `os.execvpe`）。**不需要 `restore_terminal_stdin`
這個 helper**——Go 二進位本身就是 entrypoint，`os.Stdin`/`Stdout`/`Stderr` 從一開始就是
真終端機，沒有 heredoc 佔用 fd 0 的問題，這整個 bug class 在架構上就不存在。

## CLI 介面（1:1 對應現有 argparse 介面，行為不變）

`create`、`handoff`（`--source-id`/`--target-id`/`--session`/`--no-launch`）、
`accounts list/add/remove/rename/auth-status/discover/suggest-home`、`menu`。
Flag 名稱、輸出格式（例如 `accounts list` 印 `id\tlabel`）、exit code 語意全部保留，
確保任何包著 `ach` 呼叫的腳本（若有）不用改。

## 測試

沿用現有黑盒風格：spawn 編譯好的執行檔、PATH 塞假 `fzf`/`claude`/`codex` shell script
斷言行為，用 Go 標準庫 `testing` + `t.TempDir()` 重寫，不拉 testify 之類外部依賴。
現有 31 個 python test case（`tests/test_accounts.py`、`test_handoff.py`、`test_install.py`、
`test_menu.py`、`test_registry_handoff.py`）逐一對應搬成 Go test，包含最近新加的：
stdin 還原測試（雖然 Go 版不會有這個 bug，但可以留一個等價測試確認 provider CLI 真的
繼承到終端 stdin）、numbered bind 字串斷言、session_id 截斷斷言。

## Build / Release

- `Makefile` 加 `build` target：`GOOS=darwin GOARCH=arm64 go build -o bin/ach-darwin-arm64`
  與 `GOARCH=amd64` 版本。
- 新增 `.github/workflows/release.yml`：push tag（`v*`）觸發，cross-compile 兩種 arch，
  建立 GitHub Release，上傳兩個二進位 asset。
- `make install`：偵測本機 arch（`uname -m`）→ 從最新 GitHub Release 下載對應資產
  （`curl` 打 GitHub API 取得 latest release asset URL）→ 放到 `~/bin/ccs`，可執行權限
  `chmod +x`。**不再需要 `git clone` 或本機安裝 Go**——只有維護者（在這台機器上）需要
  Go 工具鏈來 build 與 push tag，其他機器純下載二進位。
- `bin/launcher` 這層 bash 轉接殼可以拿掉：`~/bin/ccs` 直接是下載回來的 Go 二進位，
  不需要再解析 symlink 找 repo 目錄。

## README 更新

拿掉「前置需求：python3」；安裝說明改成下載 release 二進位（維護者一台機器仍保留
git clone + `make build` + push tag 的開發流程說明）。

## Commit 計畫（供 writing-plans 展開細部步驟）

1. 核心邏輯（registry/session/handoff/menu/provider）+ 對應 Go test 全綠，CLI 行為
   與現有 python 版本並行驗證（同樣輸入、同樣 registry.json，輸出比對）。
2. `Makefile` build target + `.github/workflows/release.yml`。
3. `make install` 改成下載 release 二進位；`bin/launcher`、`bin/ach`（python 版）、
   `tests/*.py` 移除；README 更新拿掉 python3 依賴。

## 風險 / 待確認

- `syscall.Flock` 在 macOS 上行為需要用真實檔案測試確認（不是模擬），尤其是
  `registry_updates_wait_for_the_registry_lock` 這個既有測試案例要能對應搬過去。
- GitHub Actions 的 release workflow 需要 repo 有寫入 Release 的權限（預設
  `GITHUB_TOKEN` 通常夠用，但要在 workflow 檔案裡明確給 `contents: write` 權限）。
