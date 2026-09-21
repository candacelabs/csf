package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	applicationName     = "csf-agent-example"
	recipeFlag          = "recipe"
	workbenchFlag       = "workbench"
	submitFlag          = "submit"
	timeoutFlag         = "timeout"
	defaultRecipeFile   = "agent.json"
	defaultWorkbenchURL = "http://127.0.0.1:14111"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", applicationName, err)
		os.Exit(1)
	}
}

func run() error {
	recipeFile := flag.String(recipeFlag, defaultRecipeFile, "immutable agent assignment recipe")
	workbench := flag.String(workbenchFlag, defaultWorkbenchURL, "existing Workbench URL")
	submit := flag.Bool(submitFlag, false, "create a worktree/session and submit the task; otherwise print the plan")
	timeout := flag.Duration(timeoutFlag, 30*time.Second, "deadline for session and prompt acceptance")
	flag.Parse()
	content, err := os.ReadFile(*recipeFile)
	if err != nil {
		return err
	}
	recipe := &pb.AgentAssignmentRecipe{}
	if err := protojson.Unmarshal(content, recipe); err != nil {
		return err
	}
	plan, err := csf.PrepareAgentAssignment(recipe)
	if err != nil {
		return err
	}
	if !*submit {
		return printMessage(plan)
	}
	return submitPlan(*workbench, *timeout, plan)
}

func submitPlan(endpoint string, timeout time.Duration, plan *pb.AgentAssignmentPlan) error {
	client, err := api.NewClientWithResponses(endpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	receipt, submitErr := csf.SubmitAgentAssignmentHTTP(ctx, client, endpoint, plan)
	if receipt != nil {
		if err := printMessage(receipt); err != nil {
			return err
		}
	}
	return submitErr
}

func printMessage(message proto.Message) error {
	content, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(content))
	return err
}
