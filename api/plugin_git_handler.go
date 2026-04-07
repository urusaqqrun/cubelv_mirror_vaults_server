package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	vaultsync "github.com/urusaqqrun/vault-mirror-service/sync"
)

// PluginGitHandler manages per-plugin Git repositories.
// Each plugin has its own independent .git/ under plugins/{pluginDir}/.
type PluginGitHandler struct {
	vaultRoot string
	locker    vaultsync.VaultLocker
	rebuilder PluginRebuilder
}

func NewPluginGitHandler(vaultRoot string, locker vaultsync.VaultLocker, rebuilder PluginRebuilder) *PluginGitHandler {
	return &PluginGitHandler{vaultRoot: vaultRoot, locker: locker, rebuilder: rebuilder}
}

func (h *PluginGitHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vault/plugins/git/head", h.HandleHead)
	mux.HandleFunc("GET /api/vault/plugins/git/status", h.HandleStatus)
	mux.HandleFunc("GET /api/vault/plugins/git/status-all", h.HandleStatusAll)
	mux.HandleFunc("GET /api/vault/plugins/git/archive", h.HandleArchive)
	mux.HandleFunc("POST /api/vault/plugins/git/push", h.HandlePush)
	mux.HandleFunc("GET /api/vault/plugins/git/log", h.HandleLog)
}

// pluginsDir returns the parent directory containing all plugins.
func (h *PluginGitHandler) pluginsDir(userID string) string {
	return filepath.Join(h.vaultRoot, userID, "plugins")
}

// pluginRepoDir returns the directory for a specific plugin (also the git repo root).
func (h *PluginGitHandler) pluginRepoDir(userID, pluginDir string) string {
	return filepath.Join(h.vaultRoot, userID, "plugins", pluginDir)
}

// migrateMonoRepo detects the old monorepo format (plugins/.git) and splits
// into per-plugin repos. Idempotent — skips if already migrated.
func (h *PluginGitHandler) migrateMonoRepo(userID string) {
	pDir := h.pluginsDir(userID)
	markerPath := filepath.Join(pDir, ".migrated")

	if _, err := os.Stat(markerPath); err == nil {
		return // already migrated
	}

	oldGitDir := filepath.Join(pDir, ".git")
	if _, err := os.Stat(oldGitDir); err != nil {
		return // no old monorepo, nothing to migrate
	}

	log.Printf("[PluginGit] migrating monorepo for %s", userID)

	entries, err := os.ReadDir(pDir)
	if err != nil {
		log.Printf("[PluginGit] migration: cannot list plugins dir: %v", err)
		return
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" {
			continue
		}
		subDir := filepath.Join(pDir, e.Name())
		subGit := filepath.Join(subDir, ".git")
		if _, err := os.Stat(subGit); err == nil {
			continue // already has its own repo
		}

		if err := gitExec(subDir, "init"); err != nil {
			log.Printf("[PluginGit] migration: git init %s failed: %v", e.Name(), err)
			continue
		}
		gitignore := "frontend/node_modules/\nbackend/node_modules/\nfrontend/bundle.js\nfrontend/bundle.css\nbackend/dist/\n.DS_Store\n"
		os.WriteFile(filepath.Join(subDir, ".gitignore"), []byte(gitignore), 0644)
		if err := gitExec(subDir, "add", "-A"); err != nil {
			log.Printf("[PluginGit] migration: git add %s failed: %v", e.Name(), err)
			continue
		}
		if err := gitExec(subDir, "commit", "--allow-empty", "-m", "migrate: init from monorepo"); err != nil {
			log.Printf("[PluginGit] migration: git commit %s failed: %v", e.Name(), err)
			continue
		}
		log.Printf("[PluginGit] migration: initialized repo for %s/%s", userID, e.Name())
	}

	os.RemoveAll(oldGitDir)
	os.WriteFile(markerPath, []byte("migrated"), 0644)
	// remove old root .gitignore if present
	os.Remove(filepath.Join(pDir, ".gitignore"))
	log.Printf("[PluginGit] migration complete for %s", userID)
}

// ensureRepo initializes a Git repo in plugins/{pluginDir}/ if it doesn't exist.
func (h *PluginGitHandler) ensureRepo(userID, pluginDir string) error {
	h.migrateMonoRepo(userID)

	dir := h.pluginRepoDir(userID, pluginDir)
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := gitExec(dir, "init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	gitignore := "frontend/node_modules/\nbackend/node_modules/\nfrontend/bundle.js\nfrontend/bundle.css\nbackend/dist/\n.DS_Store\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		return err
	}
	if err := gitExec(dir, "add", "-A"); err != nil {
		return err
	}
	if err := gitExec(dir, "commit", "--allow-empty", "-m", "init: plugin repository"); err != nil {
		return err
	}
	log.Printf("[PluginGit] initialized repo for %s/%s", userID, pluginDir)
	return nil
}

