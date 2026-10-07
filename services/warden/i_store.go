package warden

// IStore persists PersistentState. Save must be atomic and durable before
// returning (write-then-rename for the file implementation). Load returns
// ok == false when no state has ever been saved.
type IStore interface {
	Save(st PersistentState) error
	Load() (st PersistentState, ok bool, err error)
}
