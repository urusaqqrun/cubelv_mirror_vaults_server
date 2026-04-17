# ⚠️ 此服務已廢棄（Deprecated）

**此 repo 已不再使用，請勿參考其中代碼作為現行設計依據。**

Vault 鏡像功能已全部遷移到 **`cubelv_cli_worker_server`**：

- Vault export / import：`server/cubelv_cli_worker_server/mirror/`
- Vault sync pipeline：`server/cubelv_cli_worker_server/vaultsync/`

本 repo 保留僅供歷史對照。新的 schema、converter、importer、exporter 行為皆以 `cli_worker` 為準，且與此處不完全一致（例如 `cli_worker` 剝除 `parents`、不寫 `name` 進 JSON；此處剝除 `parentID`、仍寫 `name`）。

請以 `cli_worker` 為唯一 source of truth。
