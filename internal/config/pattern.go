package config

import "path"

func validPattern(p string) error { _, err := path.Match(p, "test"); return err }
