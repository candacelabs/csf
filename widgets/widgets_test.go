// Copyright 2026 Candace Labs

package widgets_test

import (
	"context"
	"encoding/json"
	"os"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
	"github.com/candacelabs/csf/services/opsview"
)

// Every definition in this directory passes the check the Workbench runs
// before installing it, and every fixture renders the text it expects with
// the widget mounted in a registry of its own, the way a host mounts it. A
// definition that fails here is one the Workbench would refuse.
var _ = Describe("The widget definitions", func() {
	definitions, err := iofs.NewHostFiles(".")
	if err != nil {
		panic(err)
	}
	read, err := opsview.ReadDefinitions(definitions)
	if err != nil {
		panic(err)
	}

	It("holds at least one definition", func() {
		Expect(read).NotTo(BeEmpty())
	})

	for _, installed := range opsview.InstallWidgets(nil, read, opsview.WidgetSources()) {
		It(installed.Name+" passes the install check", func() {
			Expect(installed.Reasons).To(BeEmpty())
			Expect(installed.Installed()).To(BeTrue())
		})

		It(installed.Name+" renders each fixture mounted in a registry", func() {
			checked, reasons := widget.CheckDefinition[live.AnonymousIdentity](definitions, installed.Name, opsview.WidgetSources())
			Expect(reasons).To(BeEmpty())
			content, err := os.ReadFile(path.Join(installed.Name, widget.FixturesFile))
			Expect(err).NotTo(HaveOccurred())
			var fixtures []widget.Fixture
			Expect(json.Unmarshal(content, &fixtures)).To(Succeed())
			for _, fixture := range fixtures {
				card, err := widgettest.Mount(context.Background(), checked.Widget)
				Expect(err).NotTo(HaveOccurred())
				event, err := checked.Event(fixture.Source)
				Expect(err).NotTo(HaveOccurred())
				card.Apply(event)
				rendered, err := card.Render(context.Background())
				Expect(err).NotTo(HaveOccurred())
				landmark, found := rendered.Landmark()
				Expect(found).To(BeTrue())
				Expect(landmark.Named).To(BeTrue(), "fixture %s: the widget is a named landmark", fixture.Name)
				for _, expected := range fixture.Expect {
					Expect(rendered.Has(expected)).To(BeTrue(), "fixture %s shows %q in\n%s", fixture.Name, expected, rendered)
				}
			}
		})
	}
})
