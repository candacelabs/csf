package nodeexec

// Plan is a validated preflight plus convergence command.
type Plan struct {
	Commands []Command `json:"commands"`
}
