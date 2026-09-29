# 用量 snapshot 格式

`ccs` 的「查看用量」只讀 snapshot 檔，不查任何 API、不碰認證資料、也不會為此開啟 session。誰來寫這些檔不重要，任何程式照下面的格式寫檔就能接上；這份文件就是公開的介面。內建的 `ccs usage record` 只是其中一個寫入端。

## 目錄

- 預設路徑：`~/.config/agent-conversation-handoff/usage`。
- `--usage-dir` 可覆蓋；預設路徑如何隨 `ACH_CONFIG_DIR` 改變，見 [README 的「設定檔位置」](../README.md#設定檔位置)。
- 也可以在 `帳號設定 > 設定用量資料目錄` 設定，設定後存進 registry 的 `usage_dir`。
- 優先順序：這次執行明確帶 `--usage-dir` 就用它，否則用 registry 的 `usage_dir`，都沒有才用預設路徑。選單畫面每次都會印出實際讀的目錄。

## 檔名

檔名由寫入端自己決定。讀取端會讀目錄裡所有 `*.json`，不看檔名；檔名不是寫入端之間協調的機制，同一個帳號有多份檔案是正常狀態，讀取端會依下面的選擇規則合併。

`ccs usage record` 的命名方式是：把正規化後的 `config_dir` 取 SHA-256 十六進位（不含換行），加上 `.json`。正規化與帳號 home 相同：展開開頭的 `~`、轉成絕對路徑、去掉多餘的 `/` 與 `..`。

寫檔請先寫暫存檔再 rename，讀取端才不會讀到寫到一半的檔案；暫存檔別用 `.json` 結尾。

## 欄位（version 1）

```json
{
  "version": 1,
  "config_dir": "/Users/me/.claude",
  "checked_at": "2026-09-29T06:28:30Z",
  "five_hour": {"used_percentage": 24, "resets_at": "2026-09-29T06:50:00Z"},
  "seven_day": {"used_percentage": 3, "resets_at": "2026-10-04T23:00:00Z"}
}
```

必填：

- `version`：必須是 `1`。
- `config_dir`：該帳號的 Claude config 目錄，必須是絕對路徑。讀取端用檔案系統（不是字串比對）對應到帳號的 home，所以 symlink 或不同拼法指到同一個目錄也認得出來。空字串或相對路徑的 snapshot 會被略過。
- `checked_at`：RFC3339 時間，是這組數字被觀察到的時間；取不到確切時間時，寫最接近的近似值。缺少、解析不出來或是零值的 snapshot 會被略過。

選填：

- `five_hour`、`seven_day`：缺哪個視窗，那個視窗就顯示 `–`。
  - `used_percentage`：0 到 100 的數字。超出範圍、`NaN` 或缺少都視為該視窗沒有資料。
  - `resets_at`：RFC3339 時間。解析不出來時只有這個欄位視為缺少，旁邊的百分比照用。

## 讀取端的選擇規則

同一個帳號可能有多份 snapshot（多個寫入端、或同一個目錄的不同拼法）。讀取端不看檔案時間也不看現在時鐘，對每個帳號、每個視窗各自挑一個值：

1. 有 `resets_at` 的候選優先於沒有的；都有時，`resets_at` 較晚的勝出，因為那是較新的視窗。例外：沒有 `resets_at` 的那份，若 `checked_at` 不早於對方的 `resets_at`，代表對方的視窗在它被觀察時已經結束，這時 `checked_at` 較晚的勝出。
2. `resets_at` 相同時，`used_percentage` 較大的勝出，因為同一個視窗內用量只會增加。閒置 session 常常用較新的 `checked_at` 重寫較舊、較低的數字，所以不能只比 `checked_at`。
3. `resets_at` 與 `used_percentage` 都相同時，`checked_at` 較晚的勝出。沒有任何候選帶 `resets_at` 時，也是 `checked_at` 最晚的勝出。

畫面顯示的資料時間是被選中的值所屬 snapshot 的 `checked_at`；兩個視窗來自不同 snapshot 時，顯示較早的那個，避免把舊數字讀成新的。

## 畫面上的呈現

- 每一列附上資料的時間與相對新舊。`checked_at` 比現在晚時，畫面直接標示時間異常，不會顯示成「剛剛」。
- 視窗的 `resets_at` 已到或已過，畫面顯示「已重置」而不是過期的百分比。沒有 `resets_at` 時，資料時間超過視窗自己的長度（5 小時或 7 天）也視為已重置。
- 缺值的視窗一律顯示 `–`，不會顯示成 `0%`。
- 單一檔案讀不到、格式錯誤或 `version` 不認得，只會讓它自己不被採用，不影響其他檔案與其他帳號。目錄不存在或是空的都是正常狀態，只是每個帳號都顯示沒有資料。

## 版本規則

新增欄位若舊版讀取端可以安全忽略，可以維持 `version` 為 `1`。會改變既有欄位意義、或舊版讀取端忽略後會誤讀的變更，必須提高 `version`；舊版讀到新版檔案視為沒有資料，不會誤讀。

## 用 `ccs usage record` 寫入

`ccs usage record` 從 stdin 讀 Claude Code 傳給 status line 的 JSON，取 `rate_limits.five_hour` 與 `rate_limits.seven_day`（`resets_at` 是 epoch 秒，會轉成 RFC3339），為 `CLAUDE_CONFIG_DIR`（沒設就是 `~/.claude`）寫一份 snapshot。目錄的決定順序與選單相同，但不會讀取有問題的 registry 而觸發備份：registry 讀不了時，靜默改用預設路徑。

- 不會印任何東西到 stdout。
- 沒有 `rate_limits`、兩個視窗都沒有可用的數字（缺少、`NaN` 或超出 0 到 100）、stdin 不是合法 JSON、或找不到家目錄時，什麼都不寫，結束碼仍是 0，原本的檔案不會被覆寫。只有真正的寫入失敗才會在 stderr 印錯誤並回傳非 0。
- 每次呼叫覆寫該帳號那一份檔，`checked_at` 就是這次呼叫的時間。
