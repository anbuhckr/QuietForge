package session

import (
	"context"
	"encoding/json"
	"fmt"
	"quietforge/provider"
	"slices"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

type MessagePart struct {
	Type       string         `json:"type"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Arguments  any         	  `json:"arguments,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Attachments []map[string]any `json:"attachments,omitempty"`
}

type Message struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Role      string         `json:"role"`
	Parts     []MessagePart  `json:"parts"`
	CreatedAt    int64          `json:"created_at"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CachedTokens int            `json:"-"`
}

func CompactMessages(ctx context.Context, messages []Message, config map[string]any, modelContext int, client *provider.Client, onProgress func(string)) []Message {
	usable := GetUsableContextWindow(modelContext, config)
	totalTokens := EstimateTokens(messages)
	if !NeedsCompaction(totalTokens, usable) {
		return messages
	}
	pruneTarget := GetPruneTarget(totalTokens, usable, config)
	pruneEnabled := false
	if p, ok := config["prune"].(bool); ok {
		pruneEnabled = p
	}
	if pruneEnabled {
		return PruneMessages(messages, config, pruneTarget)
	}
	if client != nil {
		return CompactWithLLM(ctx, messages, config, pruneTarget, client, onProgress)
	}
	return PruneMessages(messages, config, pruneTarget)
}

func SerializeMessage(msg Message, truncationLimit int) string {
	switch msg.Role {

	case "user":
		var b strings.Builder

		for _, p := range msg.Parts {
			switch p.Type {
			case "text":
				b.WriteString(p.Content)

			case "file":
				b.WriteString("\n[Attached file: ")
				b.WriteString(p.Content)
				if len(p.Metadata) > 0 {
					metaBytes, _ := json.Marshal(p.Metadata)
					metaStr := string(metaBytes)
					if len(metaStr) > truncationLimit {
						metaStr = metaStr[:truncationLimit] + "...[truncated]"
					}
					b.WriteString(" Metadata: ")
					b.WriteString(metaStr)
				}
				if len(p.Attachments) > 0 {
					attBytes, _ := json.Marshal(p.Attachments)
					attStr := string(attBytes)
					if len(attStr) > truncationLimit {
						attStr = attStr[:truncationLimit] + "...[truncated]"
					}
					b.WriteString(" Attachments: ")
					b.WriteString(attStr)
				}
				b.WriteString("]")
			}
		}

		return "[User]: " + b.String()

	case "assistant":
		var lines []string

		for _, p := range msg.Parts {
			switch p.Type {

			case "text":
				lines = append(lines,
					fmt.Sprintf("[Assistant]: %s", p.Content),
				)

			case "tool_use":
				var args string

				switch v := p.Arguments.(type) {
				case string:
					args = v
				default:
					data, _ := json.Marshal(v)
					args = string(data)
				}

				if len(args) > truncationLimit {
					args = args[:truncationLimit] + "...[truncated]"
				}

				lines = append(lines,
					fmt.Sprintf("[Assistant tool call]: %s(%s)", p.ToolName, args),
				)

			case "compaction":
			// Skip compaction entries in message serialization -
			// they are internal metadata, not displayed to the model.
			}
		}

		return strings.Join(lines, "\n")

	case "tool":
		var lines []string

		for _, p := range msg.Parts {
			if p.Type != "tool_result" {
				continue
			}

			content := p.Content
			if len(content) > truncationLimit {
				content = content[:truncationLimit] + "\n[truncated]"
			}

			var attachStr string
			if len(p.Attachments) > 0 {
				attBytes, _ := json.Marshal(p.Attachments)
				attachStr = "\n[Attachments]: " + string(attBytes)
				if len(attachStr) > truncationLimit {
					attachStr = attachStr[:truncationLimit] + "\n...[truncated]"
				}
			}

			lines = append(lines,
				fmt.Sprintf("[Tool result]: %s%s", content, attachStr),
			)
		}

		return strings.Join(lines, "\n")

	case "system":
		if len(msg.Parts) > 0 {
			return "[System update]: " + msg.Parts[0].Content
		}
		return "[System update]: "

	default:
		return ""
	}
}

func CompactWithLLM(ctx context.Context, messages []Message, config map[string]any, targetTokens int, client *provider.Client, onProgress func(string)) []Message {
	if len(messages) == 0 {
		return messages
	}

	tailStart := findTailStart(messages, GetTailTurns(config))

	if tailStart <= 0 {
		return PruneMessages(messages, config, targetTokens)
	}

	// Stage 1: Try to fit the tail by compressing inner content.
	// Stage 2: If it still doesn't fit, push older turns to the head for summarization.
	var recent []Message
	for tailStart < len(messages)-1 {
		recent = compressMessages(messages[tailStart:], targetTokens-2000)
		if EstimateTokens(recent) <= targetTokens-2000 {
			break
		}
		
		// If recent is still too big, push one more turn into the head to be summarized!
		nextUser := tailStart + 1
		for nextUser < len(messages) && messages[nextUser].Role != "user" {
			nextUser++
		}
		if nextUser >= len(messages) {
			break // Always keep at least the last user turn in the tail
		}
		tailStart = nextUser
	}
	
	if recent == nil {
		recent = compressMessages(messages[tailStart:], targetTokens-2000)
	}

	head := messages[:tailStart]

	var previousSummary string
	headContextMsgs := make([]Message, 0)
	updateCount := 0

	for _, msg := range head {
		isCompaction := false

		for _, part := range msg.Parts {
			if part.Type == "compaction" {
				previousSummary = part.Content
				if msg.Metadata != nil {
					switch v := msg.Metadata["update_count"].(type) {
					case int:
						updateCount = v
					case int64:
						updateCount = int(v)
					case float64:
						updateCount = int(v)
					}
				}
				isCompaction = true
				break
			}
		}

		if isCompaction {
			// A compaction summary encapsulates everything before it.
			// Discard the old raw messages to ensure truly incremental summarization.
			headContextMsgs = nil
		} else {
			headContextMsgs = append(headContextMsgs, msg)
		}
	}

	var contexts []string
	limit := GetToolTruncationLimit(config)
	for _, msg := range headContextMsgs {
		s := SerializeMessage(msg, limit)
		if s != "" {
			contexts = append(contexts, s)
		}
	}
	contextStr := strings.Join(contexts, "\n\n")

	var promptText string
	jsonSchema := `CRITICAL: You MUST strictly output ONLY a valid JSON object with the following schema:
{
  "user_requests": ["Brief 1-line summaries of recent user requests/prompts"],
  "outstanding_requests": ["1-3 brief tasks remaining to achieve the current goal"],
  "work_accomplished": ["1-3 brief completed steps"],
  "files_and_code": {
    "edited_files": ["Modified files"],
    "viewed_files": ["Read files"]
  },
  "current_work_and_next_steps": "A very brief, single-sentence summary of the current work and immediate next step."
}
Keep all text descriptions and array items extremely concise and short. Do not include markdown formatting or any other text outside the JSON object.`

	if previousSummary != "" {
		summaryTokens := EstimateTokens([]Message{
			{
				Role: "user",
				Parts: []MessagePart{
					{
						Type:    "compaction",
						Content: previousSummary,
					},
				},
			},
		})
		
		if summaryTokens > GetSummaryReserve(config)/2 || updateCount >= 8 {
			// Hard garbage collection trigger
			promptText = fmt.Sprintf(
				`The anchored summary below has grown too large or stale. You MUST perform a hard garbage collection.
Treat the previous summary as potentially stale.
Your goal is NOT to preserve historical details. Reconstruct the absolute smallest working memory needed to continue.
CRITICAL RULES FOR REWRITE:
1. Keep the JSON summary extremely small. Keep descriptions to a single short line.
2. Drop ALL completed tasks, old 'work_accomplished', and stale 'files_and_code' that are no longer strictly necessary.
3. Keep ONLY the most active unresolved tasks and immediate next steps.
4. Be absolutely ruthless in trimming fat.
%s
<previous-summary>
%s
</previous-summary>

%s`,
				jsonSchema,
				previousSummary,
				contextStr,
			)
			updateCount = 1 // Reset after GC
		} else {
			// Normal update
			promptText = fmt.Sprintf(
				`Update the anchored summary below using the conversation history above.
Preserve still-true details, remove stale details, and merge in the new facts.
CRITICAL: Keep descriptions and tasks extremely short and concise (e.g. single-line bullet points) to save context window space.
%s
<previous-summary>
%s
</previous-summary>

%s`,
				jsonSchema,
				previousSummary,
				contextStr,
			)
			updateCount++
		}
	} else {
		promptText = fmt.Sprintf(
			`Create a new anchored summary from the conversation history. Keep it extremely concise.
%s

%s`,
			jsonSchema,
			contextStr,
		)
		updateCount = 1
	}

	compactionSystem := LoadPromptTemplate("compaction_system")
	if compactionSystem == "" {
		compactionSystem = "You are an anchored context summarization assistant for coding sessions."
	}

	systemMsg := Message{
		ID:        "sys-compaction",
		SessionID: "",
		Role:      "system",
		Parts: []MessagePart{
			{
				Type:    "text",
				Content: compactionSystem,
			},
		},
	}

	userMsg := Message{
		ID:        "user-compaction",
		SessionID: "",
		Role:      "user",
		Parts: []MessagePart{
			{
				Type:    "text",
				Content: promptText,
			},
		},
	}

	llmMsgs := ToOpenAIMessages([]Message{
		systemMsg,
		userMsg,
	}, false)

	var builder strings.Builder

	if onProgress != nil {
		onProgress("Compacting memory...")
	}
	defer func() {
		if onProgress != nil {
			onProgress("Compaction complete")
		}
	}()

	ev, err := client.Stream(ctx, llmMsgs, []openai.Tool{})
	if err != nil {
		return PruneMessages(messages, config, targetTokens)
	}

	for e := range ev {
		if e.Type == "text" && e.Text != "" {
			builder.WriteString(e.Text)
		}
	}

	newSummary := strings.TrimSpace(builder.String())
	if newSummary == "" {
		return PruneMessages(messages, config, targetTokens)
	}

	cleanJson := strings.TrimPrefix(newSummary, "```json")
	cleanJson = strings.TrimPrefix(cleanJson, "```")
	cleanJson = strings.TrimSuffix(cleanJson, "```")
	cleanJson = strings.TrimSpace(cleanJson)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleanJson), &parsed); err != nil {
		return PruneMessages(messages, config, targetTokens)
	}

	if _, ok := parsed["user_requests"].([]any); !ok {
		return PruneMessages(messages, config, targetTokens)
	}
	if _, ok := parsed["outstanding_requests"].([]any); !ok {
		return PruneMessages(messages, config, targetTokens)
	}
	if _, ok := parsed["work_accomplished"].([]any); !ok {
		return PruneMessages(messages, config, targetTokens)
	}
	if _, ok := parsed["files_and_code"].(map[string]any); !ok {
		return PruneMessages(messages, config, targetTokens)
	}
	if _, ok := parsed["current_work_and_next_steps"].(string); !ok {
		return PruneMessages(messages, config, targetTokens)
	}

	var newSummaryStr string
	if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
		newSummaryStr = string(pretty)
	} else {
		return PruneMessages(messages, config, targetTokens)
	}

	now := time.Now().UnixMilli()

	compactionMsg := Message{
		ID:        fmt.Sprintf("compaction-%d", now),
		SessionID: messages[0].SessionID,
		Role:      "user",
		CreatedAt: now,
		Parts: []MessagePart{
			{
				Type:    "compaction",
				Content: newSummaryStr,
			},
		},
		Metadata: map[string]any{
			"update_count": float64(updateCount),
		},
	}
	turns := groupIntoTurns(head)
	var protectedBlocks [][]Message

	for _, turn := range turns {
		if hasProtectedTool(turn) {
			protectedBlocks = append(protectedBlocks, turn)
		}
	}

	recentTokens := EstimateTokens(recent)
	budget := targetTokens - recentTokens
	if budget < 0 {
		budget = 0
	}

	var protectedMsgs []Message
	for i := len(protectedBlocks) - 1; i >= 0; i-- {
		block := protectedBlocks[i]
		blockTokens := EstimateTokens(block)
		if budget >= blockTokens {
			budget -= blockTokens
			protectedMsgs = append(block, protectedMsgs...)
		} else {
			break
		}
	}

	result := make([]Message, 0, len(protectedMsgs)+1+len(recent))
	seenMessageIDs := make(map[string]struct{})

	for _, msg := range protectedMsgs {
		if _, ok := seenMessageIDs[msg.ID]; !ok {
			seenMessageIDs[msg.ID] = struct{}{}
			result = append(result, msg)
		}
	}

	if _, ok := seenMessageIDs[compactionMsg.ID]; !ok {
		seenMessageIDs[compactionMsg.ID] = struct{}{}
		result = append(result, compactionMsg)
	}

	for _, msg := range recent {
		if _, ok := seenMessageIDs[msg.ID]; !ok {
			seenMessageIDs[msg.ID] = struct{}{}
			result = append(result, msg)
		}
	}

	if EstimateTokens(result) > targetTokens {
		return compressMessages(result, targetTokens)
	}

	return result
}

