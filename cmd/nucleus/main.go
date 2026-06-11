package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/shimhyuck/nucleus/internal/ollama"
	"github.com/shimhyuck/nucleus/internal/server"
	"github.com/shimhyuck/nucleus/internal/store"
	"github.com/shimhyuck/nucleus/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "models":
		models(os.Args[2:])
	case "pull":
		pull(os.Args[2:])
	case "version":
		fmt.Printf("nucleus %s (%s, %s)\n", version.Version, version.Commit, version.Date)
	default:
		usage()
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "0.0.0.0:8787", "HTTP listen address")
	ollamaURL := fs.String("ollama-url", env("OLLAMA_HOST", "http://127.0.0.1:11434"), "Ollama base URL")
	_ = fs.Parse(args)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ollamaClient := ollama.New(*ollamaURL)
	app := server.New(ollamaClient, store.New(200), logger)
	logger.Info("nucleus starting", "addr", *addr, "ollama", *ollamaURL)
	if err := http.ListenAndServe(*addr, app.Handler()); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	ollamaURL := fs.String("ollama-url", env("OLLAMA_HOST", "http://127.0.0.1:11434"), "Ollama base URL")
	_ = fs.Parse(args)
	s := ollama.New(*ollamaURL).Status(context.Background())
	fmt.Printf("installed=%v reachable=%v base=%s version=%s error=%s\n", s.Installed, s.Reachable, s.BaseURL, s.Version, s.Error)
}

func models(args []string) {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	ollamaURL := fs.String("ollama-url", env("OLLAMA_HOST", "http://127.0.0.1:11434"), "Ollama base URL")
	_ = fs.Parse(args)
	models, err := ollama.New(*ollamaURL).Models(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, model := range models {
		fmt.Printf("%s\t%d bytes\t%s\n", model.Name, model.Size, model.ModifiedAt.Format("2006-01-02 15:04"))
	}
}

func pull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	ollamaURL := fs.String("ollama-url", env("OLLAMA_HOST", "http://127.0.0.1:11434"), "Ollama base URL")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: nucleus pull <model>")
		os.Exit(2)
	}
	err := ollama.New(*ollamaURL).Pull(context.Background(), fs.Arg(0), func(line []byte) {
		fmt.Println(string(line))
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func usage() {
	fmt.Print(`nucleus - local LLM orchestrator for macOS

Usage:
  nucleus serve [--addr 0.0.0.0:8787] [--ollama-url http://127.0.0.1:11434]
  nucleus status
  nucleus models
  nucleus pull <model>
  nucleus version

API:
  Dashboard:              http://127.0.0.1:8787
  OpenAI compatible chat: POST /v1/chat/completions
  OpenAI compatible image: POST /v1/images/generations
  OpenAI compatible list: GET  /v1/models

Tailscale or LAN access:
  nucleus serve

Local-only access:
  nucleus serve --addr 127.0.0.1:8787
`)
}
