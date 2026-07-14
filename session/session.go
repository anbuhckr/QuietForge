package session

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"quietforge/storage"
)

type SessionEvent struct {
	Type      string
	Data      map[string]any
	Timestamp int64
}

type EventHandler func(SessionEvent)

type Session struct {
	mu               sync.Mutex
	SessionID        string
	Repo             *storage.Repository
	AgentID          string
	Workspace        string
	Config           map[string]any
	Messages         []Message
	Todos            []storage.TodoRow
	Metadata         map[string]any
	ActiveToolCalls  map[string]any
	PromptTokens     int
	CompletionTokens int
	eventHandlers    []EventHandler
	loaded           bool
	PendingMessage   string
}

func NewSession(sessionID string, repo *storage.Repository, agentID string, config map[string]any, workspace string) *Session {
	if agentID == "" {
		agentID = "build"
	}
	if config == nil {
		config = make(map[string]any)
	}
	return &Session{
		SessionID:       sessionID,
		Repo:            repo,
		AgentID:         agentID,
		Workspace:       workspace,
		Config:          config,
		Messages:        make([]Message, 0),
		Todos:           make([]storage.TodoRow, 0),
		Metadata:        make(map[string]any),
		ActiveToolCalls: make(map[string]any),
		eventHandlers:   make([]EventHandler, 0),
	}
}

func (s *Session) OnEvent(handler EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventHandlers = append(s.eventHandlers, handler)
}

func (s *Session) Emit(eventType string, data map[string]any) {
	s.mu.Lock()
	handlers := make([]EventHandler, len(s.eventHandlers))
	copy(handlers, s.eventHandlers)
	s.mu.Unlock()

	event := SessionEvent{Type: eventType, Data: data, Timestamp: time.Now().UnixMilli()}
	for _, h := range handlers {
		h(event)
	}
}

func (s *Session) Load() error {
	if s.Repo == nil {
		return nil
	}
	sessionRow, err := s.Repo.GetSession(s.SessionID)
	if err != nil {
		return err
	}
	s.AgentID = sessionRow.AgentID
	s.PromptTokens = sessionRow.PromptTokens
	s.CompletionTokens = sessionRow.CompletionTokens

	msgRows, err := s.Repo.GetMessages(s.SessionID)
	if err != nil {
		return err
	}
	s.Messages = make([]Message, len(msgRows))
	for i, mr := range msgRows {
		msg := Message{
			ID:        mr.ID,
			SessionID: mr.SessionID,
			Role:      mr.Role,
			CreatedAt: mr.CreatedAt,
			Metadata:  mr.Metadata,
		}
		parts, _ := s.Repo.GetMessageParts(mr.ID)
		for _, p := range parts {
			msg.Parts = append(msg.Parts, MessagePart{
				Type:       p.Type,
				Content:    p.Content,
				ToolCallID: p.ToolCallID,
				ToolName:   p.ToolName,
				Arguments:  p.Arguments,
			})
		}
		s.Messages[i] = msg
	}

	s.Todos, err = s.Repo.ListTodos(s.SessionID)
	if err != nil {
		return err
	}
	s.loaded = true
	return nil
}

func (s *Session) Save() error {
	if s.Repo == nil {
		return nil
	}
	s.mu.Lock()
	meta := make(map[string]any)
	for k, v := range s.Metadata {
		meta[k] = v
	}
	s.mu.Unlock()

	sessionRow := storage.SessionRow{
		ID:               s.SessionID,
		AgentID:          s.AgentID,
		Workspace:        s.Workspace,
		CreatedAt:        time.Now().Unix(),
		UpdatedAt:        time.Now().Unix(),
		Metadata:         meta,
		PromptTokens:     s.PromptTokens,
		CompletionTokens: s.CompletionTokens,
	}
	if err := s.Repo.UpsertSession(sessionRow); err != nil {
		return err
	}
	s.Emit("session_saved", map[string]any{"session_id": s.SessionID})
	return nil
}
func (s *Session) QueueFollowup(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PendingMessage = text
}

func (s *Session) PopFollowup() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := s.PendingMessage
	s.PendingMessage = ""
	return msg
}

