package csf

import (
	"context"
	"errors"
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
)

var simulationMetricDefinitions = []struct {
	name, help string
	labels     []string
}{
	{"simulation_collection_readable", "One when simulator rows were read successfully; absent measurements are not zero.", nil},
	{"simulation_jobs", "Admitted simulator jobs by current durable state; reservations include ambiguous submissions.", []string{"simulator", "executor", "state"}},
	{"simulation_latest_progress_ratio", "Completed steps divided by admitted steps for the latest run; not an autonomy or safety score.", []string{"simulator", "executor"}},
	{"simulation_latest_update_timestamp_seconds", "Latest run observation timestamp for freshness inspection.", []string{"simulator", "executor"}},
	{"simulation_reserved_usd", "Retained admission reservations, including completed jobs; not an AWS invoice or measured spend.", nil},
	{"simulation_budget_usd", "Immutable campaign admission budget; does not measure shared cloud infrastructure charges.", nil},
}

func WithInspectionSimulations(simulations *Simulations) InspectionOption {
	return func(inspection *Inspection) { inspection.simulations = simulations }
}

func (inspection *Inspection) collectSimulations(emit func(name string, value float64, labels ...string)) {
	if inspection.simulations == nil {
		emit("simulation_collection_readable", 0)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queries := inspection.simulations.store.queries
	states, err := queries.CountSimulationStates(ctx)
	if err != nil {
		emit("simulation_collection_readable", 0)
		return
	}
	progress, err := queries.LatestSimulationProgress(ctx)
	if err != nil {
		emit("simulation_collection_readable", 0)
		return
	}
	budget, err := queries.SimulationBudget(ctx, simulationCampaign)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		emit("simulation_collection_readable", 0)
		return
	}
	emit("simulation_collection_readable", 1)
	for _, row := range states {
		emit("simulation_jobs", float64(row.Jobs), simulationMetricSimulator(row.Simulator), simulationMetricExecutor(row.Executor), string(row.State))
	}
	for _, row := range progress {
		simulator, executor := simulationMetricSimulator(row.Simulator), simulationMetricExecutor(row.Executor)
		emit("simulation_latest_progress_ratio", float64(row.CompletedSteps)/float64(row.Steps), simulator, executor)
		emit("simulation_latest_update_timestamp_seconds", float64(row.UpdatedAt.Time.Unix()), simulator, executor)
	}
	if err == nil {
		emit("simulation_reserved_usd", float64(budget.ReservedUsdMicros)/1e6)
		emit("simulation_budget_usd", float64(budget.BudgetLimitUsdMicros)/1e6)
	}
}

func simulationMetricSimulator(value int32) string {
	return strings.ToLower(strings.TrimPrefix(pb.Simulator(value).String(), "SIMULATOR_"))
}
func simulationMetricExecutor(value int32) string {
	return strings.ToLower(strings.TrimPrefix(pb.SimulationExecutor(value).String(), "SIMULATION_EXECUTOR_"))
}
