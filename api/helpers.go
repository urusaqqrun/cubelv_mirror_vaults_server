package api

import (
	"encoding/json"
	"net/http"
)

func memberIDFromHeader(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.Header.Get("X-User-ID")
	if id == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return id, true
}

func chatWriteError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func chatWriteJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
