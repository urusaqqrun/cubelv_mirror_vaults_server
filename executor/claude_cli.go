package executor

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ClaudeExecutor 管理 Claude CLI process
type ClaudeExecutor struct {
	maxConcurrent int
	timeout       time.Duration
	vaultRoot     string
	sem           chan struct{} // semaphore 控制並發
	running       int32        // atomic 計數佔用的 semaphore slots

	mu        sync.Mutex
	processes map[string]*exec.Cmd // taskID → 正在執行的 cmd
}

func NewClaudeExecutor(maxConcurrent int, timeout time.Duration, vaultRoot string) *ClaudeExecutor {
	return &ClaudeExecutor{
		maxConcurrent: maxConcurrent,
		timeout:       timeout,
		vaultRoot:     vaultRoot,
		sem:           make(chan struct{}, maxConcurrent),
		processes:     make(map[string]*exec.Cmd),
	}
}

// ExecuteTask 啟動 Claude CLI 執行任務
// workDir 為用戶的 Vault 目錄路徑，scope 和 userID 會注入環境變數供 hooks 使用
func (e *ClaudeExecutor) ExecuteTask(ctx context.Context, taskID, workDir, instruction, scope, userID string) (string, error) {
	// 等待 semaphore（並發排隊）
	select {
	case e.sem <- struct{}{}:
		atomic.AddInt32(&e.running, 1)
		defer func() {
			<-e.sem
			atomic.AddInt32(&e.running, -1)
		}()
	case <-ctx.Done():
		return "", fmt.Errorf("task %s cancelled while waiting in queue", taskID)
	}

	// 建立帶超時的 context
	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// 產生 CLAUDE.md 指導檔案
	claudeMD := buildClaudeMD(instruction)

	// 構建 Claude CLI 命令
	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--output-format", "text",
		"-p", claudeMD,
	}

	cmd := exec.CommandContext(execCtx, "claude", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"TASK_SCOPE="+scope,
		"VAULT_USER_ID="+userID,
	)

	e.mu.Lock()
	e.processes[taskID] = cmd
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.processes, taskID)
		e.mu.Unlock()
	}()

	output, err := cmd.CombinedOutput()
	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("task %s timed out after %v", taskID, e.timeout)
	}
	if err != nil {
		return string(output), fmt.Errorf("claude cli error: %w\noutput: %s", err, string(output))
	}

	return string(output), nil
}

// Cancel 中斷指定任務
func (e *ClaudeExecutor) Cancel(taskID string) error {
	e.mu.Lock()
	cmd, ok := e.processes[taskID]
	e.mu.Unlock()

	if !ok {
		return nil
	}

	if cmd.Process != nil {
		log.Printf("[ClaudeExecutor] killing task %s (pid=%d)", taskID, cmd.Process.Pid)
		return cmd.Process.Kill()
	}
	return nil
}

// RunningCount 目前佔用 semaphore 的任務數
func (e *ClaudeExecutor) RunningCount() int {
	return int(atomic.LoadInt32(&e.running))
}

// AvailableSlots 可用的 semaphore 插槽數
func (e *ClaudeExecutor) AvailableSlots() int {
	return e.maxConcurrent - e.RunningCount()
}

// ActiveCount 目前正在執行的任務數（已啟動 CLI process）
func (e *ClaudeExecutor) ActiveCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.processes)
}

// StreamEvent represents a single event from the Claude CLI streaming output.
type StreamEvent struct {
	Type string // "stdout", "error", "done"
	Data string
}

// ExecuteTaskStream runs the Claude CLI in streaming mode (--output-format stream-json)
// and sends each stdout line as a StreamEvent to eventCh. The caller must consume eventCh.
func (e *ClaudeExecutor) ExecuteTaskStream(
	ctx context.Context,
	taskID, workDir, instruction, scope, userID string,
	eventCh chan<- StreamEvent,
) error {
	select {
	case e.sem <- struct{}{}:
		atomic.AddInt32(&e.running, 1)
		defer func() {
			<-e.sem
			atomic.AddInt32(&e.running, -1)
		}()
	case <-ctx.Done():
		return fmt.Errorf("task %s cancelled while waiting in queue", taskID)
	}

	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	claudeMD := buildClaudeMD(instruction)

	args := []string{
		"--print",
		"--verbose",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"-p", claudeMD,
	}

	cmd := exec.CommandContext(execCtx, "claude", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"TASK_SCOPE="+scope,
		"VAULT_USER_ID="+userID,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	e.mu.Lock()
	e.processes[taskID] = cmd
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.processes, taskID)
		e.mu.Unlock()
	}()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start claude cli: %w", err)
	}

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 256*1024), 256*1024)
		for scanner.Scan() {
			stderrBuf.WriteString(scanner.Text() + "\n")
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		select {
		case eventCh <- StreamEvent{Type: "stdout", Data: line}:
		case <-execCtx.Done():
			return execCtx.Err()
		}
	}

	<-stderrDone
	waitErr := cmd.Wait()

	if execCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("task %s timed out after %v", taskID, e.timeout)
	}
	if waitErr != nil {
		errMsg := stderrBuf.String()
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		return fmt.Errorf("claude cli exit code %d: %s", cmd.ProcessState.ExitCode(), errMsg)
	}

	return nil
}

