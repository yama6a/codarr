package ingest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrBadCron is returned for anything ParseCron cannot read.
var ErrBadCron = errors.New("ingest: invalid cron expression")

// DefaultCron is the settings default of plan.md 17.1: 04:00 daily.
const DefaultCron = "0 4 * * *"

// searchYears caps the forward search. A schedule that matches nothing inside
// four years (29 February on a weekday that never lines up) returns no time
// rather than looping.
const searchYears = 4

// Schedule is a parsed five-field cron expression, in the usual order: minute,
// hour, day of month, month and finally day of week. Numeric only, plus *,
// ranges, lists and steps; no names and no @-shorthands, because
// settings.scan_cron is a single daily time and a bigger dialect buys nothing.
type Schedule struct {
	minute uint64
	hour   uint64
	dom    uint64
	month  uint64
	dow    uint64

	// Standard cron: when both day fields are restricted the match is a union,
	// not an intersection.
	domRestricted bool
	dowRestricted bool
}

// ParseCron reads a five-field cron expression.
func ParseCron(spec string) (Schedule, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("%w: want 5 fields, got %d in %q", ErrBadCron, len(fields), spec)
	}

	var (
		s   Schedule
		err error
	)

	if s.minute, _, err = parseField(fields[0], 0, 59); err != nil {
		return Schedule{}, fmt.Errorf("%w: minute: %w", ErrBadCron, err)
	}

	if s.hour, _, err = parseField(fields[1], 0, 23); err != nil {
		return Schedule{}, fmt.Errorf("%w: hour: %w", ErrBadCron, err)
	}

	if s.dom, s.domRestricted, err = parseField(fields[2], 1, 31); err != nil {
		return Schedule{}, fmt.Errorf("%w: day of month: %w", ErrBadCron, err)
	}

	if s.month, _, err = parseField(fields[3], 1, 12); err != nil {
		return Schedule{}, fmt.Errorf("%w: month: %w", ErrBadCron, err)
	}

	if s.dow, s.dowRestricted, err = parseField(fields[4], 0, 7); err != nil {
		return Schedule{}, fmt.Errorf("%w: day of week: %w", ErrBadCron, err)
	}

	// 7 and 0 are both Sunday.
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 << 0
	}

	return s, nil
}

// Next is the first matching minute strictly after t. The zero time means the
// expression matches nothing in the next few years.
func (s Schedule) Next(t time.Time) time.Time {
	cur := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location()).
		Add(time.Minute)
	limit := t.AddDate(searchYears, 0, 0)

	for cur.Before(limit) {
		switch {
		case !s.matchesDay(cur):
			cur = startOfDay(cur).AddDate(0, 0, 1)
		case !isSet(s.hour, cur.Hour()):
			cur = startOfHour(cur).Add(time.Hour)
		case !isSet(s.minute, cur.Minute()):
			cur = cur.Add(time.Minute)
		default:
			return cur
		}
	}

	return time.Time{}
}

func (s Schedule) matchesDay(t time.Time) bool {
	if !isSet(s.month, int(t.Month())) {
		return false
	}

	dom, dow := isSet(s.dom, t.Day()), isSet(s.dow, int(t.Weekday()))

	if s.domRestricted && s.dowRestricted {
		return dom || dow
	}

	return dom && dow
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func startOfHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func isSet(mask uint64, v int) bool { return mask&(1<<uint(v)) != 0 }

// parseField returns the bitmask for one field and whether it was restricted,
// which is to say anything other than a bare "*".
func parseField(field string, minVal, maxVal int) (uint64, bool, error) {
	var (
		mask       uint64
		restricted bool
	)

	for _, part := range strings.Split(field, ",") {
		m, r, err := parsePart(part, minVal, maxVal)
		if err != nil {
			return 0, false, err
		}

		mask |= m
		restricted = restricted || r
	}

	if mask == 0 {
		return 0, false, fmt.Errorf("%q matches nothing", field)
	}

	return mask, restricted, nil
}

func parsePart(part string, minVal, maxVal int) (uint64, bool, error) {
	rangeSpec, stepSpec, hasStep := strings.Cut(part, "/")

	step := 1

	if hasStep {
		var err error
		if step, err = strconv.Atoi(stepSpec); err != nil || step < 1 {
			return 0, false, fmt.Errorf("bad step in %q", part)
		}
	}

	lo, hi, restricted, err := parseRange(rangeSpec, minVal, maxVal)
	if err != nil {
		return 0, false, err
	}

	// A bare value with a step means "from here to the end", the same reading
	// crontab uses for "5/10".
	if hasStep && lo == hi && rangeSpec != "*" {
		hi = maxVal
	}

	var mask uint64
	for v := lo; v <= hi; v += step {
		mask |= 1 << uint(v)
	}

	return mask, restricted || hasStep, nil
}

func parseRange(spec string, minVal, maxVal int) (int, int, bool, error) {
	if spec == "*" {
		return minVal, maxVal, false, nil
	}

	loSpec, hiSpec, isRange := strings.Cut(spec, "-")

	lo, err := parseValue(loSpec, minVal, maxVal)
	if err != nil {
		return 0, 0, false, err
	}

	if !isRange {
		return lo, lo, true, nil
	}

	hi, err := parseValue(hiSpec, minVal, maxVal)
	if err != nil {
		return 0, 0, false, err
	}

	if hi < lo {
		return 0, 0, false, fmt.Errorf("range %q is backwards", spec)
	}

	return lo, hi, true, nil
}

func parseValue(spec string, minVal, maxVal int) (int, error) {
	v, err := strconv.Atoi(spec)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", spec)
	}

	if v < minVal || v > maxVal {
		return 0, fmt.Errorf("%d is outside %d-%d", v, minVal, maxVal)
	}

	return v, nil
}
