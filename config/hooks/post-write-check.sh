#!/bin/bash
# PostToolUse Write|Edit 檢查
# 寫入後驗證檔案完整性，回饋給 Claude（無法 block，僅提醒修正）

INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')

if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# plugin scope 不檢查 vault 規則
if [ "$TASK_SCOPE" = "plugin" ]; then
  exit 0
fi

# 取得實際檔案路徑
if [[ "$FILE_PATH" != /* ]]; then
  CWD=$(echo "$INPUT" | jq -r '.cwd')
  FULL_PATH="$CWD/$FILE_PATH"
else
  FULL_PATH="$FILE_PATH"
fi

# 檢查 1：.md 檔案必須保留 frontmatter 中的 id
if [[ "$FILE_PATH" == *.md ]] && [ -f "$FULL_PATH" ]; then
  FIRST_LINE=$(head -1 "$FULL_PATH")
  if [ "$FIRST_LINE" = "---" ]; then
    HAS_ID=$(head -20 "$FULL_PATH" | grep -c "^id:")
    if [ "$HAS_ID" -eq 0 ]; then
      jq -n '{
        decision: "block",
        reason: "此 .md 檔案的 frontmatter 缺少 id 欄位。請確保 frontmatter 中保留 id 和 parentID。格式為 ---\\nid: xxx\\nparentID: xxx\\n---"
      }'
      exit 0
    fi
  fi
fi

# 檢查 2：_folder.json 必須保留 ID 和 memberID
if [[ "$FILE_PATH" == */_folder.json ]] && [ -f "$FULL_PATH" ]; then
  HAS_ID=$(jq -r '.ID // empty' "$FULL_PATH" 2>/dev/null)
  HAS_MEMBER=$(jq -r '.memberID // empty' "$FULL_PATH" 2>/dev/null)
  if [ -z "$HAS_ID" ]; then
    jq -n '{
      decision: "block",
      reason: "_folder.json 缺少 ID 欄位。請確保保留原始的 ID 和 memberID 欄位。"
    }'
    exit 0
  fi
fi

exit 0
