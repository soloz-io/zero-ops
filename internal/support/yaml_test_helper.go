package support

import "gopkg.in/yaml.v3"

// yamlUnmarshal keeps the yaml dependency in one place, so a reader of this
// package sees one import of it rather than several.
func yamlUnmarshal(b []byte, v any) error { return yaml.Unmarshal(b, v) }
