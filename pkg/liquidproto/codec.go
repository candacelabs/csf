package liquidproto

import (
	"errors"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	// ErrNilConstructor indicates that a Codec was configured without a
	// message constructor.
	ErrNilConstructor = errors.New("liquidproto: nil message constructor")
	// ErrNilValidator indicates that a Codec was configured without a
	// contract validator.
	ErrNilValidator = errors.New("liquidproto: nil message validator")
	// ErrNilMessage indicates that a caller supplied, or a constructor
	// returned, a typed nil protobuf message.
	ErrNilMessage = errors.New("liquidproto: nil protobuf message")
	// ErrWrongMessageType indicates that a caller supplied a protobuf type that
	// differs from the constructor bound to the Codec.
	ErrWrongMessageType = errors.New("liquidproto: wrong protobuf message type")
)

// Constructor returns a fresh protobuf message for one Unmarshal call.
type Constructor[T proto.Message] func() T

// Validator checks the complete application contract for a message.
type Validator[T proto.Message] func(message T) error

// Codec deterministically marshals and validates one protobuf message type.
//
// A Codec validates immediately before Marshal and immediately after
// Unmarshal. The constructor must return a fresh, non-nil message on every
// call. Codec values are safe for concurrent use when the supplied functions
// are safe for concurrent use.
type Codec[T proto.Message] struct {
	construct  Constructor[T]
	validate   Validator[T]
	descriptor protoreflect.MessageDescriptor
}

// NewCodec constructs a validating deterministic protobuf codec.
func NewCodec[T proto.Message](construct Constructor[T], validate Validator[T]) (*Codec[T], error) {
	if construct == nil {
		return nil, ErrNilConstructor
	}
	if validate == nil {
		return nil, ErrNilValidator
	}
	sample := construct()
	if isNilMessage(sample) {
		return nil, fmt.Errorf("liquidproto: constructor: %w", ErrNilMessage)
	}
	descriptor := sample.ProtoReflect().Descriptor()
	if descriptor == nil || descriptor.FullName() == "" {
		return nil, errors.New("liquidproto: constructor returned a message without a descriptor")
	}
	return &Codec[T]{construct: construct, validate: validate, descriptor: descriptor}, nil
}

// MessageType returns the fully-qualified protobuf name bound to the Codec.
func (c *Codec[T]) MessageType() string { return string(c.descriptor.FullName()) }

// New returns a fresh non-nil message from the configured constructor. It is
// useful for descriptor inspection and integrations that need a typed decode
// target. The returned empty message has not passed validation.
func (c *Codec[T]) New() (T, error) {
	var zero T
	message := c.construct()
	if isNilMessage(message) {
		return zero, fmt.Errorf("liquidproto: constructor: %w", ErrNilMessage)
	}
	if got := message.ProtoReflect().Descriptor(); got != c.descriptor {
		return zero, fmt.Errorf("%w: expected=%q got=%q", ErrWrongMessageType, c.MessageType(), got.FullName())
	}
	return message, nil
}

// Marshal validates message and returns deterministic protobuf wire bytes.
//
// Deterministic protobuf encoding is stable for repeated marshals by the same
// binary. Protobuf does not define it as a canonical encoding across languages
// or schema changes, so callers must not use these bytes as a permanent hash.
func (c *Codec[T]) Marshal(message T) ([]byte, error) {
	if isNilMessage(message) {
		return nil, ErrNilMessage
	}
	if got := message.ProtoReflect().Descriptor(); got != c.descriptor {
		return nil, fmt.Errorf("%w: expected=%q got=%q", ErrWrongMessageType, c.MessageType(), got.FullName())
	}
	if err := c.validate(message); err != nil {
		return nil, fmt.Errorf("liquidproto: validate before marshal: %w", err)
	}
	wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("liquidproto: marshal: %w", err)
	}
	return wire, nil
}

// Unmarshal decodes wire into a fresh message and validates the result.
// Unknown protobuf fields are retained for forward-compatible re-encoding.
func (c *Codec[T]) Unmarshal(wire []byte) (T, error) {
	var zero T
	message, err := c.New()
	if err != nil {
		return zero, err
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(wire, message); err != nil {
		return zero, fmt.Errorf("liquidproto: unmarshal: %w", err)
	}
	if err := c.validate(message); err != nil {
		return zero, fmt.Errorf("liquidproto: validate after unmarshal: %w", err)
	}
	return message, nil
}

func isNilMessage[T proto.Message](message T) bool {
	value := reflect.ValueOf(message)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
