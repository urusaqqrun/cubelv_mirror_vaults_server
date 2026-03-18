#!/bin/bash
# PreToolUse Bash 安全檢查
# 阻擋危險的 shell 命令

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd')

if [ -z "$COMMAND" ]; then
  exit 0
fi

# 規則 1：阻擋破壞性命令
DANGEROUS_PATTERNS=(
  "rm -rf /"
  "rm -rf ~"
  "rm -rf \$HOME"
  "mkfs"
  "dd if="
  "> /dev/sd"
)

for pattern in "${DANGEROUS_PATTERNS[@]}"; do
  if echo "$COMMAND" | grep -qF "$pattern"; then
    echo "阻擋：偵測到危險命令模式 \"$pattern\"" >&2
    exit 2
  fi
done

# 規則 2：禁止寫入 /vault/shared/（唯讀區域）
if echo "$COMMAND" | grep -qE "(>|>>|tee|cp |mv |rm ).*/vault/shared/"; then
  echo "阻擋：/vault/shared/ 是唯讀目錄，禁止寫入" >&2
  exit 2
fi

# 規則 3：禁止存取其他用戶的 vault 目錄
if [ -n "$VAULT_USER_ID" ]; then
  if echo "$COMMAND" | grep -oE "/vault/[a-zA-Z0-9_-]+/" | grep -v "/vault/$VAULT_USER_ID/" | grep -qv "/vault/shared/"; then
    echo "阻擋：禁止存取其他用戶的 vault 目錄" >&2
    exit 2
  fi
fi

exit 0