func (s *Session) AddTokens(prompt, completion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PromptTokens += prompt
	s.CompletionTokens += completion
}

func (s *Session) GetTokenTotals() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.PromptTokens, s.CompletionTokens
}

func (s *Session) Close() error {
	return s.Save()
}

func (s *Session) ReplaceMessages(msgs []Message) error {
	s.mu.Lock()
	s.Messages = make([]Message, len(msgs))
	copy(s.Messages, msgs)
	s.mu.Unlock()
	if s.Repo == nil {
		return nil
	}
	messageRows := make([]storage.MessageRow, 0, len(msgs))
	partsByMessage := make(map[string][]storage.MessagePartRow, len(msgs))
	for _, msg := range msgs {
		msgRow := storage.MessageRow{
			ID:        msg.ID,
			SessionID: msg.SessionID,
			Role:      msg.Role,
			CreatedAt: msg.CreatedAt,
			Metadata:  msg.Metadata,
		}
		partRows := make([]storage.MessagePartRow, len(msg.Parts))
		for j, p := range msg.Parts {
			args := ""
			if p.Arguments != nil {
				switch v := p.Arguments.(type) {
				case string:
					args = v
				default:
					b, _ := json.Marshal(v)
					args = string(b)
				}
			}
			partRows[j] = storage.MessagePartRow{
				MessageID:  msg.ID,
				Type:       p.Type,
				Content:    p.Content,
				ToolCallID: p.ToolCallID,
				ToolName:   p.ToolName,
				Arguments:  args,
			}
		}
		messageRows = append(messageRows, msgRow)
		partsByMessage[msg.ID] = partRows
	}
	return s.Repo.ReplaceMessages(s.SessionID, messageRows, partsByMessage)
}

func (s *Session) SetAgent(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AgentID = agentID
	s.Metadata["agent_id"] = agentID
}

func (s *Session) AddMessage(message Message) error {
	s.mu.Lock()
	s.Messages = append(s.Messages, message)
	s.mu.Unlock()

	if s.Repo != nil {
		if !s.loaded {
			if err := s.Save(); err != nil {
				s.mu.Lock()
				s.Messages = s.Messages[:len(s.Messages)-1]
				s.mu.Unlock()
				return err
			}
			s.loaded = true
		}

		msgRow := storage.MessageRow{
			ID:        message.ID,
			SessionID: message.SessionID,
			Role:      message.Role,
			CreatedAt: message.CreatedAt,
			Metadata:  message.Metadata,
		}
		partRows := make([]storage.MessagePartRow, len(message.Parts))
		for i, p := range message.Parts {
			partRows[i] = storage.MessagePartRow{
				MessageID:  message.ID,
				Type:       p.Type,
				Content:    p.Content,
				ToolCallID: p.ToolCallID,
				ToolName:   p.ToolName,
				Arguments: func() string {
					if p.Arguments == nil {
						return ""
					}
					switch v := p.Arguments.(type) {
					case string:
						return v
					default:
						b, _ := json.Marshal(v)
						return string(b)
					}
				}(),
			}
		}
		if err := s.Repo.CreateMessage(msgRow, partRows); err != nil {
			log.Printf("AddMessage: failed to persist message %s: %v", message.ID, err)
			s.mu.Lock()
			s.Messages = s.Messages[:len(s.Messages)-1]
			s.mu.Unlock()
			return err
		}

		partsJSON, _ := json.Marshal(message.Parts)
		metaJSON := []byte("{}")
		if message.Metadata != nil {
			metaJSON, _ = json.Marshal(message.Metadata)
		}
		if err := s.Repo.AppendDisplayMessage(s.SessionID, message.ID, message.Role, string(partsJSON), string(metaJSON), message.CreatedAt); err != nil {
			log.Printf("AddMessage: failed to append display_log for %s: %v", message.ID, err)
		}
	}
	return nil
}

func (s *Session) GetHistory(limit ...int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(limit) > 0 && limit[0] > 0 && limit[0] < len(s.Messages) {
		msgs := make([]Message, limit[0])
		copy(msgs, s.Messages[len(s.Messages)-limit[0]:])
		return msgs
	}
	msgs := make([]Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs
}


