package config

import (
	"errors"
	"fmt"

	"github.com/candacelabs/csf/pkg/liquidproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// LabelRefinementError translates a Liquid Proto schema field into the label
// used at the consuming configuration boundary, such as an environment name.
// Liquid owns the invariant; this function only makes its error actionable to
// the operator and returns the original error unchanged when it cannot map the
// field safely.
func LabelRefinementError(
	message proto.Message,
	label func(field protoreflect.FieldDescriptor) string,
	err error,
) error {
	var violation *liquidproto.Error
	if message == nil || !errors.As(err, &violation) {
		return err
	}
	field := message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(violation.Field))
	if field == nil {
		return err
	}
	return fmt.Errorf("%s: %w", label(field), err)
}
