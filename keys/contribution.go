package keys

// Contribution is one source's complete current key set.
type Contribution struct {
	SchemaVersion int
	Records       []*Record
}
