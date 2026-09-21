package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

func readIncidents(path string) []warden.Incident {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "reading %s", path)
	var out []warden.Incident
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var inc warden.Incident
		Expect(json.Unmarshal(line, &inc)).To(Succeed(), "invalid JSON line %q", line)
		out = append(out, inc)
	}
	return out
}

var _ = Describe("FileNotifier", func() {
	// TestFileNotifierAppendsJSONLines
	It("appends one JSON line per incident, preserving identity and order", func() {
		path := filepath.Join(GinkgoT().TempDir(), "incidents.jsonl")
		fn := NewFileNotifier(path)
		ctx := context.Background()

		want := []warden.Incident{deadIncident("a"), recoveryIncident("a"), deadIncident("b")}
		for _, inc := range want {
			Expect(fn.Notify(ctx, inc)).To(Succeed(), "Notify(%s)", inc.ID)
		}

		got := readIncidents(path)
		Expect(got).To(HaveLen(len(want)))
		for i := range want {
			Expect(got[i].ID).To(Equal(want[i].ID), "line %d ID", i)
			Expect(got[i].Type).To(Equal(want[i].Type), "line %d Type", i)
			Expect(got[i].Peer.ID).To(Equal(want[i].Peer.ID), "line %d Peer.ID", i)
		}
	})

	// TestFileNotifierConcurrent
	It("does not interleave or lose lines under concurrent Notify calls", func() {
		path := filepath.Join(GinkgoT().TempDir(), "incidents.jsonl")
		fn := NewFileNotifier(path)
		ctx := context.Background()

		const n = 200
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(fn.Notify(ctx, deadIncident(fmt.Sprintf("p%d", i)))).To(Succeed())
			}(i)
		}
		wg.Wait()

		got := readIncidents(path)
		Expect(got).To(HaveLen(n), "interleaved/lost lines")
		seen := make(map[warden.NodeID]bool, n)
		for _, inc := range got {
			seen[inc.Peer.ID] = true
		}
		for i := 0; i < n; i++ {
			id := warden.NodeID(fmt.Sprintf("p%d", i))
			Expect(seen[id]).To(BeTrue(), "missing incident for peer %q", id)
		}
	})
})
