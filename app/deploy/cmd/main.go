// Command deploy is the single-operator deploy control plane.
package main

import "github.com/candacelabs/csf/app/deploy/bootstrap"

var version = "dev"

func main() {
	if err := bootstrap.Run(version, bootstrap.WithPII()); err != nil {
		panic(err)
	}
}
