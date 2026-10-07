package live

import "fmt"

// ConfigError reports an invalid Config, naming the offending field and what
// to set it to.
type ConfigError struct {
	// Field is the Config field at fault.
	Field string
	// Detail says what to set it to.
	Detail string
}

// Error names the field and the fix, in that order, because a construction
// error is read by the person who wrote the Config and has to change one line
// of it.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("gotth-live: Config.%s is invalid: %s", e.Field, e.Detail)
}
