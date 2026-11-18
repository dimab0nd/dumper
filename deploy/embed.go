// Package deploy holds the systemd units and example config embedded into the dumper binary.
package deploy

import "embed"

// Files is the install payload: dumper.service, dumper.timer, and config.example.yaml.
//
//go:embed dumper.service dumper.timer config.example.yaml
var Files embed.FS
