#!/bin/bash
# Stop 最終驗證
# Claude 完成任務前執行全面檢查

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd')
STOP_ACTIVE=$(echo "$INPUT" | jq -r '.stop_hook_active')

# 防止無限循環：stop hook 已觸發過一次，直接放行
if [ "$STOP_ACTIVE" = "true" ]; then
  exit 0
fi

# plugin scope：檢查 main.tsx 入口是否存在
if [ "$TASK_SCOPE" = "plugin" ]; then
  PLUGIN_DIR=$(find "$CWD/plugins" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | head -1)
  if [ -n "$PLUGIN_DIR" ] && [ ! -f "$PLUGIN_DIR/main.tsx" ]; then
    jq -n '{
      decision: "block",
      reason: "插件目錄缺少 main.tsx 入口檔案，請建立。"
    }'
    exit 0
  fi
  exit 0
fi

# vault scope：全面檢查
ERRORS=""
CHECKED_PARENTS=""

# 檢查 1：Folder 不能同時包含 folder 和 note
while IFS= read -r folder_json; do
  DIR=$(dirname "$folder_json")
  PARENT=$(dirname "$DIR")

  # 跳過已檢查的目錄，避免重複報錯
  case "$CHECKED_PARENTS" in
    *"|$PARENT|"*) continue ;;
  esac
  CHECKED_PARENTS="${CHECKED_PARENTS}|$PARENT|"

  MD_COUNT=$(find "$PARENT" -maxdepth 1 -name "*.md" 2>/dev/null | wc -l)
  SUBFOLDER_COUNT=$(find "$PARENT" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | wc -l)

  if [ "$MD_COUNT" -gt 0 ] && [ "$SUBFOLDER_COUNT" -gt 0 ]; then
    REL=$(echo "$PARENT" | sed "s|$CWD/||")
    ERRORS="${ERRORS}${REL}/ 同時包含筆記和子資料夾（不允許）\n"
  fi
done < <(find "$CWD" -name "_folder.json" -not -path "*/.NoteCEO/*" 2>/dev/null)

# 檢查 2：所有 .md 檔案必須有 frontmatter id
while IFS= read -r md_file; do
  FIRST_LINE=$(head -1 "$md_file")
  if [ "$FIRST_LINE" = "---" ]; then
    HAS_ID=$(head -20 "$md_file" | grep -c "^id:")
    if [ "$HAS_ID" -eq 0 ]; then
      REL=$(echo "$md_file" | sed "s|$CWD/||")
      ERRORS="${ERRORS}${REL} 缺少 frontmatter id 欄位\n"
    fi
  fi
done < <(find "$CWD" -name "*.md" -not -name "CLAUDE.md" -not -path "*/.NoteCEO/*" 2>/dev/null)

if [ -n "$ERRORS" ]; then
  REASON=$(echo -e "以下問題需要修正：\n$ERRORS")
  jq -n --arg reason "$REASON" '{
    decision: "block",
    reason: $reason
  }'
  exit 0
fi

exit 0