func PruneMessages(messages []Message, config map[string]any, targetTokens int) []Message {
	if len(messages) == 0 {
		return messages
	}

	protected := make(map[int]struct{})
	
	allTurns := groupIntoTurns(messages)
	msgIndex := 0
	for _, turn := range allTurns {
		turnIsProtected := hasProtectedTool(turn)
		for range turn {
			if turnIsProtected {
				protected[msgIndex] = struct{}{}
			}
			msgIndex++
		}
	}

	tailStart := findTailStart(messages, GetTailTurns(config))

	var result []Message
	keptTokens := 0
	keptToolCallIDs := make(map[string]struct{})
	seenMessageIDs := make(map[string]struct{})

	for i := len(messages) - 1; i >= tailStart; i-- {
		msg := messages[i]
		msgTokens := EstimateTokens([]Message{msg})
		_, isProtected := protected[i]

		keep := false
		if keptTokens+msgTokens <= targetTokens || isProtected {
			keep = true
		}

		if msg.Role == "assistant" && !isProtected {
			hasKeptResult := false
			missingKeptResult := false

			for _, part := range msg.Parts {
				if part.Type == "tool_use" {
					if _, ok := keptToolCallIDs[part.ToolCallID]; ok {
						hasKeptResult = true
					} else {
						missingKeptResult = true
					}
				}
			}

			if hasKeptResult {
				keep = true // Force keep to prevent dangling tool result
			} else if missingKeptResult {
				keep = false // Drop if we dropped the tool result
			}
		}

		if keep || isProtected {
			if _, ok := seenMessageIDs[msg.ID]; !ok {
				seenMessageIDs[msg.ID] = struct{}{}
				result = append([]Message{msg}, result...)
			}
			keptTokens += msgTokens

			if msg.Role == "tool" {
				for _, part := range msg.Parts {
					if part.Type == "tool_result" {
						keptToolCallIDs[part.ToolCallID] = struct{}{}
					}
				}
			}
		}
	}

	if keptTokens < targetTokens && tailStart > 0 {
		budget := targetTokens - keptTokens
		
		turns := groupIntoTurns(messages[:tailStart])
		var protectedBlocks [][]Message
		
		for _, turn := range turns {
			if hasProtectedTool(turn) {
				protectedBlocks = append(protectedBlocks, turn)
			}
		}

		for i := len(protectedBlocks) - 1; i >= 0; i-- {
			block := protectedBlocks[i]
			blockTokens := EstimateTokens(block)
			if budget >= blockTokens {
				budget -= blockTokens
				var uniqueBlock []Message
				for _, msg := range block {
					if _, ok := seenMessageIDs[msg.ID]; !ok {
						seenMessageIDs[msg.ID] = struct{}{}
						uniqueBlock = append(uniqueBlock, msg)
					}
				}
				result = append(uniqueBlock, result...)
			} else {
				break
			}
		}
	}

	// Safety fallback.
	if len(result) < 2 && len(messages) > 0 {
		start := 0
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				start = i
				break
			}
		}
		result = append([]Message(nil), messages[start:]...)
	}

	return compressMessages(result, targetTokens)
}

