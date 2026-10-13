package skills

import "gopkg.in/yaml.v3"

func decodeMetadata(data []byte, v any) error { return yaml.Unmarshal(data, v) }
