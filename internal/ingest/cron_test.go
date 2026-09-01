package ingest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ingest"
)

func mustCron(t *testing.T, spec string) ingest.Schedule {
	t.Helper()

	s, err := ingest.ParseCron(spec)
	require.NoError(t, err)

	return s
}

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestParseCron_TheDefaultIsFourInTheMorning(t *testing.T) {
	t.Parallel()

	s := mustCron(t, ingest.DefaultCron)

	require.Equal(t, at(2026, time.September, 1, 4, 0),
		s.Next(at(2026, time.September, 1, 3, 59)))
	require.Equal(t, at(2026, time.September, 2, 4, 0),
		s.Next(at(2026, time.September, 1, 4, 0)),
		"Next is strictly after, so a scan starting at 04:00 does not fire twice")
	require.Equal(t, at(2026, time.September, 2, 4, 0),
		s.Next(at(2026, time.September, 1, 12, 0)))
}

func TestSchedule_NextHandlesEveryFieldForm(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		spec string
		from time.Time
		want time.Time
	}{
		{"every minute", "* * * * *", at(2026, time.September, 1, 4, 0), at(2026, time.September, 1, 4, 1)},
		{"step minutes", "*/15 * * * *", at(2026, time.September, 1, 4, 1), at(2026, time.September, 1, 4, 15)},
		{"list of hours", "0 2,4,6 * * *", at(2026, time.September, 1, 3, 0), at(2026, time.September, 1, 4, 0)},
		{"hour range", "30 9-17 * * *", at(2026, time.September, 1, 3, 0), at(2026, time.September, 1, 9, 30)},
		{"range with step", "0 0-23/6 * * *", at(2026, time.September, 1, 1, 0), at(2026, time.September, 1, 6, 0)},
		{"day of month", "0 4 15 * *", at(2026, time.September, 1, 4, 0), at(2026, time.September, 15, 4, 0)},
		{"month", "0 4 1 1 *", at(2026, time.September, 1, 4, 0), at(2027, time.January, 1, 4, 0)},
		{"day of week", "0 4 * * 1", at(2026, time.September, 1, 4, 0), at(2026, time.September, 7, 4, 0)},
		{"sunday as 0", "0 4 * * 0", at(2026, time.September, 1, 4, 0), at(2026, time.September, 6, 4, 0)},
		{"sunday as 7", "0 4 * * 7", at(2026, time.September, 1, 4, 0), at(2026, time.September, 6, 4, 0)},
		{"bare value with step", "0 4/6 * * *", at(2026, time.September, 1, 5, 0), at(2026, time.September, 1, 10, 0)},
		{"rolls over midnight", "0 4 * * *", at(2026, time.September, 1, 23, 59), at(2026, time.September, 2, 4, 0)},
		{"leap day", "0 4 29 2 *", at(2026, time.September, 1, 0, 0), at(2028, time.February, 29, 4, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, mustCron(t, tc.spec).Next(tc.from))
		})
	}
}

// Standard cron: two restricted day fields are a union, not an intersection.
// The 1st of September 2026 is a Tuesday, so "1st or Monday" matches it.
func TestSchedule_NextUnionsBothRestrictedDayFields(t *testing.T) {
	t.Parallel()

	s := mustCron(t, "0 4 1 * 1")

	require.Equal(t, at(2026, time.September, 1, 4, 0), s.Next(at(2026, time.August, 31, 5, 0)))
	require.Equal(t, at(2026, time.September, 7, 4, 0), s.Next(at(2026, time.September, 1, 4, 0)))
}

func TestSchedule_NextIsZeroWhenNothingMatches(t *testing.T) {
	t.Parallel()

	// 30 February.
	require.True(t, mustCron(t, "0 4 30 2 *").Next(at(2026, time.September, 1, 0, 0)).IsZero())
}

func TestParseCron_RejectsWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{
		"", "0 4 * *", "0 4 * * * *", "@daily",
		"60 4 * * *", "0 24 * * *", "0 4 0 * *", "0 4 32 * *",
		"0 4 * 0 *", "0 4 * 13 *", "0 4 * * 8",
		"0 MON * * *", "a 4 * * *", "0 4-2 * * *", "*/0 4 * * *", "*/x 4 * * *",
	} {
		_, err := ingest.ParseCron(spec)
		require.ErrorIs(t, err, ingest.ErrBadCron, spec)
	}
}

func TestParseCron_IgnoresSurroundingWhitespace(t *testing.T) {
	t.Parallel()

	s, err := ingest.ParseCron("  0   4  *  *  * ")
	require.NoError(t, err)
	require.Equal(t, at(2026, time.September, 2, 4, 0), s.Next(at(2026, time.September, 1, 12, 0)))
}
