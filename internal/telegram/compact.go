package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pink-tools/pink-core/log"

	"pink-agent/internal/claude"
	"pink-agent/internal/state"
	"pink-agent/internal/store"
)

const summaryTimeout = 120 * time.Second

const summaryPrompt = "Summarize this conversation for context transfer. Include: current task, key decisions, files modified, current state, pending work. Be concise."

// compactSession handles the full compaction flow:
// 1. Kill current process (before Claude burns tokens on its own compaction)
// 2. Respawn with --resume + DISABLE_AUTO_COMPACT=true, ask for summary
// 3. Start fresh session with project context + summary + last user message
func (b *Bot) compactSession(ctx context.Context, threadID int) {
	// Set compacting flag
	b.mu.Lock()
	if b.compacting[threadID] {
		b.mu.Unlock()
		return
	}
	b.compacting[threadID] = true
	lastMsg := b.lastUserMsg[threadID]
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.compacting, threadID)
		b.mu.Unlock()
	}()

	project := b.state.GetProjectByThread(threadID)
	if project == nil {
		return
	}

	sessionID := project.SessionID
	projectID := project.ID

	// Kill current process and clean up output state
	b.claude.Stop(threadID)
	b.output.Cleanup(threadID)

	SendToThread(b.api, b.chatID, threadID, "Compacting session...", "", nil)

	// Get summary from the old session
	summary := b.getSummary(ctx, threadID, sessionID)

	// Start fresh session (no resume)
	if err := b.spawnClaude(threadID, "", nil); err != nil {
		log.Error(ctx, "compact: spawn failed", log.Attr{K: "error", V: err.Error()})
		SendToThread(b.api, b.chatID, threadID, "Compaction failed: "+err.Error(), "", nil)
		return
	}

	// Build init prompt with summary and last message
	initPrompt := buildCompactInitPrompt(projectID, summary, lastMsg, b.state, b.store)

	if err := b.claude.Send(threadID, initPrompt); err != nil {
		log.Error(ctx, "compact: init send failed", log.Attr{K: "error", V: err.Error()})
	}

	EditForumTopic(b.api, b.chatID, threadID, project.Name+" (compacted)")
	SendToThread(b.api, b.chatID, threadID, "Session compacted.", "", nil)
}

// getSummary respawns the old session with auto-compact disabled, asks for summary, returns result text.
// Uses a negative threadID as temp process key to avoid collision with the real thread.
func (b *Bot) getSummary(ctx context.Context, threadID int, sessionID string) string {
	if sessionID == "" {
		return ""
	}

	tempKey := -threadID
	resultCh := make(chan string, 1)

	extraEnv := []string{"DISABLE_AUTO_COMPACT=true"}

	err := b.claude.Start(tempKey, sessionID, extraEnv, func(ev claude.OutputEvent) {
		if ev.Type == "result" {
			var res struct {
				Result string `json:"result"`
			}
			json.Unmarshal(ev.Raw, &res)
			resultCh <- res.Result
		}
	})
	if err != nil {
		log.Error(ctx, "compact: summary spawn failed", log.Attr{K: "error", V: err.Error()})
		return ""
	}

	// Send summary prompt
	if err := b.claude.Send(tempKey, summaryPrompt); err != nil {
		log.Error(ctx, "compact: summary send failed", log.Attr{K: "error", V: err.Error()})
		b.claude.Stop(tempKey)
		return ""
	}

	// Wait for result with timeout
	var summary string
	select {
	case summary = <-resultCh:
	case <-time.After(summaryTimeout):
		log.Warn(ctx, "compact: summary timed out")
	}

	b.claude.Stop(tempKey)
	return summary
}

func buildCompactInitPrompt(projectID, summary, lastUserMsg string, stateMgr *state.Manager, fs *store.FileStore) string {
	parts := []string{"You are a new Pink Agent session."}

	if projectID != "" {
		if p := stateMgr.GetProject(projectID); p != nil {
			parts = append(parts, fmt.Sprintf("Project: %s", p.Name))
		}

		if ctx := readProjectContext(fs, projectID); ctx != "" {
			parts = append(parts, fmt.Sprintf("\nPROJECT CONTEXT:\n%s", ctx))
		}
	}

	if summary != "" {
		parts = append(parts, fmt.Sprintf("\nPREVIOUS SESSION SUMMARY:\n%s", summary))
	}

	parts = append(parts, "\nRead configuration: /Users/.claude/CLAUDE.md")

	if lastUserMsg != "" {
		parts = append(parts, fmt.Sprintf("\nUser's last message was:\n%s\n\nAnswer it.", lastUserMsg))
	}

	return strings.Join(parts, "\n")
}
