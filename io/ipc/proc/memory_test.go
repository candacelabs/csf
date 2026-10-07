// Copyright 2026 Candace Labs

package proc_test

import (
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
)

var _ = Describe("ReadMemoryAvailable", func() {
	It("reads MemAvailable in bytes from the process table", func() {
		processes := fstest.MapFS{"meminfo": &fstest.MapFile{
			Data: []byte("MemTotal:       70316204 kB\nMemFree:         1465672 kB\nMemAvailable:   50621872 kB\n"),
		}}

		available, err := proc.ReadMemoryAvailable(processes)
		Expect(err).NotTo(HaveOccurred())
		Expect(available).To(Equal(uint64(50621872 * 1024)))
	})

	It("refuses meminfo without the MemAvailable line", func() {
		_, err := proc.ReadMemoryAvailable(fstest.MapFS{"meminfo": &fstest.MapFile{Data: []byte("MemTotal: 1 kB\n")}})
		Expect(err).To(MatchError(ContainSubstring("MemAvailable")))
	})

	It("reports an unreadable meminfo file as the file error", func() {
		_, err := proc.ReadMemoryAvailable(fstest.MapFS{})
		Expect(err).To(HaveOccurred())
	})
})
