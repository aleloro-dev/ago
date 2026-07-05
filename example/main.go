package main

import (
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
	add := ago.NewTool("add", "Add two integers", func(args AddArgs) (string, error) {
		return fmt.Sprintf("%d", args.A+args.B), nil
	})

	agent := ago.NewAgent("calculator",
		ago.NewClient(ago.OpenRouter, os.Getenv("OPENROUTER_API_KEY"), "gpt-4o-mini"),
		add,
	)

	result, err := agent.Run("What is 42 + 58?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
