package observability

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLookback = 7 * 24 * time.Hour
	defaultLimit    = int32(50)
	maxLimit        = int32(200)
)

var (
	ErrInvalidFilter   = errors.New("invalid gateway observability filter")
	ErrInvalidID       = errors.New("invalid gateway observability id")
	ErrSessionNotFound = errors.New("gateway session not found")
)

type Filter struct {
	Since       time.Time
	Until       time.Time
	Limit       int32
	Status      string
	Backend     string
	Model       string
	BucketWidth string
}

func ParseFilter(values url.Values, now time.Time) (Filter, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	since, err := parseSince(values.Get("since"), now)
	if err != nil {
		return Filter{}, err
	}
	if since.After(now) {
		return Filter{}, fmt.Errorf("%w: since cannot be in the future", ErrInvalidFilter)
	}

	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return Filter{}, err
	}

	return Filter{
		Since:       since.UTC(),
		Until:       now,
		Limit:       limit,
		Status:      strings.TrimSpace(values.Get("status")),
		Backend:     strings.TrimSpace(values.Get("backend")),
		Model:       strings.TrimSpace(values.Get("model")),
		BucketWidth: bucketWidth(since, now),
	}, nil
}

func parseSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now.Add(-defaultLookback), nil
	}

	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}

	duration, err := parseRelativeDuration(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid since", ErrInvalidFilter)
	}
	if duration <= 0 {
		return time.Time{}, fmt.Errorf("%w: since duration must be positive", ErrInvalidFilter)
	}
	return now.Add(-duration), nil
}

func parseRelativeDuration(raw string) (time.Duration, error) {
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}

func parseLimit(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLimit, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid limit", ErrInvalidFilter)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%w: limit must be positive", ErrInvalidFilter)
	}
	if parsed > int(maxLimit) {
		return maxLimit, nil
	}
	return int32(parsed), nil
}

func bucketWidth(since, now time.Time) string {
	if now.Sub(since) > 72*time.Hour {
		return "day"
	}
	return "hour"
}
