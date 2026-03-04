package executor

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"strings"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
)

// isSystemFile 判斷是否為系統檔案（不參與 diff/回寫）
func isSystemFile(relPath string) bool {
	if relPath == "CLAUDE.md" {
		return true
	}
	if strings.HasPrefix(relPath, ".NoteCEO/") {
		return true
	}
	return false
}

// TakeSnapshot 掃描用戶 Vault 目錄產生檔案快照（用於 AI 任務前後 diff 比對）
func TakeSnapshot(vaultFS mirror.VaultFS, userID string) (map[string]FileSnapshot, error) {
	snap := make(map[string]FileSnapshot)
	err := vaultFS.Walk(userID, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		data, rErr := vaultFS.ReadFile(path)
		if rErr != nil {
			return nil
		}
		h := sha256.Sum256(data)
		// path 已是相對於 VaultFS root 的路徑（含 userID 前綴），
		// 需去掉 userID/ 前綴讓 diff 結果可直接對應 Vault 內部路徑
		relPath := path
		prefix := userID + "/"
		if len(path) > len(prefix) && path[:len(prefix)] == prefix {
			relPath = path[len(prefix):]
		}
		if isSystemFile(relPath) {
			return nil
		}
		snap[relPath] = FileSnapshot{
			Path:    relPath,
			Hash:    fmt.Sprintf("%x", h),
			ModTime: info.ModTime(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}
