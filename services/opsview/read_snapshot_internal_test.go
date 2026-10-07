// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	stdfs "io/fs"
	"log/slog"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// panelFixture is the snapshot shape the spec decodes.
type panelFixture struct {
	Name string `json:"name"`
}

var _ = Describe("readSnapshot", func() {
	const file = "panel.json"
	const noun = "fixture panel"

	var (
		log  *bytes.Buffer
		view *OpsView
	)
	viewOver := func(tree fstest.MapFS) *OpsView {
		log = &bytes.Buffer{}
		return &OpsView{files: tree, logger: slog.New(slog.NewTextHandler(log, nil))}
	}

	It("decodes the file into the typed snapshot", func() {
		view = viewOver(fstest.MapFS{file: {Data: []byte(`{"name":"queue"}`)}})

		snapshot, readable := readSnapshot[panelFixture](view, file, noun)

		Expect(readable).To(BeTrue())
		Expect(snapshot).To(Equal(&panelFixture{Name: "queue"}))
		Expect(log.String()).To(BeEmpty())
	})

	It("treats a file that does not exist yet as the empty panel, without a log line", func() {
		view = viewOver(fstest.MapFS{})

		snapshot, readable := readSnapshot[panelFixture](view, file, noun)

		Expect(readable).To(BeTrue())
		Expect(snapshot).To(BeNil())
		Expect(log.String()).To(BeEmpty())
	})

	It("logs a file that cannot be read and leaves the panel as it was", func() {
		view = viewOver(fstest.MapFS{file: {Mode: stdfs.ModeDir | 0o755}})

		snapshot, readable := readSnapshot[panelFixture](view, file, noun)

		Expect(readable).To(BeFalse())
		Expect(snapshot).To(BeNil())
		Expect(log.String()).To(ContainSubstring("ops view: fixture panel not read"))
	})

	It("logs a file that does not decode and leaves the panel as it was", func() {
		view = viewOver(fstest.MapFS{file: {Data: []byte(`{"name":`)}})

		snapshot, readable := readSnapshot[panelFixture](view, file, noun)

		Expect(readable).To(BeFalse())
		Expect(snapshot).To(BeNil())
		Expect(log.String()).To(ContainSubstring("ops view: fixture panel not decoded"))
	})
})
