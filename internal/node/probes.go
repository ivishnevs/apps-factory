package node

// Runtime probes live with their drivers (codex.Driver.Probe, claude.Driver.Probe); these helpers let them
// check files the same way the doctor does.

// Exists reports whether path is a regular file.
func (h Host) Exists(path string) bool { return exists(h, path) }

// BinaryCheck checks that the factory binary name exists in dir (see Doctor.BinDir).
func (h Host) BinaryCheck(name, dir string) CheckResult { return binary(name, dir, h) }
