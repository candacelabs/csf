package pgmem

import "fmt"

type config struct {
	translator ITranslator
}

// Option configures a database before its first connection is opened.
type Option func(cfg *config) error

// WithTranslator replaces the PostgreSQL compatibility translator. This is
// primarily useful for adding project-specific syntax during tests.
func WithTranslator(translator ITranslator) Option {
	return func(cfg *config) error {
		if translator == nil {
			return fmt.Errorf("pgmem: translator must not be nil")
		}
		cfg.translator = translator
		return nil
	}
}
