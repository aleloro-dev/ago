# Ago

A lightweight library to build AI agents. Written in Go with zero dependencies (stdlib only).

The name comes from the Latin verb _ago_ — to act. Exactly what agents do.

## Install

```sh
go get github.com/aleloro-dev/ago
```

## Quick start

```go
package main

import (
	"context"
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
	add := ago.NewTool("add", "Add two integers", func(ctx context.Context, args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})

	agent := ago.NewAgent("calculator",
		ago.NewClient(ago.OpenAI, os.Getenv("OPENAI_API_KEY"), "gpt-4o"),
		[]ago.Tool{add},
	)

	result, err := agent.NewSession().Send(context.Background(), "What is 42 + 58?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Concepts

### Agent

An `Agent` is stateless configuration: model, tools, system prompt, and max iterations. It is safe to share across goroutines. Create one at startup and reuse it for the lifetime of your application.

```go
agent := ago.NewAgent("assistant",
	ago.NewClient(ago.Anthropic, os.Getenv("ANTHROPIC_API_KEY"), "claude-3-5-sonnet-20241022"),
	[]ago.Tool{tool1, tool2},
)
agent.SystemPrompt = "You are a helpful assistant."
agent.MaxIter = 20 // default: 10
```

### Session

A `Session` owns the conversation history for a single user or task. Create one per conversation — do not share sessions across goroutines.

```go
session := agent.NewSession()
fmt.Println(session.ID) // e.g. "session_a3f2c8d1e4b07f91"
```

`Send` appends the user message to history, runs the agent loop until the model stops calling tools, and returns the final response. Call it multiple times for multi-turn conversations:

```go
reply, err := session.Send(ctx, "What is the capital of France?")
// reply: "Paris"

reply, err = session.Send(ctx, "What is the population there?")
// history carries over — the model knows you mean Paris
```

## Providers

```go
ago.NewClient(ago.OpenAI,     os.Getenv("OPENAI_API_KEY"),     "gpt-4o")
ago.NewClient(ago.Anthropic,  os.Getenv("ANTHROPIC_API_KEY"),  "claude-3-5-sonnet-20241022")
ago.NewClient(ago.OpenRouter, os.Getenv("OPENROUTER_API_KEY"), "anthropic/claude-3.5-sonnet")
ago.NewClient(ago.Groq,       os.Getenv("GROQ_API_KEY"),       "llama-3.1-70b-versatile")
```

Set `MaxTokens` to override the default of 4096:

```go
client := ago.NewClient(ago.OpenAI, apiKey, "gpt-4o")
client.MaxTokens = 8192
```

## Tools

Define a struct for the input and pass a typed function to `NewTool`. The JSON schema is generated automatically from the struct tags.

```go
type SearchArgs struct {
	Query  string   `json:"query"`
	Tags   []string `json:"tags,omitempty"`
	Limit  int      `json:"limit"`
}

search := ago.NewTool("search", "Search the knowledge base", func(ctx context.Context, args SearchArgs) (string, error) {
	results, err := db.Search(ctx, args.Query, args.Tags, args.Limit)
	if err != nil {
		return "", err
	}
	return results.String(), nil
})
```

Supported field types: `string`, `int`, `float64`, `bool`, slices, nested structs, and pointers. Fields tagged with `omitempty` are not marked as required in the schema. Unexported fields and `json:"-"` fields are ignored.

Tools receive a `context.Context` and should respect cancellation for any I/O they perform.

When the model requests multiple tools in a single turn, they are executed in parallel.

`Tool` is an interface:

```go
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}
```

So you can implement it directly for full control if you prefer.

## Token usage

Token usage is tracked automatically and accumulated on the session across all `Send` calls:

```go
session := agent.NewSession()
session.Send(ctx, "Summarize this document")
session.Send(ctx, "Now translate it to Spanish")

fmt.Println(session.Usage.InputTokens)
fmt.Println(session.Usage.OutputTokens)
```

## Persistence

Implement the `Store` interface to persist and restore sessions:

```go
type Store interface {
	Save(s *ago.Session) error
	Load(sessionID ago.ResourceID) (*ago.Session, error)
}
```

Attach it to the agent — `Save` is called automatically after each `Send`:

```go
agent.Store = &RedisStore{}

// save happens automatically
session := agent.NewSession()
session.Send(ctx, "Hello")

// restore a session by ID
session, err := agent.Store.Load(sessionID)
session.Send(ctx, "Continue where we left off")
```

## Observability

Implement the `Observer` interface to hook into agent events:

```go
type Observer interface {
	OnEvent(e ago.Event)
}
```

Attach it to the agent — all sessions share the same observer:

```go
agent.Observer = &MyObserver{}
```

Each event carries a typed `EventType` and relevant fields:

```go
func (o *MyObserver) OnEvent(e ago.Event) {
	switch e.Type {
	case ago.EventSessionStart:
		// e.SessionID, e.AgentName, e.Task
	case ago.EventModelCall:
		// e.SessionID, e.DurationMs, e.Usage
	case ago.EventToolCall:
		// e.SessionID, e.Tool, e.DurationMs, e.Err
	}
}
```

By default, events are logged via `log/slog`. Set a handler at startup to control format and destination:

```go
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

## Sub-agents

Agents can be used as tools, enabling multi-agent orchestration:

```go
researcher := ago.NewAgent("researcher", client, []ago.Tool{searchTool, fetchTool})

orchestrator := ago.NewAgent("orchestrator", client,
	[]ago.Tool{
		ago.NewTool("research", "Research a topic in depth", func(ctx context.Context, args ResearchArgs) (string, error) {
			return researcher.NewSession().Send(ctx, args.Topic)
		}),
	},
)
```

## Custom model client

Implement `ModelClient` to use any provider or add middleware:

```go
type ModelClient interface {
	Call(ctx context.Context, system string, history []ago.Message, tools []ago.Tool) (ago.Message, error)
}
```

## Reliability

The HTTP client retries automatically on transient errors (429, 5xx) with exponential backoff — up to 3 retries with delays of 1s, 2s, and 4s. Non-retryable errors (4xx) are returned immediately. All requests respect the context passed to `Send`, including cancellation and deadlines.
