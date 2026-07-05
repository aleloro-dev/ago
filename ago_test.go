package ago_test

import (
	"fmt"
	"testing"

	"github.com/aleloro-dev/ago"
)

type mockClient struct {
	calls int
}

func (m *mockClient) Call(system string, history []ago.Message, tools []ago.Tool) (ago.Message, error) {
	m.calls++
	if m.calls == 1 {
		return ago.Message{
			Role: ago.RoleAssistant,
			ToolCalls: []ago.ToolCall{
				{ID: "1", Name: "add", Input: []byte(`{"a":42,"b":58}`)},
			},
		}, nil
	}
	return ago.Message{
		Role:    ago.RoleAssistant,
		Content: "42 + 58 = 100",
	}, nil
}

type AddArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestAgentToolCall(t *testing.T) {
	add := ago.NewTool("add", "Add two integers", func(args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})

	agent := ago.NewAgent("test", &mockClient{}, add)
	result, err := agent.Run("What is 42 + 58?")
	if err != nil {
		t.Fatal(err)
	}
	if result != "42 + 58 = 100" {
		t.Fatalf("unexpected result: %s", result)
	}
}
