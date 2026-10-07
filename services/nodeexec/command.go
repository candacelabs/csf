package nodeexec

// Command is a directly executed argv vector. It is never interpreted by a
// shell.
type Command struct {
	Argv []string `json:"argv"`
}

func cloneCommands(in []Command) []Command {
	out := make([]Command, len(in))
	for i := range in {
		out[i].Argv = append([]string(nil), in[i].Argv...)
	}
	return out
}
