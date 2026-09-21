package blobfish

// StoreView is what the store tells anybody, and it is deliberately the shape
// the Blobfish card's `replicaReport` event carries.
type StoreView struct {
	// Sequence is this view's position in the stream, from 1.
	Sequence uint64

	// Generation is the highest generation the coordinator has minted. It is
	// never reused for a key, which is what makes it the card's tick.
	Generation uint64

	// StorageClass is the tier this store's objects are in. It is the fleet's
	// second text field, and it never changes for a given store — which is
	// itself worth having in the view, because a card that can render a string
	// it did not compute is a card that can render a name.
	StorageClass string

	// Objects is how many objects the bucket holds.
	Objects int

	// WriteAcks is how many zones answered the last write before the
	// coordinator stopped counting.
	WriteAcks int

	// LaggingZones is how many zones the last repair sweep found behind.
	LaggingZones int

	// Writable is whether the last write reached its quorum, and Readable
	// whether the last read reached its own.
	Writable bool
	Readable bool
}

// foldReport merges one half of the picture into the published view.
//
// It is a pure function, which is what lets the merge be specified without
// starting a goroutine: the coordinator cannot see how many zones are behind and
// the repairer cannot see whether the last write landed, so every rule about
// what the card shows is a rule about this function.
func foldReport(view StoreView, incoming storeReport) StoreView {
	next := view
	switch incoming.Kind {
	case reportWrite:
		next.Generation = incoming.Generation
		next.Acks(incoming.Acks)
		next.Objects = incoming.Objects
		next.Writable = incoming.Writable
		next.Readable = incoming.Readable
	case reportRepair:
		next.LaggingZones = incoming.Lagging
	}
	return next
}

// Acks records how many zones answered the last write.
//
// It is a method rather than a field assignment because the count is capped at
// the zone set: a coordinator that stopped counting at the quorum reports the
// quorum, and a view claiming more acknowledgements than there are zones would
// be a card reporting durability nobody has.
func (view *StoreView) Acks(counted int) {
	if counted > zoneCount {
		counted = zoneCount
	}
	if counted < 0 {
		counted = 0
	}
	view.WriteAcks = counted
}

// Durable reports whether the last write reached the write quorum.
func (view StoreView) Durable() bool { return view.WriteAcks >= quorum }

// Serving reports whether both quorums were met, which is what the card's
// `serving` predicate composes.
func (view StoreView) Serving() bool { return view.Writable && view.Readable }
