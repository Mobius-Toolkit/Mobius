// Command mobius-session starts one Claude Code session through ACP with the Mobius MCP server.
// It sends one prompt and prints the tool calls and the end of the session.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/mobius-go/internal/mcp"
	"github.com/Mobius-Toolkit/mobius-go/internal/runner"
)

const prompt = "Call the Mobius tool list_tasks one time. Do not call other tools. Then reply with its result."

const toolsTimeout = 30 * time.Second

func main() {
	model := flag.String("model", "sonnet", "the model of the session")
	effort := flag.String("effort", "low", "the effort of the session")
	flag.Parse()

	dataDir, err := os.MkdirTemp("", "mobius-session-")
	if err != nil {
		log.Fatal(err)
	}
	err = run(dataDir, *model, *effort)
	if removeErr := os.RemoveAll(dataDir); removeErr != nil {
		log.Print(removeErr)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run(dataDir, model, effort string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := mcp.New()
	mux := http.NewServeMux()
	server.Register(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Fatal(srv.Serve(listener))
	}()

	key := server.Open()
	defer server.Close(key)
	url := mcp.URL(listener.Addr().String(), key)
	fmt.Printf("mcp server: %s\n", url)

	if err := runner.Prepare(dataDir); err != nil {
		return err
	}
	cwd := filepath.Join(dataDir, "work")
	if err := os.Mkdir(cwd, 0o750); err != nil {
		return err
	}

	ctx := context.Background()
	var message strings.Builder
	start := time.Now()
	session, err := runner.Start(ctx, cwd, dataDir, os.Getenv("PATH"), url, func(notification acp.SessionNotification) {
		update := notification.Update
		switch {
		case update.AgentMessageChunk != nil && update.AgentMessageChunk.Content.Text != nil:
			message.WriteString(update.AgentMessageChunk.Content.Text.Text)
		case update.ToolCall != nil:
			input, _ := json.Marshal(update.ToolCall.RawInput)
			fmt.Printf("tool_call %s: %s, %s, input %s\n", update.ToolCall.ToolCallId, update.ToolCall.Title, update.ToolCall.Status, input)
		case update.ToolCallUpdate != nil && update.ToolCallUpdate.Status != nil:
			fmt.Printf("tool_call_update %s: %s%s\n", update.ToolCallUpdate.ToolCallId, *update.ToolCallUpdate.Status, contentText(update.ToolCallUpdate.Content))
		}
	})
	if err != nil {
		return err
	}
	defer session.Close()
	fmt.Printf("session %s: started in %s, cwd %s\n", session.ID(), time.Since(start).Round(time.Millisecond), cwd)

	select {
	case version := <-server.Listed(key):
		fmt.Printf("tools/list: by %s after the start, MCP protocol %s\n", time.Since(start).Round(time.Millisecond), version)
	case <-time.After(toolsTimeout):
		return errors.New("the session sent no tools/list, so it has no Mobius tools")
	}

	if err := session.Configure(ctx, model, effort); err != nil {
		return err
	}
	fmt.Printf("model %s, effort %s, mode bypassPermissions\n", model, effort)

	fmt.Printf("prompt: %s\n", prompt)
	stopReason, err := session.Prompt(ctx, prompt)
	if err != nil {
		return err
	}
	fmt.Printf("agent message: %s\n", message.String())
	fmt.Printf("end of turn: %s\n", stopReason)
	return nil
}

func contentText(content []acp.ToolCallContent) string {
	var text string
	for _, item := range content {
		if item.Content != nil && item.Content.Content.Text != nil {
			text += "\n" + item.Content.Content.Text.Text
		}
	}
	return text
}
