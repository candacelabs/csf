package widget_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget"
)

// soundRegistration is the node-status widget's registration: the smallest one
// the dialect can produce, and the one every fault below is a single edit away
// from.
func soundRegistration() widget.Registration {
	return widget.Registration{
		Name:   "NodeStatus",
		Region: "widget.node-status",
		Events: []string{"widget.node-status.health"},
		Streams: []widget.StreamDeclaration{{
			Name:     "healthWatch",
			Source:   "widget.node-status.watch",
			Delivers: "widget.node-status.health",
		}},
	}
}

var _ = Describe("Registration.Validate", func() {
	It("accepts the registration the minimal document produces", func() {
		Expect(soundRegistration().Validate()).To(Succeed())
	})

	It("refuses a widget with no name, because a snapshot could not say where it came from", func() {
		registration := soundRegistration()
		registration.Name = "   "

		Expect(registration.Validate()).To(MatchError(widget.ErrEmptyName))
	})

	DescribeTable("refuses a region identity the wire cannot carry",
		func(region string) {
			registration := soundRegistration()
			registration.Region = region

			Expect(registration.Validate()).To(MatchError(widget.ErrInvalidRegion))
		},
		Entry("empty", ""),
		Entry("a space", "widget node-status"),
		Entry("a slash", "widget/node-status"),
		Entry("65 characters", strings.Repeat("r", 65)),
	)

	It("accepts a region identity of exactly 64 characters", func() {
		registration := soundRegistration()
		registration.Region = strings.Repeat("r", 64)

		Expect(registration.Validate()).To(Succeed())
	})

	It("refuses an event with no wire name", func() {
		registration := soundRegistration()
		registration.Events = []string{""}
		registration.Streams = nil

		Expect(registration.Validate()).To(MatchError(widget.ErrEmptyEvent))
	})

	It("refuses one widget claiming a wire name twice, browser-sendable or not", func() {
		registration := soundRegistration()
		registration.Internal = []string{"widget.node-status.health"}

		Expect(registration.Validate()).To(MatchError(widget.ErrDuplicateEvent))
	})

	It("accepts an internal name the browser may not send", func() {
		registration := soundRegistration()
		registration.Internal = []string{"widget.node-status.sync"}

		Expect(registration.Validate()).To(Succeed())
	})

	It("refuses a stream delivering an event the widget never declared", func() {
		registration := soundRegistration()
		registration.Streams[0].Delivers = "widget.node-status.absent"

		validationError := registration.Validate()

		Expect(validationError).To(MatchError(widget.ErrUndeliveredStream))
		Expect(validationError.Error()).To(ContainSubstring("healthWatch"))
	})

	It("names the widget and the offending spelling, so the fault is repairable from the message", func() {
		registration := soundRegistration()
		registration.Region = "widget node-status"

		validationError := registration.Validate()

		Expect(validationError.Error()).To(ContainSubstring("NodeStatus"))
		Expect(validationError.Error()).To(ContainSubstring(`"widget node-status"`))
	})

	It("distinguishes its faults, so a caller can branch on which one it met", func() {
		empty := widget.Registration{}

		Expect(errors.Is(empty.Validate(), widget.ErrEmptyName)).To(BeTrue())
		Expect(errors.Is(empty.Validate(), widget.ErrInvalidRegion)).To(BeFalse())
	})

	Describe("the payload declarations", func() {
		It("accepts a payload for an event the widget declares", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{{
				Event:  "widget.node-status.health",
				Fields: []string{"reachable", "last_seen"},
			}}

			Expect(registration.Validate()).To(Succeed())
		})

		It("accepts a payload for an internal event, because a stream fills one too", func() {
			registration := soundRegistration()
			registration.Events = nil
			registration.Internal = []string{"widget.node-status.health"}
			registration.Payloads = []widget.EventPayload{{
				Event: "widget.node-status.health", Fields: []string{"reachable"},
			}}

			Expect(registration.Validate()).To(Succeed())
		})

		It("refuses a payload for an event the widget never declared", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{{
				Event: "widget.node-status.absent", Fields: []string{"reachable"},
			}}

			Expect(registration.Validate()).To(MatchError(widget.ErrUnknownPayload))
		})

		It("refuses one event described twice, which leaves two answers to one question", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{
				{Event: "widget.node-status.health", Fields: []string{"reachable"}},
				{Event: "widget.node-status.health", Fields: []string{"last_seen"}},
			}

			Expect(registration.Validate()).To(MatchError(widget.ErrDuplicatePayload))
		})

		It("refuses a field with no wire name", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{{
				Event: "widget.node-status.health", Fields: []string{""},
			}}

			Expect(registration.Validate()).To(MatchError(widget.ErrEmptyField))
		})

		It("refuses one field declared twice", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{{
				Event: "widget.node-status.health", Fields: []string{"reachable", "reachable"},
			}}

			Expect(registration.Validate()).To(MatchError(widget.ErrDuplicateField))
		})
	})

	Describe("Payload", func() {
		It("returns the field names one event carries", func() {
			registration := soundRegistration()
			registration.Payloads = []widget.EventPayload{{
				Event: "widget.node-status.health", Fields: []string{"reachable", "last_seen"},
			}}

			fields, described := registration.Payload("widget.node-status.health")

			Expect(described).To(BeTrue())
			Expect(fields).To(Equal([]string{"reachable", "last_seen"}))
		})

		It("reports an event nobody described, rather than an empty payload", func() {
			// The two are different answers. "This event carries no fields" and
			// "nobody said what this event carries" would otherwise read the
			// same to a host filling one.
			_, described := soundRegistration().Payload("widget.node-status.health")

			Expect(described).To(BeFalse())
		})
	})
})