func EstimateTokens(messages []Message) int {
	total := 0
	for i := range messages {
		msg := &messages[i]
		if msg.CachedTokens > 0 {
			total += msg.CachedTokens
			continue
		}

		parts := make([]map[string]any, 0, len(msg.Parts))
		for _, p := range msg.Parts {
			parts = append(parts, map[string]any{
				"type":        p.Type,
				"content":     p.Content,
				"arguments":   p.Arguments,
				"metadata":    p.Metadata,
				"attachments": p.Attachments,
			})
		}
		content, err := json.Marshal(map[string]any{
			"role":  msg.Role,
			"parts": parts,
		})
		if err != nil {
			continue
		}
		
		tokens := len(content) / 4
		if tokens == 0 {
			tokens = 1 // Ensure >0 so we don't recalculate 0-token messages
		}
		msg.CachedTokens = tokens
		total += tokens
	}
	return total
}

func groupIntoTurns(messages []Message) [][]Message {
	var turns [][]Message
	var current []Message

	for _, msg := range messages {
		if msg.Role == "user" {
			if len(current) > 0 {
				turns = append(turns, current)
			}
			current = []Message{msg}
		} else {
			current = append(current, msg)
		}
	}
	if len(current) > 0 {
		turns = append(turns, current)
	}
	return turns
}

