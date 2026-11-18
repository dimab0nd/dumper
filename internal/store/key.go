package store

import (
	"strings"
	"time"
)

// ObjectKey is the S3 key of one dump. The timestamp is UTC.
func ObjectKey(prefix, target, database string, at time.Time) string {
	stamp := at.UTC().Format("20060102T150405Z")
	return DatabasePrefix(prefix, target, database) + stamp + ".dump"
}

// DatabasePrefix is the key prefix that holds every dump of one database.
func DatabasePrefix(prefix, target, database string) string {
	parts := make([]string, 0, 3)
	if trimmed := strings.Trim(prefix, "/"); trimmed != "" {
		parts = append(parts, trimmed)
	}
	parts = append(parts, target, database)
	return strings.Join(parts, "/") + "/"
}
