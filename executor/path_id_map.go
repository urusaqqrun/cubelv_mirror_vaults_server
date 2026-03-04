package executor

import (
	"encoding/json"
	"io/fs"
	"strings"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
)

// BuildPathIDMap 掃描用戶 Vault 目錄，建立 path→docID 映射。
// 用於刪除回寫：AI 刪除檔案後無法讀取內容取得 ID，需事前建立映射。
func BuildPathIDMap(vaultFS mirror.VaultFS, userID string) map[string]string {
	idMap := make(map[string]string)
	prefix := userID + "/"

	vaultFS.Walk(userID, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		data, rErr := vaultFS.ReadFile(path)
		if rErr != nil {
			return nil
		}

		relPath := path
		if len(path) > len(prefix) && path[:len(prefix)] == prefix {
			relPath = path[len(prefix):]
		}

		if strings.HasSuffix(path, ".md") {
			meta, _, pErr := mirror.MarkdownToNote(string(data))
			if pErr == nil && meta.ID != "" {
				idMap[relPath] = meta.ID
			}
		} else if strings.HasSuffix(path, "_folder.json") {
			meta, jErr := mirror.JSONToFolder(data)
			if jErr == nil && meta.ID != "" {
				idMap[relPath] = meta.ID
			}
		} else if strings.HasSuffix(path, ".json") {
			var doc map[string]any
			if jErr := json.Unmarshal(data, &doc); jErr == nil {
				if id, ok := doc["id"].(string); ok && id != "" {
					idMap[relPath] = id
				}
			}
		}
		return nil
	})
	return idMap
}