func hasProtectedTool(turn []Message) bool {
	for _, msg := range turn {
		for _, part := range msg.Parts {
			if part.Type == "tool_use" && slices.Contains(ProtectedTools, part.ToolName) {
				return true
			}
		}
	}
	return false
}

func findTailStart(messages []Message, tailTurns int) int {
	userTurns := 0
	tailStart := 0

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userTurns++
			if userTurns >= tailTurns {
				tailStart = i
				break
			}
		}
	}

	if tailStart <= 0 && len(messages) > 1 {
		for i := 1; i < len(messages); i++ {
			if messages[i].Role == "user" {
				tailStart = i
				break
			}
		}
	}

	for tailStart > 0 && tailStart < len(messages) && messages[tailStart].Role == "tool" {
		tailStart--
	}
	return tailStart
}

func compressMessages(recent []Message, budget int) []Message {
	if EstimateTokens(recent) <= budget {
		return recent
	}

	compressed := make([]Message, len(recent))
	for i, msg := range recent {
		compressed[i] = msg
		compressed[i].Parts = make([]MessagePart, len(msg.Parts))
		copy(compressed[i].Parts, msg.Parts)
		// Reset cached tokens since we are modifying the parts
		compressed[i].CachedTokens = 0
	}

	// Level 1: Truncate tool results to 8000 chars (approx 2000 tokens)
	for i := range compressed {
		for j := range compressed[i].Parts {
			part := &compressed[i].Parts[j]
			if part.Type == "tool_result" && len(part.Content) > 8000 {
				part.Content = truncateToolResultSemantically(part.Content, 8000)
			}
		}
	}
	
	if EstimateTokens(compressed) <= budget {
		return compressed
	}

	// Level 2: Truncate tool results to 1000 chars and drop attachments
	for i := range compressed {
		for j := range compressed[i].Parts {
			part := &compressed[i].Parts[j]
			if part.Type == "tool_result" && len(part.Content) > 1000 {
				part.Content = truncateToolResultSemantically(part.Content, 1000)
			} else if part.Type == "file" {
				part.Content = part.Content + " (Attachment stripped for context limits)"
				part.Attachments = nil
				part.Metadata = nil
			}
		}
	}
	
	if EstimateTokens(compressed) <= budget {
		return compressed
	}

	// Level 3: Replace completely with semantic string
	for i := range compressed {
		for j := range compressed[i].Parts {
			part := &compressed[i].Parts[j]
			if part.Type == "tool_result" {
				originalSize := len(part.Content)
				part.Content = fmt.Sprintf("[Tool output truncated completely to preserve context limits. Original size: %d bytes. Semantic summary unavailable.]", originalSize)
			}
		}
	}

	return compressed
}

