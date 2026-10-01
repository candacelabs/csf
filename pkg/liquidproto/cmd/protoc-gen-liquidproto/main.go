// protoc-gen-liquidproto compiles Liquid Proto field refinements into native
// Go validation boundaries.
package main

import (
	"github.com/candacelabs/csf/pkg/liquidproto/cmd/protoc-gen-liquidproto/internal/gen"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	protogen.Options{}.Run(func(plugin *protogen.Plugin) error {
		plugin.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		return gen.Run(plugin)
	})
}
