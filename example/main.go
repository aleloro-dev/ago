package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aleloro-dev/ago"
	"github.com/joho/godotenv"
)

type AddArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

func main() {
	godotenv.Load()
	add := ago.NewTool("add", "Add two integers", func(ctx context.Context, args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})

	agent := ago.NewAgent("calculator",
		ago.NewClient(ago.OpenRouter, os.Getenv("OPENROUTER_API_KEY"), "gpt-4o-mini"),
		[]ago.Tool{add},
	)

	session := agent.NewSession()
	resp, err := session.Send(context.Background(), "What's 45 + 34?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp)
}
