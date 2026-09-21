package config_test

import (
	"time"

	"github.com/candacelabs/csf/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Environment", func() {
	const (
		stringName   = "APP_STRING"
		durationName = "APP_DURATION"
		integerName  = "APP_INTEGER"
		aliasName    = "APP_ALIAS"
		missingName  = "APP_MISSING"
		fallback     = "fallback"
		duration     = "250ms"
		integer      = "12"
		expectedWait = 250 * time.Millisecond
		expectedInt  = int32(12)
	)

	values := map[string]string{
		stringName:   " configured ",
		durationName: duration,
		integerName:  integer,
		aliasName:    "alias",
	}
	environment := config.NewEnvironment(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})

	It("reads strings, durations, integers, and aliases", func() {
		Expect(environment.String(stringName, fallback)).To(Equal("configured"))
		Expect(environment.String(missingName, fallback)).To(Equal(fallback))
		Expect(environment.Duration(durationName, time.Second)).To(Equal(expectedWait))
		Expect(environment.Int32(integerName, 1)).To(Equal(expectedInt))
		Expect(environment.First(missingName, aliasName)).To(Equal("alias"))
		Expect(config.NewEnvironment(nil).String(missingName, fallback)).To(Equal(fallback))
		Expect((config.Environment{}).String(missingName, fallback)).To(Equal(fallback))
	})

	It("labels malformed typed input with its environment name", func() {
		values[durationName] = "later"
		values[integerName] = "many"
		DeferCleanup(func() {
			values[durationName] = duration
			values[integerName] = integer
		})

		_, err := environment.Duration(durationName, time.Second)
		Expect(err).To(MatchError(ContainSubstring(durationName)))
		_, err = environment.Int32(integerName, 1)
		Expect(err).To(MatchError(ContainSubstring(integerName)))
	})
})
