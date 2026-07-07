package ago

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func oaiOKResponse() []byte {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
	})
	return b
}

func antOKResponse() []byte {
	b, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "ok"}},
		"usage":   map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
	return b
}

func TestRetryOn429(t *testing.T) {
	retryBaseDelay = 0
	defer func() { retryBaseDelay = time.Second }()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(oaiOKResponse())
	}))
	defer srv.Close()

	c := NewClient(OpenAI, "test", "gpt-4o")
	c.BaseURL = srv.URL
	_, err := c.Call(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestRetryOn5xx(t *testing.T) {
	retryBaseDelay = 0
	defer func() { retryBaseDelay = time.Second }()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(oaiOKResponse())
	}))
	defer srv.Close()

	c := NewClient(OpenAI, "test", "gpt-4o")
	c.BaseURL = srv.URL
	_, err := c.Call(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	retryBaseDelay = 0
	defer func() { retryBaseDelay = time.Second }()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(400)
	}))
	defer srv.Close()

	c := NewClient(OpenAI, "test", "gpt-4o")
	c.BaseURL = srv.URL
	_, err := c.Call(context.Background(), "", nil, nil)
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts.Load())
	}
}

func TestRetryExhausted(t *testing.T) {
	retryBaseDelay = 0
	defer func() { retryBaseDelay = time.Second }()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(429)
	}))
	defer srv.Close()

	c := NewClient(OpenAI, "test", "gpt-4o")
	c.BaseURL = srv.URL
	_, err := c.Call(context.Background(), "", nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts.Load() != int32(maxRetries+1) {
		t.Fatalf("expected %d attempts, got %d", maxRetries+1, attempts.Load())
	}
}

func TestContextCancelDuringRetry(t *testing.T) {
	retryBaseDelay = 50 * time.Millisecond
	defer func() { retryBaseDelay = time.Second }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	c := NewClient(OpenAI, "test", "gpt-4o")
	c.BaseURL = srv.URL
	_, err := c.Call(ctx, "", nil, nil)
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestAnthropicToolResultBatching(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(antOKResponse())
	}))
	defer srv.Close()

	c := NewClient(Anthropic, "test-key", "claude-3")
	c.BaseURL = srv.URL

	history := []Message{
		{Role: RoleUser, Content: "use both tools"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "tc1", Name: "tool1", Input: []byte(`{}`)},
			{ID: "tc2", Name: "tool2", Input: []byte(`{}`)},
		}},
		{Role: RoleToolResult, Content: "result1", ToolCallID: "tc1"},
		{Role: RoleToolResult, Content: "result2", ToolCallID: "tc2"},
	}

	_, err := c.Call(context.Background(), "", history, nil)
	if err != nil {
		t.Fatal(err)
	}

	var req map[string]any
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatal(err)
	}

	msgs := req["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, tool_results), got %d", len(msgs))
	}

	last := msgs[2].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("expected tool results message role 'user', got %v", last["role"])
	}
	content := last["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 tool_result blocks in single message, got %d", len(content))
	}
	for i, block := range content {
		b := block.(map[string]any)
		if b["type"] != "tool_result" {
			t.Errorf("block[%d]: expected type 'tool_result', got %v", i, b["type"])
		}
	}
}
