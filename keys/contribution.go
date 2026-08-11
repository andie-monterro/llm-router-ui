package keys

// Contribution is one source's complete current key set.
type Contribution struct {
	SchemaVersion int
	Records       []*Record
}

func (c *Contribution) clone(source string) *Contribution {
	if c == nil {
		return nil
	}
	clone := &Contribution{
		SchemaVersion: c.SchemaVersion,
		Records:       make([]*Record, 0, len(c.Records)),
	}
	for _, record := range c.Records {
		if record == nil {
			clone.Records = append(clone.Records, nil)
			continue
		}
		owned := *record
		owned.AllowedModels = append([]string(nil), record.AllowedModels...)
		owned.AllowedRoutes = append([]string(nil), record.AllowedRoutes...)
		if record.ExpiresAt != nil {
			expiresAt := *record.ExpiresAt
			owned.ExpiresAt = &expiresAt
		}
		owned.Source = source
		clone.Records = append(clone.Records, &owned)
	}
	return clone
}
