package liquidproto_test

import (
	"errors"
	"fmt"

	"github.com/candacelabs/csf/pkg/liquidproto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func ExampleNewCodec() {
	codec, err := liquidproto.NewCodec(
		func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) },
		func(message *wrapperspb.StringValue) error {
			if message.GetValue() == "" {
				return errors.New("value is empty")
			}
			return nil
		},
	)
	if err != nil {
		panic(err)
	}

	wire, err := codec.Marshal(wrapperspb.String("hello"))
	if err != nil {
		panic(err)
	}
	decoded, err := codec.Unmarshal(wire)
	if err != nil {
		panic(err)
	}

	fmt.Println(codec.MessageType())
	fmt.Println(decoded.GetValue())
	// Output:
	// google.protobuf.StringValue
	// hello
}
