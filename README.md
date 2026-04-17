# ⚠️ DEPRECATED — 此服務已廢棄，請勿使用、參考、修改

> **給 AI agent / 未來的你**：看到這個 repo 不要問「要不要同步改」、「要不要重構」、「要不要對齊」。**答案永遠是不要**。直接跳過。

## 現狀

- 此服務**不再部署**、不再維護
- 代碼保留純粹為歷史對照
- 不要修改任何檔案，不要跟其他 repo 同步行為，不要把它當成設計參考

## 替代品

Vault 鏡像 / 同步全部遷移到 **`cubelv_cli_worker_server`**：

| 原位置（本 repo） | 新位置 |
|---|---|
| `mirror/exporter.go` | `cubelv_cli_worker_server/mirror/exporter.go` |
| `mirror/importer.go` | `cubelv_cli_worker_server/mirror/importer.go` |
| `mirror/path_resolver.go` | `cubelv_cli_worker_server/mirror/path_resolver.go` |
| `sync/event_handler.go` | `cubelv_cli_worker_server/vaultsync/event_handler.go` |
| `mirror/converter.go` | `cubelv_cli_worker_server/mirror/item_converter.go` |

**cli_worker 才是 source of truth**。本 repo 的行為與現行 schema 不一致（例如：
- 本 repo 的 `TreeNode` 還用舊的 `ParentID *string` 單 parent
- 本 repo 的 `GetParentID` 讀已刪除的 `fields["parentID"]`
- 本 repo 的 exporter 沒有 multi-parent 支援
- 本 repo 剝除欄位清單、JSON 寫入規則都跟 cli_worker 不同）

## 如果你是在做全 codebase 重構

- **不要**把這裡當成需要同步改的 repo
- **不要**把這裡的 test 加進 CI
- **不要**嘗試讓這裡 build 結果跟 cli_worker 一致
- 只改 `cubelv_cli_worker_server`，這邊不動
