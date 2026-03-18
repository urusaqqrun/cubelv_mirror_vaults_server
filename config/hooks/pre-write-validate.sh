#!/bin/bash
# PreToolUse Write 驗證
# 在 Claude 寫入檔案之前檢查 vault 結構規則
# 僅在 TASK_SCOPE != "plugin" 時執行 vault 規則檢查

INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd')

if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# plugin scope 不走 vault 規則
if [ "$TASK_SCOPE" = "plugin" ]; then
  exit 0
fi

# 安全：禁止寫入 cwd 範圍之外
case "$FILE_PATH" in
  "$CWD"/*)
    ;;
  *)
    if [[ "$FILE_PATH" != /* ]]; then
      # 相對路徑，允許
      :
    else
      jq -n '{
        hookSpecificOutput: {
          hookEventName: "PreToolUse",
          permissionDecision: "deny",
          permissionDecisionReason: "禁止寫入工作目錄範圍外的路徑"
        }
      }'
      exit 0
    fi
    ;;
esac

# 取得目標檔案的目錄（絕對或相對）
if [[ "$FILE_PATH" == /* ]]; then
  DIR=$(dirname "$FILE_PATH")
else
  DIR=$(dirname "$CWD/$FILE_PATH")
fi

# 規則 1：Folder 不能同時包含 folder 和 note
# 如果要寫入 .md 檔案，檢查同目錄是否已有子資料夾（含 _folder.json）
if [[ "$FILE_PATH" == *.md ]]; then
  HAS_SUBFOLDER=false
  if [ -d "$DIR" ]; then
    for sub in "$DIR"/*/; do
      if [ -f "${sub}_folder.json" ] 2>/dev/null; then
        HAS_SUBFOLDER=true
        break
      fi
    done
  fi
  if [ "$HAS_SUBFOLDER" = "true" ]; then
    jq -n '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: "此資料夾已包含子資料夾，不能在此新增筆記檔案。請將筆記放在子資料夾中，或先移除子資料夾。"
      }
    }'
    exit 0
  fi
fi

# 規則 2：如果要建立 _folder.json（新資料夾），檢查同層是否已有 .md 筆記
if [[ "$FILE_PATH" == */_folder.json ]]; then
  PARENT_DIR=$(dirname "$DIR")
  if [ -d "$PARENT_DIR" ]; then
    MD_COUNT=$(find "$PARENT_DIR" -maxdepth 1 -name "*.md" 2>/dev/null | wc -l)
    if [ "$MD_COUNT" -gt 0 ]; then
      jq -n '{
        hookSpecificOutput: {
          hookEventName: "PreToolUse",
          permissionDecision: "deny",
          permissionDecisionReason: "此資料夾已包含筆記檔案，不能在此新增子資料夾。Folder 不能同時包含 folder 和 note。"
        }
      }'
      exit 0
    fi
  fi
fi

# 規則 3：Folder 最多兩層巢狀
# 計算相對於 vault root 的目錄深度（NOTE/A/B/ = 3 層，超過 2 層阻擋）
if [[ "$FILE_PATH" == */_folder.json ]]; then
  REL_PATH="${DIR#$CWD/}"
  DEPTH=$(echo "$REL_PATH" | tr '/' '\n' | wc -l)
  if [ "$DEPTH" -gt 3 ]; then
    jq -n '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: "資料夾巢狀深度超過限制（最多兩層）。請減少資料夾層級。"
      }
    }'
    exit 0
  fi
fi

exit 0