func truncateToolResultSemantically(content string, limit int) string {
	if len(content) <= limit {
		return content
	}

	// Check if JSON
	trimmed := strings.TrimSpace(content)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) || (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		halfLimit := limit / 2
		if halfLimit > 400 {
			halfLimit = 400
		}
		return content[:halfLimit] + "\n\n... [JSON data truncated to preserve context limits] ...\n\n" + content[len(content)-halfLimit:]
	}

	lines := strings.Split(content, "\n")
	var errorLines []string
	errorKeywords := []string{"error:", "fail", "failed", "panic:", "exception:", "undefined:", "err!", "exit status"}

	for _, line := range lines {
		lowerLine := strings.ToLower(line)
		isError := false
		for _, keyword := range errorKeywords {
			if strings.Contains(lowerLine, keyword) {
				isError = true
				break
			}
		}
		if isError {
			errorLines = append(errorLines, line)
			if len(errorLines) >= 15 {
				break
			}
		}
	}

	if len(errorLines) > 0 {
		var summary strings.Builder
		summary.WriteString(fmt.Sprintf("\n[Tool output truncated to %d chars. Found error/failure indicators in output:]\n", limit))
		for _, errLine := range errorLines {
			summary.WriteString(errLine)
			summary.WriteString("\n")
		}
		summary.WriteString("...\n[Final output tail:]\n")
		lastLinesStart := len(lines) - 5
		if lastLinesStart < 0 {
			lastLinesStart = 0
		}
		for i := lastLinesStart; i < len(lines); i++ {
			summary.WriteString(lines[i])
			summary.WriteString("\n")
		}
		
		res := summary.String()
		if len(res) <= limit+500 {
			return res
		}
	}

	halfLimit := limit / 2
	return content[:halfLimit] + fmt.Sprintf("\n\n[Tool output truncated to preserve context limits. Middle %d characters omitted.]\n\n", len(content)-limit) + content[len(content)-halfLimit:]
}