// commit stages all changes and commits in a specific plugin repo.
func (h *PluginGitHandler) commit(userID, pluginDir, message, author string) (string, error) {
	dir := h.pluginRepoDir(userID, pluginDir)
	if err := h.ensureRepo(userID, pluginDir); err != nil {
		return "", err
	}
	if err := gitExec(dir, "add", "-A"); err != nil {
		return "", err
	}
	status, _ := gitOutput(dir, "status", "--porcelain")
	if strings.TrimSpace(status) == "" {
		return "", nil
	}
	authorArg := fmt.Sprintf("%s <noreply@cubelv.com>", author)
	if err := gitExec(dir, "commit", "-m", message, "--author", authorArg); err != nil {
		return "", err
	}
	hash, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(hash), nil
}

func (h *PluginGitHandler) headCommit(userID, pluginDir string) (string, error) {
	dir := h.pluginRepoDir(userID, pluginDir)
	if err := h.ensureRepo(userID, pluginDir); err != nil {
		return "", err
	}
	hash, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(hash), nil
}

// Commit is the public method for other handlers (plugin delete, etc.)
func (h *PluginGitHandler) Commit(userID, pluginDir, message, author string) (string, error) {
	return h.commit(userID, pluginDir, message, author)
}

// listPluginDirs returns all subdirectory names under plugins/ that have a .git/.
func (h *PluginGitHandler) listPluginDirs(userID string) []string {
	pDir := h.pluginsDir(userID)
	entries, err := os.ReadDir(pDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(pDir, e.Name(), ".git")); err == nil {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

// requirePluginParam extracts and validates the "plugin" query param.
func requirePluginParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.URL.Query().Get("plugin")
	if p == "" {
		chatWriteError(w, 400, "missing 'plugin' query parameter")
		return "", false
	}
	p = filepath.Base(p)
	if p == "." || p == ".." || strings.Contains(p, "/") {
		chatWriteError(w, 400, "invalid plugin name")
		return "", false
	}
	return p, true
}

// --- HTTP Handlers ---

// HandleHead returns the HEAD commit hash of a specific plugin repo.
// GET /api/vault/plugins/git/head?plugin={dir}
func (h *PluginGitHandler) HandleHead(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}
	pluginDir, ok := requirePluginParam(w, r)
	if !ok {
		return
	}
	hash, err := h.headCommit(memberID, pluginDir)
	if err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}
	chatWriteJSON(w, 200, map[string]string{"commit": hash})
}

// HandleStatus returns changed files for a specific plugin repo.
// GET /api/vault/plugins/git/status?plugin={dir}&since=...
func (h *PluginGitHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}
	pluginDir, ok := requirePluginParam(w, r)
	if !ok {
		return
	}
	since := r.URL.Query().Get("since")
	dir := h.pluginRepoDir(memberID, pluginDir)

	if err := h.ensureRepo(memberID, pluginDir); err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}

	head, _ := gitOutput(dir, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)

	var files []map[string]string
	if since == "" || since == "0" {
		output, _ := gitOutput(dir, "ls-files")
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			if line != "" {
				files = append(files, map[string]string{"path": line, "action": "A"})
			}
		}
	} else {
		output, err := gitOutput(dir, "diff", "--name-status", since+"..HEAD")
		if err != nil {
			output, _ = gitOutput(dir, "ls-files")
			for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
				if line != "" {
					files = append(files, map[string]string{"path": line, "action": "A"})
				}
			}
		} else {
			for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, "\t", 2)
				if len(parts) == 2 {
					files = append(files, map[string]string{"action": parts[0], "path": parts[1]})
				}
			}
		}
	}
	if files == nil {
		files = []map[string]string{}
	}

	chatWriteJSON(w, 200, map[string]interface{}{
		"headCommit": head,
		"files":      files,
	})
}

// HandleStatusAll returns the HEAD commit of every plugin repo at once.
// GET /api/vault/plugins/git/status-all
// Response: { "plugins": { "pomodoro": { "headCommit": "abc123" }, ... } }
func (h *PluginGitHandler) HandleStatusAll(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}

	h.migrateMonoRepo(memberID)

	result := map[string]interface{}{}
	for _, d := range h.listPluginDirs(memberID) {
		dir := h.pluginRepoDir(memberID, d)
		head, _ := gitOutput(dir, "rev-parse", "HEAD")
		result[d] = map[string]string{"headCommit": strings.TrimSpace(head)}
	}

	chatWriteJSON(w, 200, map[string]interface{}{"plugins": result})
}

