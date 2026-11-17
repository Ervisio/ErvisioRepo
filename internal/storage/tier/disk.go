package tier

// DiskUsage is the space of the file system holding a local target.
type DiskUsage struct {
	Used, Total int64
}

// LocalDisk returns the disk usage of the first local target in
// configuration order; ok is false without a local target or when the file
// system cannot be read.
func (t *Tier) LocalDisk() (name string, d DiskUsage, ok bool) {
	for _, tc := range t.cfg.Targets {
		if tc.Kind != "local" {
			continue
		}
		d, err := diskUsage(tc.Path)
		return tc.Name, d, err == nil
	}
	return "", DiskUsage{}, false
}

// targetDisks returns the disk usage of every readable local target.
func (t *Tier) targetDisks() map[string]DiskUsage {
	out := map[string]DiskUsage{}
	for _, tc := range t.cfg.Targets {
		if tc.Kind != "local" {
			continue
		}
		if d, err := diskUsage(tc.Path); err == nil {
			out[tc.Name] = d
		}
	}
	return out
}
