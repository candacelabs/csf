// Package config is the config capability: the one place in a CSF process that
// reads the process environment.
//
// Configuration is a capability like a socket or a file. A binary's main
// function obtains [OSEnvironment] once and hands the resulting [Environment]
// — or values parsed from it — to the services it constructs; a service never
// reads os.Getenv itself. Application policy (which names exist, their
// defaults and their validation) stays with the application that declares
// them. Pure parsing of configuration values that needs no process input lives
// in candace/pkg/config.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment reads configuration from a named string source.
type Environment struct {
	lookup func(name string) (string, bool)
}

// NewEnvironment constructs an environment reader around lookup. A nil lookup
// is an empty environment, which keeps zero-input configuration deterministic.
func NewEnvironment(lookup func(name string) (string, bool)) Environment {
	if lookup == nil {
		lookup = func(name string) (string, bool) { return "", false }
	}
	return Environment{lookup: lookup}
}

// OSEnvironment reads the current process environment.
func OSEnvironment() Environment {
	return NewEnvironment(os.LookupEnv)
}

// InheritedEnvironment is the whole process environment as name=value pairs:
// what a child process inherits. The process capability (candace/ipc/proc)
// reads it here, at launch, to extend a child's environment without reading
// the process environment itself.
func InheritedEnvironment() []string {
	return os.Environ()
}

// String returns the trimmed named value or fallback when it is empty.
func (environment Environment) String(name, fallback string) string {
	if value := environment.Raw(name); value != "" {
		return value
	}
	return fallback
}

// Raw returns the trimmed named value.
func (environment Environment) Raw(name string) string {
	if environment.lookup == nil {
		return ""
	}
	value, _ := environment.lookup(name)
	return strings.TrimSpace(value)
}

// Duration parses the named Go duration or returns fallback when it is empty.
func (environment Environment) Duration(name string, fallback time.Duration) (time.Duration, error) {
	value := environment.Raw(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

// Int32 parses the named base-10 integer or returns fallback when it is empty.
func (environment Environment) Int32(name string, fallback int32) (int32, error) {
	value := environment.Raw(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return int32(parsed), nil
}

// First returns the first non-empty named value.
func (environment Environment) First(names ...string) string {
	for _, name := range names {
		if value := environment.Raw(name); value != "" {
			return value
		}
	}
	return ""
}
