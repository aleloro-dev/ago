# Ago

A lightweight library to build AI agents. Written in Go with zero dependencies (stdlib only). 

The name 'ago' comes from the Latin verb _ago_, which means to act - exactly what agents do.

## Install

```sh
go get github.com/aleloro-dev/ago
```

## Quick start

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aleloro-dev/ago"
)

type AddArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

func main() {
	add := ago.NewTool("add", "Add two integers", func(args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})

	agent := ago.NewAgent("calculator",
		ago.NewClient(ago.OpenAI, os.Getenv("OPENAI_API_KEY"), "gpt-4o"),
		add,
	)

	result, err := agent.Run("What is 42 + 58?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Providers

```go
ago.NewClient(ago.OpenAI,     os.Getenv("OPENAI_API_KEY"),     "gpt-4o")
ago.NewClient(ago.Anthropic,  os.Getenv("ANTHROPIC_API_KEY"),  "claude-3-5-sonnet-20241022")
ago.NewClient(ago.OpenRouter, os.Getenv("OPENROUTER_API_KEY"), "anthropic/claude-3.5-sonnet")
ago.NewClient(ago.Groq,       os.Getenv("GROQ_API_KEY"),       "llama-3.1-70b-versatile")
```

## Tools

Define a struct for the input, pass a typed function to `NewTool`. Schema is generated automatically from the struct.

```go
type SearchArgs struct {
	Query string   `json:"query"`
	Tags  []string `json:"tags"`
}

search := ago.NewTool("search", "Search the web", func(args SearchArgs) (string, error) {
	// ...
	return result, nil
})
```

Tools must be stateless — they are executed in parallel when the model requests multiple calls in a single turn.

Implement the `Tool` interface directly for more control:

```go
type MyTool struct{}

func (t MyTool) Name() string             { return "my_tool" }
func (t MyTool) Description() string      { return "Does something." }
func (t MyTool) Schema() json.RawMessage  { return json.RawMessage(`{...}`) }
func (t MyTool) Execute(input json.RawMessage) (string, error) { ... }
```

## Agent options

```go
agent := ago.NewAgent("assistant", client, tool1, tool2)
agent.System  = "You are a helpful assistant."
agent.MaxIter = 20 // default: 10
```

## Sub-agents

Agents can be used as tools, enabling multi-agent orchestration:

```go
researcher := ago.NewAgent("researcher", client, searchTool, fetchTool)

orchestrator := ago.NewAgent("orchestrator", client,
	ago.NewTool("research", "Research a topic in depth", func(args ResearchArgs) (string, error) {
		return researcher.Run(args.Topic)
	}),
)
```

## Observability

`ago` logs via `log/slog`. Set a handler at startup to control output:

```go
// JSON logs
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

Log events:

| Event | Description |
|---|---|
| `[agent:run]` | Agent started a task |
| `[model:call]` | Model API call |
| `[model:error]` | Model API error |
| `[tool:call]` | Tool invoked |
| `[tool:result]` | Tool returned |
| `[agent:done]` | Agent finished |
| `[agent:limit]` | Max iterations reached |

## Custom client

Implement `ModelClient` to use any provider:

```go
type ModelClient interface {
	Call(system string, history []ago.Message, tools []ago.Tool) (ago.Message, error)
}
```
