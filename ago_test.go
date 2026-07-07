package ago_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/aleloro-dev/ago"
)

// --- helpers ---

type sequenceClient struct {
	mu        sync.Mutex
	responses []ago.Message
	calls     int
}

func (m *sequenceClient) Call(_ context.Context, _ string, _ []ago.Message, _ []ago.Tool) (ago.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls >= len(m.responses) {
		return ago.Message{}, fmt.Errorf("unexpected call %d", m.calls+1)
	}
	r := m.responses[m.calls]
	m.calls++
	return r, nil
}

type captureObserver struct {
	mu     sync.Mutex
	events []ago.Event
}

func (o *captureObserver) OnEvent(e ago.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *captureObserver) Events() []ago.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.events
}

type mockStore struct {
	saved []*ago.Session
	err   error
}

func (s *mockStore) Save(session *ago.Session) error {
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, session)
	return nil
}

func (s *mockStore) Load(id ago.ResourceID) (*ago.Session, error) {
	return nil, nil
}

type AddArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

// --- agent behavior ---

func TestAgentToolCall(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, ToolCalls: []ago.ToolCall{{ID: "1", Name: "add", Input: []byte(`{"a":42,"b":58}`)}}},
		{Role: ago.RoleAssistant, Content: "42 + 58 = 100"},
	}}
	add := ago.NewTool("add", "Add two integers", func(_ context.Context, args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})
	agent := ago.NewAgent("test", client, []ago.Tool{add})
	result, err := agent.NewSession().Send(context.Background(), "What is 42 + 58?")
	if err != nil {
		t.Fatal(err)
	}
	if result != "42 + 58 = 100" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestParallelToolCalls(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{
			Role: ago.RoleAssistant,
			ToolCalls: []ago.ToolCall{
				{ID: "1", Name: "tool_a", Input: []byte(`{}`)},
				{ID: "2", Name: "tool_b", Input: []byte(`{}`)},
			},
		},
		{Role: ago.RoleAssistant, Content: "done"},
	}}

	var mu sync.Mutex
	var called []string
	type Empty struct{}

	toolA := ago.NewTool("tool_a", "tool a", func(_ context.Context, _ Empty) (string, error) {
		mu.Lock()
		called = append(called, "tool_a")
		mu.Unlock()
		return "a", nil
	})
	toolB := ago.NewTool("tool_b", "tool b", func(_ context.Context, _ Empty) (string, error) {
		mu.Lock()
		called = append(called, "tool_b")
		mu.Unlock()
		return "b", nil
	})

	agent := ago.NewAgent("test", client, []ago.Tool{toolA, toolB})
	result, err := agent.NewSession().Send(context.Background(), "use both")
	if err != nil {
		t.Fatal(err)
	}
	if result != "done" {
		t.Fatalf("expected 'done', got %s", result)
	}
	if len(called) != 2 {
		t.Fatalf("expected 2 tools called, got %d", len(called))
	}
}

func TestToolNotFound(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, ToolCalls: []ago.ToolCall{{ID: "1", Name: "missing", Input: []byte(`{}`)}}},
		{Role: ago.RoleAssistant, Content: "ok"},
	}}
	agent := ago.NewAgent("test", client, nil)
	session := agent.NewSession()
	_, err := session.Send(context.Background(), "call missing tool")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range session.History {
		if m.Role == ago.RoleToolResult && m.Content == `error: tool "missing" not found` {
			found = true
		}
	}
	if !found {
		t.Fatal("expected tool not found error in history")
	}
}

func TestMaxIterations(t *testing.T) {
	responses := make([]ago.Message, 20)
	for i := range responses {
		responses[i] = ago.Message{
			Role:      ago.RoleAssistant,
			ToolCalls: []ago.ToolCall{{ID: "1", Name: "add", Input: []byte(`{"a":1,"b":2}`)}},
		}
	}
	client := &sequenceClient{responses: responses}
	add := ago.NewTool("add", "add", func(_ context.Context, args AddArgs) (string, error) {
		return "3", nil
	})
	agent := ago.NewAgent("test", client, []ago.Tool{add})
	agent.MaxIter = 3
	_, err := agent.NewSession().Send(context.Background(), "loop")
	if err == nil {
		t.Fatal("expected max iterations error")
	}
}

func TestToolError(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, ToolCalls: []ago.ToolCall{{ID: "1", Name: "fail", Input: []byte(`{}`)}}},
		{Role: ago.RoleAssistant, Content: "handled"},
	}}
	type Empty struct{}
	fail := ago.NewTool("fail", "always fails", func(_ context.Context, _ Empty) (string, error) {
		return "", errors.New("something went wrong")
	})
	agent := ago.NewAgent("test", client, []ago.Tool{fail})
	session := agent.NewSession()
	result, err := session.Send(context.Background(), "fail please")
	if err != nil {
		t.Fatal(err)
	}
	if result != "handled" {
		t.Fatalf("expected 'handled', got %s", result)
	}
	var found bool
	for _, m := range session.History {
		if m.Role == ago.RoleToolResult && m.Content == "error: something went wrong" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected tool error in history")
	}
}

// --- session ---

