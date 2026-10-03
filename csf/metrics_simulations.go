package csf

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/jobs"
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
	ledger := inspection.simulations.ledger
	states, err := ledger.States(ctx)
	if err != nil {
		emit("simulation_collection_readable", 0)
		return
	}
	progress, err := ledger.LatestProgress(ctx)
	if err != nil {
		emit("simulation_collection_readable", 0)
		return
	}
	budget, err := ledger.BudgetUsage(ctx)
	if err != nil && !errors.Is(err, jobs.ErrBudgetNotOpened) {
		emit("simulation_collection_readable", 0)
		return
	}
	emit("simulation_collection_readable", 1)
	for _, row := range states {
		emit("simulation_jobs", float64(row.Jobs), simulationMetricSimulator(row.Kind), simulationMetricExecutor(row.Executor), string(row.State))
	}
	for _, row := range progress {
		simulator, executor := simulationMetricSimulator(row.Kind), simulationMetricExecutor(row.Executor)
		emit("simulation_latest_progress_ratio", float64(row.CompletedUnits)/float64(row.TotalUnits), simulator, executor)
		emit("simulation_latest_update_timestamp_seconds", float64(row.UpdatedAt.Unix()), simulator, executor)
	}
	if err == nil {
		emit("simulation_reserved_usd", float64(budget.ReservedUSDMicros)/1e6)
		emit("simulation_budget_usd", float64(budget.LimitUSDMicros)/1e6)
	}
}

func simulationMetricSimulator(kind jobs.Kind) string {
	return strings.ToLower(strings.TrimPrefix(simulatorOf(kind).String(), "SIMULATOR_"))
}
func simulationMetricExecutor(name jobs.ExecutorName) string {
	return strings.ToLower(strings.TrimPrefix(simulationExecutorOf(name).String(), "SIMULATION_EXECUTOR_"))
}
