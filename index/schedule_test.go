package index

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbosCronParser is the parser DBOS validates a schedule with
// (dbos/internal/models.NewScheduleCronParser). It is restated here rather than
// reached for, because it is unexported there and because restating it is what
// makes the assertion below about *DBOS's* reading of the constant rather than
// about cron in general.
func dbosCronParser() cron.Parser {
	return cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
}

// TestRebuildScheduleIsNightly is the test the six-field format needs.
//
// DBOS parses schedules with second precision, and the failure mode of getting
// that wrong is not an error — it is a schedule that parses and means something
// else. A five-field `0 3 * * *` is read by this parser as second 0 of minute 3
// of every hour: 24 whole-corpus rebuilds a day, each of them taking the
// exclusive link lock and holding off every index while it runs. Nothing would
// report that; the graph would stay correct and the machine would simply be busy.
//
// So the constant is parsed and its next two ticks asserted, which pins both the
// field count and the hour.
func TestRebuildScheduleIsNightly(t *testing.T) {
	schedule, err := dbosCronParser().Parse(RebuildSchedule)
	require.NoError(t, err, "DBOS would panic at registration on a schedule it cannot parse")

	// From midday, the next run is 03:00 the following day, and the one after
	// that is 03:00 the day after — a day apart, not an hour.
	noon := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	first := schedule.Next(noon)
	assert.Equal(t, time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC), first)
	assert.Equal(t, 24*time.Hour, schedule.Next(first).Sub(first),
		"the backstop runs nightly, not hourly")
}

// TestScheduledRebuildRefusesBeforeRegister covers the one way the workflow can
// be reached without a database handle: a caller that registered it by hand
// instead of through Register, which is the whole reason Register does both
// halves at once. The alternative to this error is a nil-pointer panic inside a
// scheduled tick, which is the worst place to discover it.
func TestScheduledRebuildRefusesBeforeRegister(t *testing.T) {
	saved := registered.Load()
	registered.Store(nil)
	t.Cleanup(func() { registered.Store(saved) })

	_, err := ScheduledRebuild(nil, time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before index.Register")
}

// TestRebuildIsCheckpointable is the same round trip every other checkpointed
// payload in this package gets (dbos_test.go): a workflow's output is
// serialized into the system database and decoded again on replay, so a field
// that does not survive the trip is a run reporting something it did not do.
func TestRebuildIsCheckpointable(t *testing.T) {
	want := Rebuild{
		ScheduledTime: time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC),
		Took:          1500 * time.Millisecond,
	}
	assert.Equal(t, want, checkpoint(t, want))
}
