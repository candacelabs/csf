package main

import (
	"bufio"
	"bytes"
	"net/http/httptest"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("CSF command adapters", func() {
	It("calls a generated operation with an empty JSON request", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine("csf-command-call")
		service.Register(router)
		server := httptest.NewServer(router)
		DeferCleanup(server.Close)
		output := &bytes.Buffer{}
		Expect(callWithStreams([]string{"--endpoint", server.URL, "GetSnapshot"}, bytes.NewReader(nil), output)).To(Succeed())
		response := &pb.GetSnapshotResponse{}
		Expect(protojson.Unmarshal(output.Bytes(), response)).To(Succeed())
		Expect(response.Snapshot.Issues).To(ContainElement(ContainSubstring("no event source configured")))
	})

	It("writes one JSON response for each JSONL runtime request", func() {
		request, err := protojson.Marshal(&pb.RuntimeRequest{
			Kind:        pb.RequestKind_REQUEST_KIND_STEP,
			Observation: &pb.Observation{Epoch: 1, Sequence: 1, Tick: 1, Features: []int64{0, 0, 0, 0}},
			Tick:        1,
		})
		Expect(err).NotTo(HaveOccurred())
		input := bytes.NewBuffer(append([]byte("not JSON\n"), append(request, '\n')...))
		output := &bytes.Buffer{}
		Expect(runWithStreams(input, output)).To(Succeed())
		scanner := bufio.NewScanner(output)
		Expect(scanner.Scan()).To(BeTrue())
		invalid := &pb.RuntimeResponse{}
		Expect(protojson.Unmarshal(scanner.Bytes(), invalid)).To(Succeed())
		Expect(invalid.Error).NotTo(BeEmpty())
		Expect(scanner.Scan()).To(BeTrue())
		fallback := &pb.RuntimeResponse{}
		Expect(protojson.Unmarshal(scanner.Bytes(), fallback)).To(Succeed())
		Expect(fallback.Action.Fallback).To(BeTrue())
		Expect(fallback.Action.Reason).To(Equal("no_active_controller_or_observation"))
		Expect(scanner.Scan()).To(BeFalse())
		Expect(scanner.Err()).NotTo(HaveOccurred())
	})
})
