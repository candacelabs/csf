// Copyright 2026 Candace Labs

package docker_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
)

const location = `"container":"csf-metrics-0123456789ab","volume":"csf-metrics-0123456789ab-data","image":"i","host":"127.0.0.1","port":15999`

func decodeToken(content string) (docker.OwnedRecord[token], bool, error) {
	return docker.DecodeOwnedRecord("/state/metrics.json", []byte(content), func(settings token) *docker.RecordError {
		if settings.Secret == "" {
			return docker.RequiredField("secret", "restore it")
		}
		return nil
	})
}

var _ = Describe("DecodeOwnedRecord", func() {
	It("decodes the current shape and the previous flat one to the same record, and says which it read", func() {
		current, previous, err := decodeToken(`{` + location + `,"settings":{"secret":"s"}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(previous).To(BeFalse())
		flat, previous, err := decodeToken(`{` + location + `,"secret":"s"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(previous).To(BeTrue())
		Expect(flat).To(Equal(current))
		Expect(flat.Settings.Secret).To(Equal("s"))
	})

	It("refuses an unknown field in either shape, naming the file and the field", func() {
		_, _, err := decodeToken(`{` + location + `,"settings":{"secret":"s","extra":1}}`)
		Expect(err).To(MatchError(ContainSubstring(`/state/metrics.json: field "extra" is not part of this record`)))
		_, _, err = decodeToken(`{` + location + `,"secret":"s","extra":1}`)
		Expect(err).To(MatchError(ContainSubstring(`/state/metrics.json: field "extra" is not part of this record`)))
		Expect(err).To(MatchError(docker.ErrRecordInvalid))
	})

	It("refuses a missing location field and a missing required setting instead of a zero value", func() {
		_, _, err := decodeToken(`{"container":"c","volume":"v","image":"i","host":"127.0.0.1","settings":{"secret":"s"}}`)
		Expect(err).To(MatchError(ContainSubstring(`field "port" is missing or empty; fix: restore the field`)))
		_, _, err = decodeToken(`{` + location + `,"settings":{}}`)
		Expect(err).To(MatchError(`ipc/docker: /state/metrics.json: field "secret" is missing or empty; fix: restore it`))
	})

	It("names a file that is not a JSON object", func() {
		_, _, err := decodeToken(`[]`)
		Expect(err).To(MatchError(ContainSubstring(`/state/metrics.json: field "(file)" is not a JSON object`)))
	})
})

var _ = Describe("OwnedService.Ensure with a record it cannot act on", func() {
	It("refuses a mount whose host path decoded empty and never creates a directory or touches the Engine", func(ctx SpecContext) {
		host := newOwner()
		definition := host.definition()
		definition.Spec = func(record docker.OwnedRecord[token]) docker.ServiceSpec {
			return docker.ServiceSpec{Mounts: []docker.Mount{{Source: "", Target: "/exports"}}, Port: "9090/tcp"}
		}
		Expect(os.WriteFile(filepath.Join(host.state, "metrics.json"), []byte(`{`+location+`,"settings":{"secret":"s"}}`), 0o600)).To(Succeed())
		host.services.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Times(0)
		owned, err := docker.NewOwnedService(definition, docker.WithServices(host.services), docker.WithListener(host.listener))
		Expect(err).NotTo(HaveOccurred())

		_, err = owned.Ensure(ctx)
		Expect(err).To(MatchError(docker.ErrRecordInvalid))
		Expect(err).To(MatchError(ContainSubstring(`field "/exports" names the host path "", which is not absolute`)))
	})
})
