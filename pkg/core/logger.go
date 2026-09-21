package core

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

var Logger *zerolog.Logger

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFunc = func() time.Time {
		return time.Now().UTC()
	}

	// Create a console writer
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stdout}

	logger := zerolog.New(consoleWriter).
		With().
		Timestamp().
		Caller().
		Logger()

	Logger = &logger
}
