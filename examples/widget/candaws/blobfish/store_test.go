package blobfish

import (
	"context"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
	"github.com/candacelabs/csf/pkg/patience"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Store specifications use production routines and real timers. Most run the
// full bucket and coordinator; the repair specification supplies replica work
// directly so its anti-entropy ordering is deterministic.
const (
	specDelay    = 5 * time.Millisecond
	specCadence  = 25 * time.Millisecond
	specPatience = 15 * time.Second
)

var specWaitBudget = patience.Budget{Within: specPatience}

// specConfig is the pace every specification in this file runs at. The slow
// zone is the parameter, because a store whose zones are all the same speed and
// one that has a zone the quorum does not wait for are the two states this
// engine exists to tell apart.
func specConfig(slowZone int) Config {
	return Config{
		StorageClass:   "Glacial",
		Cadence:        specCadence,
		ReplicaDelay:   specDelay,
		DelaySpread:    2 * specDelay,
		SlowZone:       slowZone,
		SlowFactor:     20,
		Patience:       12 * specDelay,
		RepairInterval: 4 * specCadence,
		Seed:           20260902,
	}
}

// stream is one subscriber, read only by the specification's own goroutine.
type stream struct {
	views <-chan StoreView
	seen  []StoreView
}

// await takes views until one matches, checking on the way that no view ever
// claimed a quorum wider than the zone set.
func (subscriber *stream) await(what string, match func(view StoreView) bool) StoreView {
	GinkgoHelper()
	return patience.Await(GinkgoTB(), what, specWaitBudget,
		func() StoreView {
			var last StoreView
			for {
				select {
				case view, open := <-subscriber.views:
					Expect(open).To(BeTrue(), "the store stopped before %s", what)
					Expect(view.WriteAcks).To(BeNumerically("<=", zoneCount))
					Expect(view.LaggingZones).To(BeNumerically("<", zoneCount),
						"every zone cannot be behind every zone")
					subscriber.seen = append(subscriber.seen, view)
					last = view
					if match(view) {
						return view
					}
				default:
					return last
				}
			}
		}, func(view StoreView) bool { return view.StorageClass != "" && match(view) })
}

// storeUnder starts a store for the length of one specification, with one
// subscriber attached, and joins its goroutines on the way out.
func storeUnder(config Config) (*Store, *stream) {
	GinkgoHelper()

	store, buildError := NewStore(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- store.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		var runError error
		patience.Await(GinkgoTB(), "the store goroutines to stop", specWaitBudget,
			func() bool {
				select {
				case runError = <-stopped:
					return true
				default:
					return false
				}
			}, func(stopped bool) bool { return stopped })
		Expect(runError).ToNot(HaveOccurred())
	})

	views, subscribeError := store.Watch(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return store, &stream{views: views}
}

// repairStoreUnder starts only the replicas and repairer. The specification
// supplies replica work directly so the bucket cannot keep adding writes while
// the repair assertion is observing one deliberately lagging zone.
func repairStoreUnder(config Config) *Store {
	GinkgoHelper()

	store, buildError := NewStore(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{}, zoneCount+1)
	for zone := range zoneCount {
		member := &replica{
			id:     zone,
			inbox:  store.inboxes[zone],
			acks:   store.acks,
			delay:  store.config.zoneDelay(zone),
			spread: store.config.DelaySpread,
			jitter: fleet.Jitter(store.config.Seed, uint64(zone)),
		}
		go func(member *replica) {
			member.run(ctx)
			stopped <- struct{}{}
		}(member)
	}
	go func() {
		store.repair(ctx)
		stopped <- struct{}{}
	}()

	DeferCleanup(func() {
		cancel()
		finished := 0
		patience.Await(GinkgoTB(), "the repair workers to stop", specWaitBudget,
			func() int {
				for finished < zoneCount+1 {
					select {
					case <-stopped:
						finished++
					default:
						return finished
					}
				}
				return finished
			}, func(finished int) bool { return finished == zoneCount+1 })
	})

	return store
}

func (store *Store) awaitRepair(what string, lagging int) storeReport {
	GinkgoHelper()

	return patience.Await(GinkgoTB(), what, specWaitBudget,
		func() storeReport {
			for {
				select {
				case report := <-store.reports:
					if report.Kind == reportRepair {
						return report
					}
				default:
					return storeReport{}
				}
			}
		}, func(report storeReport) bool {
			return report.Kind == reportRepair && report.Lagging == lagging
		})
}

var _ = Describe("A store that is configured wrongly", func() {
	It("reports the fault rather than running and never acknowledging", func() {
		sound := specConfig(-1)
		for _, broken := range []struct {
			what   string
			change func(config *Config)
			fault  error
		}{
			{"no storage class", func(config *Config) { config.StorageClass = "" }, ErrStorageClass},
			{"no cadence", func(config *Config) { config.Cadence = 0 }, ErrCadence},
			{"no zone latency", func(config *Config) { config.ReplicaDelay = 0 }, ErrReplicaDelay},
			{"a slow zone that is not a zone",
				func(config *Config) { config.SlowZone = zoneCount }, ErrSlowZone},
			{"a slow zone that is not slower",
				func(config *Config) { config.SlowFactor = 0 }, ErrSlowFactor},
			{"a coordinator that gives up before a healthy zone can answer",
				func(config *Config) { config.Patience = time.Nanosecond }, ErrPatience},
			{"no repair sweep",
				func(config *Config) { config.RepairInterval = 0 }, ErrRepairInterval},
		} {
			config := sound
			broken.change(&config)
			_, buildError := NewStore(config)
			Expect(buildError).To(MatchError(broken.fault), "for %s", broken.what)
		}
	})

	It("refuses a second Run rather than minting two generation counters", func() {
		store, _ := storeUnder(specConfig(-1))
		Expect(store.Run(context.Background())).To(MatchError(ContainSubstring("already running")))
	})
})

var _ = Describe("A store whose zones are all the same speed", func() {
	It("acknowledges a write, serves a read, and finds nothing to repair", func() {
		_, subscriber := storeUnder(specConfig(-1))

		durable := subscriber.await("a durable write",
			func(view StoreView) bool { return view.Writable && view.Generation >= 2 })
		Expect(durable.Durable()).To(BeTrue())
		Expect(durable.Serving()).To(BeTrue())
		Expect(durable.StorageClass).To(Equal("Glacial"),
			"the storage class is a string the card renders and never computes")
		Expect(durable.Objects).To(BeNumerically(">=", 1))
	})
})

var _ = Describe("A store with a zone the quorum does not wait for", func() {
	It("replies at the quorum and stops counting", func() {
		// The third zone is twenty times slower than the coordinator's whole
		// patience allows for, so it cannot be one of the two that answered.
		// The write is durable anyway, and that is the entire point: the
		// coordinator replied and never un-replied.
		_, subscriber := storeUnder(specConfig(zoneCount - 1))

		durable := subscriber.await("a write acknowledged by a quorum",
			func(view StoreView) bool { return view.Writable })
		Expect(durable.WriteAcks).To(Equal(quorum),
			"stopping at the quorum is a select that stopped counting, not a timeout")
		Expect(durable.WriteAcks).To(BeNumerically("<", zoneCount))

		last := durable.Generation
		for range 2 {
			later := subscriber.await("a subsequent write generation",
				func(view StoreView) bool { return view.Generation > last })
			Expect(later.Generation).To(BeNumerically(">", last))
			last = later.Generation
		}
		highest := uint64(0)
		for _, view := range subscriber.seen {
			Expect(view.Generation).To(BeNumerically(">=", highest),
				"a generation is never reused for a key, so it never walks backwards")
			highest = view.Generation
		}
	})

	It("finds the zone behind and repairs it forward", func() {
		store := repairStoreUnder(specConfig(zoneCount - 1))

		// One quorum is ahead while the slow replica is idle at generation zero.
		// The repair sweeps then own the ordering: the first sees exactly one
		// zone behind and queues its repair before the next probe.
		for zone := range quorum {
			store.inboxes[zone] <- replicaOp{Kind: opWrite, Generation: 1}
		}
		acks := map[int]replicaAck{}
		patience.Await(GinkgoTB(), "the fast replicas to acknowledge generation one", specWaitBudget,
			func() int {
				for {
					select {
					case ack := <-store.acks:
						if ack.Kind == opWrite && ack.Generation == 1 {
							acks[ack.Zone] = ack
						}
					default:
						return len(acks)
					}
				}
			}, func(count int) bool { return count == quorum })
		for zone := range quorum {
			ack, acknowledged := acks[zone]
			Expect(acknowledged).To(BeTrue(), "fast zone %d acknowledged", zone)
			Expect(ack.Zone).To(Equal(zone))
		}

		behind := store.awaitRepair("a repair sweep with exactly one zone behind", 1)
		Expect(behind.Lagging).To(Equal(1),
			"the two fast zones are current while the slow zone is repaired")

		caughtUp := store.awaitRepair("the next repair sweep to bring every zone forward", 0)
		Expect(caughtUp.Lagging).To(BeZero(),
			"the probe after repair sees the slow replica at the quorum generation")

		reply := make(chan generationReport, 1)
		store.inboxes[zoneCount-1] <- replicaOp{Kind: opProbe, Reply: reply}
		repaired := patience.Await(GinkgoTB(), "the slow replica to report generation one", specWaitBudget,
			func() generationReport {
				select {
				case report := <-reply:
					return report
				default:
					return generationReport{}
				}
			}, func(report generationReport) bool {
				return report.Zone == zoneCount-1 && report.Generation >= 1
			})
		Expect(repaired.Generation).To(Equal(uint64(1)))
	})
})
