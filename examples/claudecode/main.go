// Copyright 2026 Candace Labs

// Command claudecode runs one real turn through the Claude Code turn executor
// (ipc/model/claudecode) and prints the turn report: every event in and out,
// as one JSON log record per line, each carrying the turn trace, its own span,
// the session, the turn number, the event type and the elapsed time.
//
//	go run ./examples/claudecode -prompt "Reply with exactly one word: pong"
//
// The turn runs with every built-in tool disabled (--tools ""), so the proof
// reads nothing and changes nothing on the machine. It needs an installed,
// signed-in claude on PATH.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
)

const (
	// messageTypeUser and roleUser are the stream-json user message type and
	// its role.
	messageTypeUser = "user"
	roleUser        = "user"
	// flagTools and noTools disable every built-in tool for the proof turn.
	flagTools = "--tools"
	// The command line flags of this example.
	flagPrompt     = "prompt"
	flagDirectory  = "dir"
	flagSession    = "session"
	flagResume     = "resume"
	flagExecutable = "claude"
	noTools        = ""
)

// userMessage is one stream-json input message.
type userMessage struct {
	Type    string      `json:"type"`
	Message messageBody `json:"message"`
}

type messageBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	prompt := flag.String(flagPrompt, "Reply with exactly one word: pong", "the user message for the turn")
	directory := flag.String(flagDirectory, "", "the working directory Claude Code runs in; empty means this one")
	session := flag.String(flagSession, "", "the session uuid; empty mints a new session")
	resume := flag.Bool(flagResume, false, "resume an existing session instead of creating it")
	executable := flag.String(flagExecutable, claudecode.DefaultExecutable, "the Claude Code executable")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	id := uuid.New()
	if *session != "" {
		parsed, err := uuid.Parse(*session)
		if err != nil {
			return fmt.Errorf("session: %w", err)
		}
		id = parsed
	}
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		return err
	}
	options := []claudecode.ClaudeCodeBrainOption{
		claudecode.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil))),
		claudecode.WithExecutable(*executable),
		claudecode.WithDirectory(*directory),
		claudecode.WithArguments(flagTools, noTools),
	}
	if *resume {
		options = append(options, claudecode.WithResumedSession())
	}
	brain, err := claudecode.NewClaudeCodeBrain(launcher, id, options...)
	if err != nil {
		return err
	}
	message, err := json.Marshal(userMessage{Type: messageTypeUser, Message: messageBody{Role: roleUser, Content: *prompt}})
	if err != nil {
		return err
	}
	proposal, err := brain.Propose(ctx, &claudecode.Turn{Messages: []json.RawMessage{message}})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "session %s: turn finished with %d events from %s\n", id, len(proposal.Actions), proposal.Provider)
	return nil
}