func buildClaudeMD(instruction string) string {
	aiServiceURL := os.Getenv("AI_SERVICE_URL")
	if aiServiceURL == "" {
		aiServiceURL = "http://chatbot.svc.local:8000"
	}

	var sb strings.Builder
	sb.WriteString("你是 NoteCEO Vault 的 AI 助手。\n")
	sb.WriteString("你正在操作一個包含用戶資料的檔案系統。\n\n")
	sb.WriteString("## 重要：先讀 .schemas/_index.json\n\n")
	sb.WriteString("建立任何 item 之前，**必須**先讀 .schemas/_index.json 確認正確的 itemType 名稱和欄位。\n")
	sb.WriteString("不要自己猜 itemType，只用 .schemas/ 裡列出的名稱（如 NOTE、NOTE_FOLDER、CARD、CARD_FOLDER、TODO、TODO_FOLDER 等）。\n\n")
	sb.WriteString("目錄結構：\n")
	sb.WriteString("頂層目錄名稱對應 itemType 的前綴（NOTE_FOLDER 和 NOTE 都在 NOTE/ 目錄下）。\n")
	sb.WriteString("每個 item 都是 {name}.json，有子項就有同名目錄。\n\n")
	sb.WriteString("## JSON 格式\n\n")
	sb.WriteString("每個 .json 檔案格式如下（所有欄位放在頂層）：\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\"id\":\"24字元hex\",\"name\":\"名稱\",\"itemType\":\"NOTE\",\"parentID\":\"父項ID或null\",\"version\":1,\"createdAt\":\"毫秒時間戳\",\"updatedAt\":\"毫秒時間戳\",\"content\":\"內容\",\"tags\":[]}\n")
	sb.WriteString("```\n")
	sb.WriteString("id 用 24 字元 hex（如 `a3f8c21d4e9b70500000001`）。createdAt/updatedAt 用 Unix 毫秒時間戳字串。\n\n")
	sb.WriteString("規則：\n")
	sb.WriteString("1. 不要刪除任何 .json 中的 id、parentID 欄位\n")
	sb.WriteString("2. 搬移 item 時更新 parentID\n")
	sb.WriteString("3. 改名 item 時同步調整 .json 與同名子目錄\n")
	sb.WriteString("4. 使用 Bash 時只用明確的絕對路徑\n")
	sb.WriteString("5. 建立資料夾型 item（如 CARD_FOLDER）時，parentID 設為 null\n")
	sb.WriteString("6. 建立子 item（如 CARD）時，parentID 設為資料夾的 id\n\n")

	// AI Service API（通用工具）
	sb.WriteString("## AI Service API\n\n")
	sb.WriteString("API base URL: " + aiServiceURL + "\n\n")
	sb.WriteString("### 圖片搜尋\n")
	sb.WriteString("搜尋圖片 URL（有 retry 機制，自動驗證 URL 可用性）：\n")
	sb.WriteString("```bash\n")
	sb.WriteString("curl -s -X POST " + aiServiceURL + "/cubelv/search_card_image \\\n")
	sb.WriteString("  -H 'Content-Type: application/json' \\\n")
	sb.WriteString("  -d '{\"query\":\"搜尋關鍵詞\"}'\n")
	sb.WriteString("```\n")
	sb.WriteString("回傳：`{\"imageUrl\":\"...\",\"title\":\"...\",\"source\":\"...\"}`\n\n")
	sb.WriteString("## 各 item type 的特殊規則\n\n")
	sb.WriteString("讀 .schemas/_index.json，每個 type 可能有 aiHints 欄位，列出該 type 的特殊操作指示。\n")
	sb.WriteString("aiHints 中的 {AI_SERVICE_URL} 請替換為：" + aiServiceURL + "\n\n")
	sb.WriteString("用戶指令：\n")
	sb.WriteString(instruction)
	return sb.String()
}
