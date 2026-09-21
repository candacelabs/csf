package livetest_test

import (
	"fmt"

	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// Example is the decoded frame a spec asserts on, and the three questions it
// is built to answer.
//
// The harness that produces one — NewClient, dialing a real socket against a
// real handler — takes a testing.TB, and a godoc example takes no arguments,
// so it cannot appear here. What it hands back can: a [Frame] is plain values
// with no generated protobuf type anywhere in the assertion, which is the
// property api-surface.md §6 records and the reason this view exists at all.
// The value below is written out by hand for that reason; in a spec it comes
// from Client.Next, Client.Await or Client.WaitFor.
//
// The three questions, in the order they are usually asked:
//
//   - Did this patch touch a region? Fragment answers with two values, so "the
//     region rendered empty" and "the region was not in this patch" are
//     different answers. The second is the independent-live-regions property.
//   - Which regions did it carry? FragmentIDs, in wire order.
//   - What did it cost? HTMLBytes, with the per-region split, because "the
//     snapshot was N bytes" is only useful beside which region spent them.
//
// Frame's String is what a failure message prints. It leads with the causal
// chain rather than the markup, because the markup is rarely why a spec failed.
func Example() {
	frame := &livetest.Frame{
		Kind: livetest.FramePatch,
		Patch: &livetest.Patch{
			ServerSeq:    7,
			PatchID:      7,
			TransitionID: 9,
			StateVersion: 4,
			Origin: livetest.Origin{
				Kind:      1, // CLIENT_EVENT
				EventID:   12,
				ClientRef: 3,
				Source:    "event:counter.increment",
			},
			Updates: []livetest.Update{
				{FragmentID: "counter", HTML: `<b data-gotth-region="counter">42</b>`},
				{FragmentID: "total", HTML: `<span data-gotth-region="total">42</span>`},
			},
		},
	}

	html, carried := frame.Patch.Fragment("counter")
	fmt.Printf("counter carried=%t html=%s\n", carried, html)

	_, carried = frame.Patch.Fragment("controls")
	fmt.Printf("controls carried=%t\n", carried)

	fmt.Println("regions:", frame.Patch.FragmentIDs())

	total, per := frame.Patch.HTMLBytes()
	fmt.Printf("html bytes: total=%d counter=%d total-region=%d\n", total, per["counter"], per["total"])

	fmt.Println(frame)

	// Output:
	// counter carried=true html=<b data-gotth-region="counter">42</b>
	// controls carried=false
	// regions: [counter total]
	// html bytes: total=78 counter=37 total-region=41
	// patch{seq=7 origin=event:counter.increment/1 fragments=[counter total] contributing=[]}
}
