package store

import (
	"testing"
	"time"
)

func TestObjectKey(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	at := time.Date(2026, 10, 17, 4, 2, 3, 0, msk)
	got := ObjectKey("vps-1", "myapp", "myapp", at)
	want := "vps-1/myapp/myapp/20261017T010203Z.dump"
	if got != want {
		t.Fatalf("ObjectKey() = %q, want %q", got, want)
	}
}

func TestObjectKeyPrefix(t *testing.T) {
	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		prefix string
		want   string
	}{
		{prefix: "", want: "myapp/db/20261017T010203Z.dump"},
		{prefix: "/vps-1/", want: "vps-1/myapp/db/20261017T010203Z.dump"},
		{prefix: "backups/vps-1", want: "backups/vps-1/myapp/db/20261017T010203Z.dump"},
	}
	for _, tt := range tests {
		got := ObjectKey(tt.prefix, "myapp", "db", at)
		if got != tt.want {
			t.Fatalf("ObjectKey(%q) = %q, want %q", tt.prefix, got, tt.want)
		}
	}
}

func TestDatabasePrefix(t *testing.T) {
	got := DatabasePrefix("/vps-1/", "myapp", "db")
	want := "vps-1/myapp/db/"
	if got != want {
		t.Fatalf("DatabasePrefix() = %q, want %q", got, want)
	}
}
