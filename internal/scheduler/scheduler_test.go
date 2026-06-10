package scheduler

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

func TestBuildCronSpec(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		timezone string
		wantErr  bool
		want     string
	}{
		{"plain", "0 3 * * *", "", false, "0 3 * * *"},
		{"with tz", "0 3 * * *", "Europe/Kyiv", false, "CRON_TZ=Europe/Kyiv 0 3 * * *"},
		{"macro", "@hourly", "", false, "@hourly"},
		{"bad schedule", "61 99 * * *", "", true, ""},
		{"bad tz", "0 3 * * *", "Mars/Olympus", true, ""},
		{"empty", "", "", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildCronSpec(tt.schedule, tt.timezone)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildCronSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("BuildCronSpec() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNextRun(t *testing.T) {
	now := time.Date(2026, 6, 10, 2, 0, 0, 0, time.UTC)
	next, err := NextRun("0 3 * * *", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("NextRun() = %v, want %v", next, want)
	}
}

func TestUpsertRemove(t *testing.T) {
	s := New()
	key := types.NamespacedName{Namespace: "ns", Name: "job"}

	if err := s.Upsert(key, "* * * * *", func() {}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", s.Len())
	}

	// Same spec: entry kept, still one.
	if err := s.Upsert(key, "* * * * *", func() {}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() after same-spec upsert = %d, want 1", s.Len())
	}

	// New spec: replaced, still one.
	if err := s.Upsert(key, "*/5 * * * *", func() {}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() after re-spec upsert = %d, want 1", s.Len())
	}

	s.Remove(key)
	if s.Len() != 0 {
		t.Fatalf("Len() after remove = %d, want 0", s.Len())
	}
	// Removing a missing key is a no-op.
	s.Remove(key)
}
