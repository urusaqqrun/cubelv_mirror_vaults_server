package api

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/urusaqqrun/vault-mirror-service/executor"
)

const (
	debugLogPath    = "/Users/run/CubeLV/.cursor/debug-2de599.log"
	debugSessionID  = "2de599"
	debugRunInitial = "initial"
)

var debugLogMu sync.Mutex

func debugMirrorLog(location, message, runID, hypothesisID string, data map[string]interface{}) {
	entry := map[string]interface{}{
		"sessionId":    debugSessionID,
		"runId":        runID,
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    unixMilliNow(),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	debugLogMu.Lock()
	defer debugLogMu.Unlock()
	f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func debugToolCallCount(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var toolCalls []map[string]interface{}
	if err := json.Unmarshal(raw, &toolCalls); err != nil {
		return 0
	}
	return len(toolCalls)
}

func debugSessionMessageSummary(messages []executor.SessionMessage) []map[string]interface{} {
	if len(messages) == 0 {
		return nil
	}
	start := 0
	if len(messages) > 6 {
		start = len(messages) - 6
	}
	summary := make([]map[string]interface{}, 0, len(messages)-start)
	for _, msg := range messages[start:] {
		summary = append(summary, map[string]interface{}{
			"id":            msg.ID,
			"role":          msg.Role,
			"contentLen":    len(msg.Content),
			"thinkingLen":   len(msg.Thinking),
			"toolCallCount": debugToolCallCount(msg.ToolCalls),
			"toolCallID":    msg.ToolCallID,
		})
	}
	return summary
}

func unixMilliNow() int64 {
	return time.Now().UnixMilli()
}

