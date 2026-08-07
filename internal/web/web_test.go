package web

import (
	"testing"
	"time"
)

func TestParseAccountDateTimePreservesAmbiguousOriginalInstant(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	original := time.Date(2024, 11, 3, 6, 30, 0, 0, time.UTC) // second 01:30, after fall-back

	parsed, err := parseAccountDateTimeIn("2024-11-03T01:30", original.Format(time.RFC3339Nano), location)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(original) {
		t.Fatalf("parsed = %s, want original instant %s", parsed, original)
	}
}
