package api

import (
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
)

// SchemaHandler handles item schema sync from frontend decorators.
type SchemaHandler struct {
	fs mirror.VaultFS
}

func NewSchemaHandler(fs mirror.VaultFS) *SchemaHandler {
	return &SchemaHandler{fs: fs}
}

func (h *SchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/vault/schemas", h.SyncSchemas)
}

// SyncSchemas receives item type schemas from the frontend @itemType decorator
// and writes them to .schemas/ in the user's vault directory.
func (h *SchemaHandler) SyncSchemas(w http.ResponseWriter, r *http.Request) {
	// Extract memberID from query or header
	memberID := r.URL.Query().Get("memberID")
	if memberID == "" {
		memberID = r.Header.Get("X-User-ID")
	}
	if memberID == "" {
		// Try to get from auth token (simplified: just use a default for now)
		memberID = "_global"
	}

	var req struct {
		Schemas map[string]interface{} `json:"schemas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}

	if len(req.Schemas) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"count":0}`))
		return
	}

	// Write each schema as a separate .json file
	schemasDir := filepath.Join(memberID, ".schemas")
	h.fs.MkdirAll(schemasDir)

	for typeName, schema := range req.Schemas {
		data, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			continue
		}
		path := filepath.Join(schemasDir, typeName+".json")
		if err := h.fs.WriteFile(path, data); err != nil {
			log.Printf("[SchemaHandler] write %s error: %v", path, err)
		}
	}

	// Write _index.json with all schemas
	indexData, _ := json.MarshalIndent(req.Schemas, "", "  ")
	indexPath := filepath.Join(schemasDir, "_index.json")
	if err := h.fs.WriteFile(indexPath, indexData); err != nil {
		log.Printf("[SchemaHandler] write _index.json error: %v", err)
	}

	log.Printf("[SchemaHandler] synced %d schemas for %s", len(req.Schemas), memberID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(req.Schemas),
	})
}