func TestMultiTurnSession(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, Content: "Paris"},
		{Role: ago.RoleAssistant, Content: "It will be sunny"},
	}}
	agent := ago.NewAgent("test", client, nil)
	session := agent.NewSession()

	r1, err := session.Send(context.Background(), "Where is the Eiffel Tower?")
	if err != nil {
		t.Fatal(err)
	}
	if r1 != "Paris" {
		t.Fatalf("expected 'Paris', got %s", r1)
	}
	r2, err := session.Send(context.Background(), "What's the weather there?")
	if err != nil {
		t.Fatal(err)
	}
	if r2 != "It will be sunny" {
		t.Fatalf("expected 'It will be sunny', got %s", r2)
	}
	var userMsgs int
	for _, m := range session.History {
		if m.Role == ago.RoleUser {
			userMsgs++
		}
	}
	if userMsgs != 2 {
		t.Fatalf("expected 2 user messages in history, got %d", userMsgs)
	}
}

// --- observer ---

func TestObserverEvents(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{
			Role:      ago.RoleAssistant,
			ToolCalls: []ago.ToolCall{{ID: "1", Name: "add", Input: []byte(`{"a":1,"b":2}`)}},
			Usage:     ago.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
		{Role: ago.RoleAssistant, Content: "3", Usage: ago.TokenUsage{InputTokens: 8, OutputTokens: 3}},
	}}
	add := ago.NewTool("add", "add", func(_ context.Context, args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})
	obs := &captureObserver{}
	agent := ago.NewAgent("test", client, []ago.Tool{add})
	agent.Observer = obs

	session := agent.NewSession()
	_, err := session.Send(context.Background(), "1+2")
	if err != nil {
		t.Fatal(err)
	}

	events := obs.Events()
	expected := []ago.EventType{ago.EventSessionStart, ago.EventModelCall, ago.EventToolCall, ago.EventModelCall}
	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(events))
	}
	for i, e := range events {
		if e.Type != expected[i] {
			t.Errorf("event[%d]: expected %s, got %s", i, expected[i], e.Type)
		}
		if e.SessionID != session.ID {
			t.Errorf("event[%d]: wrong session ID", i)
		}
		if e.ID == "" {
			t.Errorf("event[%d]: empty event ID", i)
		}
	}
	if events[2].Tool != "add" {
		t.Errorf("expected tool name 'add', got %s", events[2].Tool)
	}
}

// --- store ---

func TestStoreSave(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, Content: "hello"},
	}}
	store := &mockStore{}
	agent := ago.NewAgent("test", client, nil)
	agent.Store = store
	_, err := agent.NewSession().Send(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 save, got %d", len(store.saved))
	}
}

func TestStoreSaveError(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, Content: "hello"},
	}}
	store := &mockStore{err: errors.New("db down")}
	agent := ago.NewAgent("test", client, nil)
	agent.Store = store
	_, err := agent.NewSession().Send(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected store error to propagate")
	}
}

// --- token usage ---

func TestTokenUsageAccumulation(t *testing.T) {
	client := &sequenceClient{responses: []ago.Message{
		{Role: ago.RoleAssistant, Content: "one", Usage: ago.TokenUsage{InputTokens: 10, OutputTokens: 5}},
		{Role: ago.RoleAssistant, Content: "two", Usage: ago.TokenUsage{InputTokens: 8, OutputTokens: 3}},
	}}
	agent := ago.NewAgent("test", client, nil)
	session := agent.NewSession()
	session.Send(context.Background(), "first")
	session.Send(context.Background(), "second")

	if session.Usage.InputTokens != 18 {
		t.Fatalf("expected 18 input tokens, got %d", session.Usage.InputTokens)
	}
	if session.Usage.OutputTokens != 8 {
		t.Fatalf("expected 8 output tokens, got %d", session.Usage.OutputTokens)
	}
}

// --- schema ---

type schemaInner struct {
	Value string `json:"value"`
}

type schemaOuter struct {
	Name    string      `json:"name"`
	Score   float64     `json:"score,omitempty"`
	Inner   schemaInner `json:"inner"`
	Ptr     *schemaInner `json:"ptr,omitempty"`
	Tags    []string    `json:"tags"`
	secret  string
	Ignored string `json:"-"`
}

func TestSchemaGeneration(t *testing.T) {
	tool := ago.NewTool("test", "test", func(_ context.Context, _ schemaOuter) (string, error) {
		return "", nil
	})
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	required := schema["required"].([]any)

	requiresField := func(name string) bool {
		for _, r := range required {
			if r == name {
				return true
			}
		}
		return false
	}

	if !requiresField("name") {
		t.Error("expected 'name' to be required")
	}
	if requiresField("score") {
		t.Error("expected 'score' to not be required (omitempty)")
	}
	if requiresField("ptr") {
		t.Error("expected 'ptr' to not be required (omitempty pointer)")
	}
	if inner := props["inner"].(map[string]any); inner["type"] != "object" {
		t.Errorf("expected 'inner' to be object, got %v", inner["type"])
	}
	if ptr := props["ptr"].(map[string]any); ptr["type"] != "object" {
		t.Errorf("expected 'ptr' to be object, got %v", ptr["type"])
	}
	if tags := props["tags"].(map[string]any); tags["type"] != "array" {
		t.Errorf("expected 'tags' to be array, got %v", tags["type"])
	}
	if _, ok := props["secret"]; ok {
		t.Error("expected unexported field 'secret' to not be in schema")
	}
	if _, ok := props["ignored"]; ok {
		t.Error("expected json:\"-\" field to not be in schema")
	}
}
