package ago

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
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

type Message struct {
	Role       MessageRole
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(input json.RawMessage) (string, error)
}

type ModelClient interface {
	Call(system string, history []Message, tools []Tool) (Message, error)
}

type Agent struct {
	Name    string
	Model   ModelClient
	Tools   map[string]Tool
	History []Message
	System  string
	MaxIter int
}

func NewAgent(name string, model ModelClient, tools ...Tool) *Agent {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Agent{Name: name, Model: model, Tools: m}
}

func (a *Agent) Run(task string) (string, error) {
	maxIter := a.MaxIter
	if maxIter == 0 {
		maxIter = 10
	}
	slog.Info("[agent:run]", "agent", a.Name, "task", task)
	a.History = append(a.History, Message{Role: RoleUser, Content: task})
	for range maxIter {
		slog.Info("[model:call]", "agent", a.Name, "history", len(a.History))
		resp, err := a.Model.Call(a.System, a.History, a.toolList())
		if err != nil {
			slog.Error("[model:error]", "agent", a.Name, "error", err)
			return "", err
		}
		a.History = append(a.History, resp)
		if len(resp.ToolCalls) == 0 {
			slog.Info("[agent:done]", "agent", a.Name, "response", resp.Content)
			return resp.Content, nil
		}
		results := make([]string, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.ToolCalls {
			wg.Add(1)
			go func(i int, tc ToolCall) {
				defer wg.Done()
				slog.Info("[tool:call]", "agent", a.Name, "tool", tc.Name, "input", string(tc.Input))
				results[i] = a.executeTool(tc)
				slog.Info("[tool:result]", "agent", a.Name, "tool", tc.Name, "result", results[i])
			}(i, tc)
		}
		wg.Wait()
		for i, tc := range resp.ToolCalls {
			a.History = append(a.History, Message{
				Role:       RoleToolResult,
				Content:    results[i],
				ToolCallID: tc.ID,
			})
		}
	}
	slog.Warn("[agent:limit]", "agent", a.Name)
	return "", fmt.Errorf("max iterations reached")
}

func (a *Agent) executeTool(tc ToolCall) string {
	tool, ok := a.Tools[tc.Name]
	if !ok {
		return fmt.Sprintf("error: tool %q not found", tc.Name)
	}
	result, err := tool.Execute(tc.Input)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return result
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
	fn          func(T) (string, error)
}

func (t *funcTool[T]) Name() string            { return t.name }
func (t *funcTool[T]) Description() string     { return t.description }
func (t *funcTool[T]) Schema() json.RawMessage { return t.schema }
func (t *funcTool[T]) Execute(input json.RawMessage) (string, error) {
	var args T
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	return t.fn(args)
}

func NewTool[T any](name, description string, fn func(T) (string, error)) Tool {
	var zero T
	return &funcTool[T]{
		name:        name,
		description: description,
		schema:      schemaOf(reflect.TypeOf(zero)),
		fn:          fn,
	}
}
