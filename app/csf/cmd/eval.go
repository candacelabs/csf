package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/candacelabs/csf/io/net/model/jev"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
)

const (
	evalCommand = "eval"

	evalEndpointFlag = "endpoint"
	evalModelFlag    = "model"

	// evalTimeout bounds one candidate's whole held-out run. The first question
	// loads the model into memory before it answers.
	evalTimeout = 30 * time.Minute
)

// eval measures every declared decision model over the committed held-out
// corpus and prints the eval table: one row per measured model with its
// accuracy, expected calibration error, median latency and decisions per
// second. The table it prints is the one committed under ipc/model/jev, and
// the endpoint is named on the command line rather than compiled in, so no
// server address is tracked.
func eval(ctx context.Context, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(evalCommand, flag.ContinueOnError)
	endpoint := flags.String(evalEndpointFlag, jev.DefaultOllamaEndpoint, "Ollama server URL")
	modelName := flags.String(evalModelFlag, "", "one declared model to measure (default: every declared model)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("eval takes flags only; unexpected %q", flags.Arg(0))
	}
	candidates, err := evalCandidates(*modelName)
	if err != nil {
		return err
	}
	questions, err := jev.LoadTreeQuestions()
	if err != nil {
		return err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(evalTimeout))
	if err != nil {
		return err
	}
	table := jev.EvalTable{}
	for _, candidate := range candidates {
		row, err := measureCandidate(ctx, client, *endpoint, candidate, questions)
		if err != nil {
			// A candidate the server cannot load is left out of the table
			// rather than failing the run; the committed table records only
			// what was measured.
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", candidate.Name, err)
			continue
		}
		table = append(table, row)
	}
	if len(table) == 0 {
		return fmt.Errorf("eval measured no declared model")
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(table)
}

// evalCandidates is the candidates to measure: the named declared model, or
// every declared candidate when none is named.
func evalCandidates(name string) ([]jev.DeciderModel, error) {
	if name == "" {
		return jev.DecisionCandidates, nil
	}
	model, err := jev.DeclaredModel(name)
	if err != nil {
		return nil, err
	}
	return []jev.DeciderModel{model}, nil
}

// measureCandidate runs the held-out corpus against one declared model and
// unloads it when the run ends, so the next candidate loads into a free GPU.
func measureCandidate(ctx context.Context, client iohttp.IHTTPClient, endpoint string, candidate jev.DeciderModel, questions []jev.TreeQuestion) (jev.ModelResult, error) {
	decider, err := jev.NewDecider(client, candidate, jev.WithOllamaEndpoint(endpoint))
	if err != nil {
		return jev.ModelResult{}, err
	}
	metrics, err := jev.Evaluate(ctx, decider, questions)
	if unloadErr := decider.Unload(ctx); unloadErr != nil {
		fmt.Fprintf(os.Stderr, "warning: unload %s: %v\n", candidate.Name, unloadErr)
	}
	if err != nil {
		return jev.ModelResult{}, err
	}
	return jev.ModelResult{Model: candidate.Name, Metrics: metrics}, nil
}
