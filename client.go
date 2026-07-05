package ago

import (
	"encoding/json"
	"fmt"
)

type Provider string

const (
	OpenAI     Provider = "openai"
	Anthropic  Provider = "anthropic"
	OpenRouter Provider = "openrouter"
	Groq       Provider = "groq"
)

var providerBaseURL = map[Provider]string{
	OpenAI:     "https://api.openai.com/v1",
	Anthropic:  "https://api.anthropic.com/v1",
	OpenRouter: "https://openrouter.ai/api/v1",
	Groq:       "https://api.groq.com/openai/v1",
}

type Client struct {
	Provider  Provider
	APIKey    string
	Model     string
	BaseURL   string
	MaxTokens int
}

func NewClient(provider Provider, apiKey, model string) *Client {
	return &Client{
		Provider: provider,
		APIKey:   apiKey,
		Model:    model,
		BaseURL:  providerBaseURL[provider],
	}
}

func (c *Client) Call(system string, history []Message, tools []Tool) (Message, error) {
	if c.Provider == Anthropic {
		return c.callAnthropic(system, history, tools)
	}
	return c.callOpenAI(system, history, tools)
}

func (c *Client) callOpenAI(system string, history []Message, tools []Tool) (Message, error) {
	msgs := make([]oaiMsg, 0, len(history)+1)
	if system != "" {
		msgs = append(msgs, oaiMsg{Role: "system", Content: system})
	}
	for _, m := range history {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, oaiMsg{Role: "user", Content: m.Content})
		case RoleAssistant:
			msg := oaiMsg{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: oaiFn{Name: tc.Name, Arguments: string(tc.Input)},
				})
			}
			msgs = append(msgs, msg)
		case RoleToolResult:
			msgs = append(msgs, oaiMsg{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID})
		}
	}

	oaiTools := make([]oaiTool, len(tools))
	for i, t := range tools {
		oaiTools[i] = oaiTool{
			Type: "function",
			Function: oaiToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		}
	}

	body, _ := json.Marshal(oaiRequest{Model: c.Model, Messages: msgs, Tools: oaiTools})
	data, err := httpPost(c.BaseURL+"/chat/completions", map[string]string{
		"Authorization": "Bearer " + c.APIKey,
	}, body)
	if err != nil {
		return Message{}, err
	}

	var resp oaiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Message{}, err
	}
	if resp.Error != nil {
		return Message{}, fmt.Errorf("%s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return Message{}, fmt.Errorf("no choices returned")
	}

	choice := resp.Choices[0].Message
	msg := Message{Role: RoleAssistant}
	if choice.Content != nil {
		msg.Content = *choice.Content
	}
	for _, tc := range choice.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}
	return msg, nil
}

type oaiRequest struct {
	Model    string    `json:"model"`
	Messages []oaiMsg  `json:"messages"`
	Tools    []oaiTool `json:"tools,omitempty"`
}

type oaiMsg struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function oaiFn  `json:"function"`
}

type oaiFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string     `json:"type"`
	Function oaiToolDef `json:"function"`
}

type oaiToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message struct {
			Content   *string       `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) callAnthropic(system string, history []Message, tools []Tool) (Message, error) {
	msgs := make([]antMsg, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, antMsg{Role: "user", Content: m.Content})
		case RoleAssistant:
			var blocks []antBlock
			if m.Content != "" {
				blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, antBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input})
			}
			msgs = append(msgs, antMsg{Role: "assistant", Content: blocks})
		case RoleToolResult:
			msgs = append(msgs, antMsg{
				Role:    "user",
				Content: []antBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}},
			})
		}
	}

	antTools := make([]antTool, len(tools))
	for i, t := range tools {
		antTools[i] = antTool{Name: t.Name(), Description: t.Description(), InputSchema: t.Schema()}
	}

	maxTokens := c.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	body, _ := json.Marshal(antRequest{
		Model:     c.Model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
		Tools:     antTools,
	})
	data, err := httpPost(c.BaseURL+"/messages", map[string]string{
		"x-api-key":         c.APIKey,
		"anthropic-version": "2023-06-01",
	}, body)
	if err != nil {
		return Message{}, err
	}

	var resp antResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Message{}, err
	}
	if resp.Error != nil {
		return Message{}, fmt.Errorf("%s", resp.Error.Message)
	}

	msg := Message{Role: RoleAssistant}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content = block.Text
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Input: block.Input})
		}
	}
	return msg, nil
}

type antRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []antMsg  `json:"messages"`
	Tools     []antTool `json:"tools,omitempty"`
}

type antMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type antResponse struct {
	Content []antBlock `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}
