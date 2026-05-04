package observability

import (
	"net/url"
	"testing"
	"time"
)

func TestParseFilterDefaultsAndClampsLimit(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	filter, err := ParseFilter(url.Values{"limit": {"999"}}, now)
	if err != nil {
		t.Fatalf("ParseFilter returned error: %v", err)
	}

	if !filter.Since.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("Since = %s, want %s", filter.Since, now.Add(-7*24*time.Hour))
	}
	if !filter.Until.Equal(now) {
		t.Fatalf("Until = %s, want %s", filter.Until, now)
	}
	if filter.Limit != 200 {
		t.Fatalf("Limit = %d, want 200", filter.Limit)
	}
	if filter.BucketWidth != "day" {
		t.Fatalf("BucketWidth = %q, want day", filter.BucketWidth)
	}
}

func TestParseFilterSupportsRelativeDurationsAndRFC3339(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	relative, err := ParseFilter(url.Values{"since": {"24h"}}, now)
	if err != nil {
		t.Fatalf("ParseFilter relative returned error: %v", err)
	}
	if !relative.Since.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("relative Since = %s, want %s", relative.Since, now.Add(-24*time.Hour))
	}
	if relative.BucketWidth != "hour" {
		t.Fatalf("relative BucketWidth = %q, want hour", relative.BucketWidth)
	}

	absoluteSince := "2026-05-01T00:00:00Z"
	absolute, err := ParseFilter(url.Values{"since": {absoluteSince}}, now)
	if err != nil {
		t.Fatalf("ParseFilter absolute returned error: %v", err)
	}
	want, _ := time.Parse(time.RFC3339, absoluteSince)
	if !absolute.Since.Equal(want) {
		t.Fatalf("absolute Since = %s, want %s", absolute.Since, want)
	}
}

func TestParseFilterRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	for name, values := range map[string]url.Values{
		"bad since":      {"since": {"later"}},
		"zero duration":  {"since": {"0h"}},
		"bad limit":      {"limit": {"many"}},
		"negative limit": {"limit": {"-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFilter(values, now); err == nil {
				t.Fatal("ParseFilter returned nil error, want error")
			}
		})
	}
}
