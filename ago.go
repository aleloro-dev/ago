package ago

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type MessageRole string

const (
	RoleSystem     MessageRole = "system"
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "tool_result"
)

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

const (
	ResourceAgent   Resource = "agent"
	ResourceSession Resource = "session"
	ResourceEvent   Resource = "event"
)

type EventType string

const (
	EventSessionStart EventType = "session.start"
	EventModelCall    EventType = "model.call"
	EventToolCall     EventType = "tool.call"
)

// Struct for event data. Useful for monitoring and observability
type Event struct {
	ID         ResourceID
	Type       EventType
	SessionID  ResourceID
	AgentName  string
	Task       string
	Tool       string
	DurationMs int64
	Usage      TokenUsage
	Err        error
}

// Observer receives agent events for monitoring and tracing.
// OnEvent may be called concurrently — implementations must be safe for concurrent use.
type Observer interface {
	OnEvent(e Event)
}

// Default observer that logs events using slog
type slogObserver struct{}

func (o *slogObserver) OnEvent(e Event) {
	switch e.Type {
	case EventSessionStart:
		slog.Info("[session:start]", "session", e.SessionID, "agent", e.AgentName, "task", e.Task)
	case EventModelCall:
		slog.Info("[model:call]", "session", e.SessionID, "duration_ms", e.DurationMs, "input_tokens", e.Usage.InputTokens, "output_tokens", e.Usage.OutputTokens)
	case EventToolCall:
		slog.Info("[tool:call]", "session", e.SessionID, "tool", e.Tool, "duration_ms", e.DurationMs, "error", e.Err)
	}
}

type Message struct {
	Role       MessageRole
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Usage      TokenUsage
}

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

type ModelClient interface {
	Call(ctx context.Context, system string, history []Message, tools []Tool) (Message, error)
}

type Store interface {
	Save(s *Session) error
	Load(sessionID ResourceID) (*Session, error)
}

type Agent struct {
	ID           ResourceID
	Name         string
	SystemPrompt string
	MaxIter      int

	Model    ModelClient
	Tools    map[string]Tool
	Observer Observer
	Store    Store
}

func NewAgent(name string, model ModelClient, tools []Tool) *Agent {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Agent{ID: newID(ResourceAgent), Name: name, Model: model, Tools: m, Observer: &slogObserver{}}
}

type Session struct {
	ID      ResourceID
	History []Message
	Usage   TokenUsage
	agent   *Agent
}

func (a *Agent) NewSession() *Session {
	return &Session{ID: newID(ResourceSession), agent: a}
}

// ResumeSession restores a previous session from the agent's Store.
// Returns an error if no Store is configured or the session cannot be loaded.
func (a *Agent) ResumeSession(id ResourceID) (*Session, error) {
	if a.Store == nil {
		return nil, fmt.Errorf("no store configured")
	}
	return a.Store.Load(id)
}

func (s *Session) Send(ctx context.Context, task string) (string, error) {
	a := s.agent
	maxIter := a.MaxIter
	if maxIter == 0 {
		maxIter = 10
	}
	a.Observer.OnEvent(Event{ID: newID(ResourceEvent), Type: EventSessionStart, SessionID: s.ID, AgentName: a.Name, Task: task})
	s.History = append(s.History, Message{Role: RoleUser, Content: task})
	for range maxIter {
		t := time.Now()
		resp, err := a.Model.Call(ctx, a.SystemPrompt, s.History, a.toolList())
		if err != nil {
			slog.Error("[model:error]", "session", s.ID, "error", err)
			return "", err
		}
		s.History = append(s.History, resp)
		s.Usage.InputTokens += resp.Usage.InputTokens
		s.Usage.OutputTokens += resp.Usage.OutputTokens
		a.Observer.OnEvent(Event{ID: newID(ResourceEvent), Type: EventModelCall, SessionID: s.ID, AgentName: a.Name, DurationMs: time.Since(t).Milliseconds(), Usage: resp.Usage})
		if len(resp.ToolCalls) == 0 {
			if a.Store != nil {
				if err := a.Store.Save(s); err != nil {
					return "", err
				}
			}
			return resp.Content, nil
		}
		results := make([]string, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.ToolCalls {
			wg.Add(1)
			go func(i int, tc ToolCall) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						err := fmt.Errorf("tool panicked: %v", r)
						a.Observer.OnEvent(Event{ID: newID(ResourceEvent), Type: EventToolCall, SessionID: s.ID, AgentName: a.Name, Tool: tc.Name, Err: err})
						results[i] = fmt.Sprintf("error: %v", err)
					}
				}()
				t := time.Now()
				result, err := a.executeTool(ctx, tc)
				a.Observer.OnEvent(Event{ID: newID(ResourceEvent), Type: EventToolCall, SessionID: s.ID, AgentName: a.Name, Tool: tc.Name, DurationMs: time.Since(t).Milliseconds(), Err: err})
				if err != nil {
					results[i] = fmt.Sprintf("error: %v", err)
				} else {
					results[i] = result
				}
			}(i, tc)
		}
		wg.Wait()
		for i, tc := range resp.ToolCalls {
			s.History = append(s.History, Message{
				Role:       RoleToolResult,
				Content:    results[i],
				ToolCallID: tc.ID,
			})
		}
	}
	slog.Warn("[agent:limit]", "session", s.ID)
	return "", fmt.Errorf("max iterations reached")
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall) (string, error) {
	tool, ok := a.Tools[tc.Name]
	if !ok {
		return "", fmt.Errorf("tool %q not found", tc.Name)
	}
	return tool.Execute(ctx, tc.Input)
}

func (a *Agent) toolList() []Tool {
	list := make([]Tool, 0, len(a.Tools))
	for _, t := range a.Tools {
		list = append(list, t)
	}
	return list
}

type funcTool[T any] struct {
	name        string
	description string
	schema      json.RawMessage
	fn          func(context.Context, T) (string, error)
}

func (t *funcTool[T]) Name() string            { return t.name }
func (t *funcTool[T]) Description() string     { return t.description }
func (t *funcTool[T]) Schema() json.RawMessage { return t.schema }
func (t *funcTool[T]) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args T
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	return t.fn(ctx, args)
}

func NewTool[T any](name, description string, fn func(context.Context, T) (string, error)) Tool {
	var zero T
	return &funcTool[T]{
		name:        name,
		description: description,
		schema:      schemaOf(reflect.TypeOf(zero)),
		fn:          fn,
	}
}
