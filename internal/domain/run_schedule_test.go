package domain

import (
	"testing"
	"time"
)

func scheduleRun(start, windowEnd, end time.Time) FermentationRun {
	return FermentationRun{
		Code:             "RUN-TEST-0001",
		FormulaID:        "formula_00000000000000000000",
		PlannedVolumeL:   100,
		State:            RunPlanned,
		PlannedStartAt:   &start,
		StartWindowEndAt: &windowEnd,
		PlannedEndAt:     &end,
	}
}

func TestValidateSchedule(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("no schedule is valid", func(t *testing.T) {
		run := FermentationRun{Code: "RUN-TEST-0001", State: RunPlanned}
		if err := run.ValidateSchedule(now); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("start without end is invalid", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		run := FermentationRun{
			Code:           "RUN-TEST-0001",
			State:          RunPlanned,
			PlannedStartAt: &start,
		}
		err := run.ValidateSchedule(now)
		if !IsCode(err, CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("end without start is invalid", func(t *testing.T) {
		end := now.Add(48 * time.Hour)
		run := FermentationRun{
			Code:         "RUN-TEST-0001",
			State:        RunPlanned,
			PlannedEndAt: &end,
		}
		err := run.ValidateSchedule(now)
		if !IsCode(err, CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("window end before start is invalid", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		windowEnd := start.Add(-1 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := scheduleRun(start, windowEnd, end)
		err := run.ValidateSchedule(now)
		if !IsCode(err, CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("end not after window is invalid", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		windowEnd := start.Add(2 * time.Hour)
		end := windowEnd // end == windowEnd, must be strictly after
		run := scheduleRun(start, windowEnd, end)
		err := run.ValidateSchedule(now)
		if !IsCode(err, CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("start in the past is invalid", func(t *testing.T) {
		start := now.Add(-1 * time.Hour)
		windowEnd := start.Add(2 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := scheduleRun(start, windowEnd, end)
		err := run.ValidateSchedule(now)
		if !IsCode(err, CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("valid schedule passes", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		windowEnd := start.Add(2 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := scheduleRun(start, windowEnd, end)
		if err := run.ValidateSchedule(now); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("window defaults to start when omitted", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := FermentationRun{
			Code:           "RUN-TEST-0001",
			State:          RunPlanned,
			PlannedStartAt: &start,
			PlannedEndAt:   &end,
		}
		if err := run.ValidateSchedule(now); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
}

func TestOccupancyInterval(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("no schedule yields no interval", func(t *testing.T) {
		run := FermentationRun{State: RunPlanned}
		_, _, ok := run.OccupancyInterval()
		if ok {
			t.Fatal("expected no interval")
		}
	})

	t.Run("planned run uses planned window", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := scheduleRun(start, start, end)
		gotStart, gotEnd, ok := run.OccupancyInterval()
		if !ok {
			t.Fatal("expected interval")
		}
		if !gotStart.Equal(start) {
			t.Fatalf("start = %v, want %v", gotStart, start)
		}
		if gotEnd == nil || !gotEnd.Equal(end) {
			t.Fatalf("end = %v, want %v", gotEnd, end)
		}
	})

	t.Run("started run uses actual start", func(t *testing.T) {
		plannedStart := now.Add(24 * time.Hour)
		plannedEnd := plannedStart.Add(48 * time.Hour)
		actualStart := now.Add(26 * time.Hour)
		run := scheduleRun(plannedStart, plannedStart, plannedEnd)
		run.StartedAt = &actualStart
		gotStart, _, ok := run.OccupancyInterval()
		if !ok {
			t.Fatal("expected interval")
		}
		if !gotStart.Equal(actualStart) {
			t.Fatalf("start = %v, want actual %v", gotStart, actualStart)
		}
	})

	t.Run("started run without planned end is open ended", func(t *testing.T) {
		plannedStart := now.Add(24 * time.Hour)
		actualStart := now.Add(26 * time.Hour)
		run := FermentationRun{
			Code:           "RUN-TEST-0001",
			State:          RunFermenting,
			PlannedStartAt: &plannedStart,
			StartedAt:      &actualStart,
		}
		_, gotEnd, ok := run.OccupancyInterval()
		if !ok {
			t.Fatal("expected interval")
		}
		if gotEnd != nil {
			t.Fatalf("end = %v, want nil", gotEnd)
		}
	})
}

func TestOverlapsInterval(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	start := now.Add(24 * time.Hour)
	end := start.Add(48 * time.Hour)
	run := scheduleRun(start, start, end)

	cases := []struct {
		name        string
		otherStart  time.Time
		otherEnd    *time.Time
		wantOverlap bool
	}{
		{
			name:        "overlapping window",
			otherStart:  start.Add(1 * time.Hour),
			otherEnd:    ptrTime(end.Add(-1 * time.Hour)),
			wantOverlap: true,
		},
		{
			name:        "window inside run",
			otherStart:  start.Add(1 * time.Hour),
			otherEnd:    ptrTime(start.Add(2 * time.Hour)),
			wantOverlap: true,
		},
		{
			name:        "adjacent window (half-open, no overlap)",
			otherStart:  end,
			otherEnd:    ptrTime(end.Add(24 * time.Hour)),
			wantOverlap: false,
		},
		{
			name:        "disjoint window after",
			otherStart:  end.Add(1 * time.Hour),
			otherEnd:    ptrTime(end.Add(24 * time.Hour)),
			wantOverlap: false,
		},
		{
			name:        "disjoint window before",
			otherStart:  start.Add(-48 * time.Hour),
			otherEnd:    ptrTime(start.Add(-24 * time.Hour)),
			wantOverlap: false,
		},
		{
			name:        "nil end means open ended and overlaps",
			otherStart:  start.Add(1 * time.Hour),
			otherEnd:    nil,
			wantOverlap: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run.OverlapsInterval(&tc.otherStart, tc.otherEnd)
			if got != tc.wantOverlap {
				t.Fatalf("overlap = %v, want %v", got, tc.wantOverlap)
			}
		})
	}
}

func TestScheduleVariance(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("nil when no plan", func(t *testing.T) {
		run := FermentationRun{State: RunFermenting}
		if v := run.ScheduleVariance(); v != nil {
			t.Fatalf("expected nil, got %v", v)
		}
	})

	t.Run("nil when not started", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := scheduleRun(start, start, end)
		if v := run.ScheduleVariance(); v != nil {
			t.Fatalf("expected nil, got %v", v)
		}
	})

	t.Run("positive delay when started late", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		actualStart := start.Add(2 * time.Hour)
		run := scheduleRun(start, start, end)
		run.StartedAt = &actualStart
		variance := run.ScheduleVariance()
		if variance == nil {
			t.Fatal("expected variance")
		}
		if variance.StartDelaySeconds == nil || *variance.StartDelaySeconds != 7200 {
			t.Fatalf("start delay = %v, want 7200", variance.StartDelaySeconds)
		}
	})

	t.Run("negative delay when started early", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		actualStart := start.Add(-1 * time.Hour)
		run := scheduleRun(start, start, end)
		run.StartedAt = &actualStart
		variance := run.ScheduleVariance()
		if variance == nil || variance.StartDelaySeconds == nil {
			t.Fatal("expected start delay")
		}
		if *variance.StartDelaySeconds != -3600 {
			t.Fatalf("start delay = %d, want -3600", *variance.StartDelaySeconds)
		}
	})

	t.Run("end variance when ended", func(t *testing.T) {
		start := now.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		actualStart := start.Add(1 * time.Hour)
		actualEnd := end.Add(3 * time.Hour)
		run := scheduleRun(start, start, end)
		run.StartedAt = &actualStart
		run.EndedAt = &actualEnd
		variance := run.ScheduleVariance()
		if variance == nil || variance.EndVarianceSeconds == nil {
			t.Fatal("expected end variance")
		}
		if *variance.EndVarianceSeconds != 10800 {
			t.Fatalf("end variance = %d, want 10800", *variance.EndVarianceSeconds)
		}
	})
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
