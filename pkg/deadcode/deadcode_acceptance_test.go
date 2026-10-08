// Copyright 2026 Candace Labs

//go:build acceptance

package deadcode_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/deadcode"
)

// These specs load a fixture module through go list, a real process
// crossing, so they run only in the acceptance tier.
var _ = Describe("Find and Fix over a module", func() {
	var module string

	BeforeEach(func() {
		module = GinkgoT().TempDir()
		Expect(os.CopyFS(module, os.DirFS("testdata/module"))).To(Succeed())
	})

	names := func(functions []deadcode.Function) map[string]bool {
		found := map[string]bool{}
		for _, function := range functions {
			found[function.Name] = function.Removable()
		}
		return found
	}

	It("reports dead code, removable or kept, and nothing kept API calls", func() {
		found, err := deadcode.Find(module, "./...")
		Expect(err).NotTo(HaveOccurred())
		Expect(names(found)).To(Equal(map[string]bool{
			"deadInMain": true,
			"helper":     true,
			"named.Name": false,
			"Exported":   false,
		}))
	})

	It("prunes until nothing removable is left, keeping what the build needs", func() {
		removed, err := deadcode.Fix(module, "./...")
		Expect(err).NotTo(HaveOccurred())
		Expect(names(removed)).To(Equal(map[string]bool{"deadInMain": true, "helper": true}))

		library, err := os.ReadFile(filepath.Join(module, "lib", "lib.go"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(library)).NotTo(ContainSubstring("func helper"))
		Expect(string(library)).NotTo(ContainSubstring(`"strings"`), "the import only helper used is pruned")
		Expect(string(library)).To(ContainSubstring("func (named) Name"))
		Expect(string(library)).To(ContainSubstring("func Exported"))

		remaining, err := deadcode.Find(module, "./...")
		Expect(err).NotTo(HaveOccurred())
		Expect(names(remaining)).To(Equal(map[string]bool{"named.Name": false, "Exported": false}))
	})
})