// HandleArchive returns a tar.gz archive of a specific plugin's files.
// GET /api/vault/plugins/git/archive?plugin={dir}&since=...
func (h *PluginGitHandler) HandleArchive(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}
	pluginDir, ok := requirePluginParam(w, r)
	if !ok {
		return
	}
	since := r.URL.Query().Get("since")
	dir := h.pluginRepoDir(memberID, pluginDir)

	if err := h.ensureRepo(memberID, pluginDir); err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}

	var filePaths []string
	if since == "" || since == "0" {
		output, _ := gitOutput(dir, "ls-files")
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			if line != "" {
				filePaths = append(filePaths, line)
			}
		}
	} else {
		output, _ := gitOutput(dir, "diff", "--name-only", since+"..HEAD")
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			if line != "" {
				filePaths = append(filePaths, line)
			}
		}
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, fp := range filePaths {
		fullPath := filepath.Join(dir, fp)
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		header, _ := tar.FileInfoHeader(info, "")
		header.Name = fp
		tw.WriteHeader(header)
		f, err := os.Open(fullPath)
		if err != nil {
			continue
		}
		io.Copy(tw, f)
		f.Close()
	}

	tw.Close()
	gw.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.tar.gz", pluginDir))
	w.Write(buf.Bytes())
}

// HandlePush receives file changes for a specific plugin and commits them.
// POST /api/vault/plugins/git/push
// Body: { "pluginDir": "pomodoro", "baseCommit": "...", "message": "...", "files": [...] }
func (h *PluginGitHandler) HandlePush(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}

	if h.locker != nil && h.locker.IsLocked(memberID) {
		chatWriteError(w, 409, "vault locked")
		return
	}

	var req struct {
		PluginDir  string `json:"pluginDir"`
		BaseCommit string `json:"baseCommit"`
		Message    string `json:"message"`
		Files      []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Action  string `json:"action"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		chatWriteError(w, 400, "invalid body")
		return
	}

	pluginDir := filepath.Base(req.PluginDir)
	if pluginDir == "" || pluginDir == "." || pluginDir == ".." {
		chatWriteError(w, 400, "pluginDir is required")
		return
	}

	dir := h.pluginRepoDir(memberID, pluginDir)
	if err := h.ensureRepo(memberID, pluginDir); err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}

	head, _ := gitOutput(dir, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)
	if req.BaseCommit != "" && req.BaseCommit != head {
		err := gitExec(dir, "merge-base", "--is-ancestor", req.BaseCommit, "HEAD")
		if err != nil {
			chatWriteJSON(w, 409, map[string]interface{}{
				"conflict":   true,
				"headCommit": head,
			})
			return
		}
	}

	for _, f := range req.Files {
		fullPath := filepath.Join(dir, f.Path)
		if f.Action == "delete" {
			os.Remove(fullPath)
		} else {
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			os.WriteFile(fullPath, []byte(f.Content), 0644)
		}
	}

	msg := req.Message
	if msg == "" {
		msg = "client push"
	}
	commitHash, err := h.commit(memberID, pluginDir, msg, "client-sync")
	if err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}

	if h.rebuilder != nil {
		go func() {
			if _, err := h.rebuilder.Rebuild(r.Context(), RebuildReq{MemberID: memberID, PluginDir: pluginDir}); err != nil {
				log.Printf("[PluginGit] rebuild failed: %s/%s: %v", memberID, pluginDir, err)
			}
		}()
	}

	chatWriteJSON(w, 200, map[string]string{"commitHash": commitHash})
}

// HandleLog returns recent commits for a specific plugin repo.
// GET /api/vault/plugins/git/log?plugin={dir}&limit=20
func (h *PluginGitHandler) HandleLog(w http.ResponseWriter, r *http.Request) {
	memberID, ok := memberIDFromHeader(w, r)
	if !ok {
		return
	}
	pluginDir, ok := requirePluginParam(w, r)
	if !ok {
		return
	}
	dir := h.pluginRepoDir(memberID, pluginDir)
	if err := h.ensureRepo(memberID, pluginDir); err != nil {
		chatWriteError(w, 500, err.Error())
		return
	}

	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	output, err := gitOutput(dir, "log", "--format=%H|%s|%an|%aI", "-"+limit)
	if err != nil {
		chatWriteJSON(w, 200, []interface{}{})
		return
	}

	var entries []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) == 4 {
			entries = append(entries, map[string]string{
				"hash":    parts[0],
				"message": parts[1],
				"author":  parts[2],
				"date":    parts[3],
			})
		}
	}
	if entries == nil {
		entries = []map[string]string{}
	}
	chatWriteJSON(w, 200, entries)
}

// --- Git helpers ---

func gitExec(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=CubeLV", "GIT_AUTHOR_EMAIL=noreply@cubelv.com",
		"GIT_COMMITTER_NAME=CubeLV", "GIT_COMMITTER_EMAIL=noreply@cubelv.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, string(out))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=CubeLV", "GIT_AUTHOR_EMAIL=noreply@cubelv.com",
		"GIT_COMMITTER_NAME=CubeLV", "GIT_COMMITTER_EMAIL=noreply@cubelv.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
