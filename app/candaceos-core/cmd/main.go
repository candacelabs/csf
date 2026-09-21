// Command candaceos-core is the single-operator CandaceOS control plane.
package main

import "github.com/candacelabs/csf/app/candaceos-core/bootstrap"

var version = "dev"

func main() {
	if err := bootstrap.Run(version, bootstrap.WithPII()); err != nil {
		panic(err)
	}
}
