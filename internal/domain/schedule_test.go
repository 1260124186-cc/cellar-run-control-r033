package domain

import (
	"testing"
	"time"
)

func TestEffectiveWindowUsesActualTimestampsAndRetainsPlan(t *testing.T) {
	plannedStart := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	plannedEnd := plannedStart.Add(24 * time.Hour)
	actualStart := plannedStart.Add(25 * time.Hour)
	run := FermentationRun{
		PlannedStartAt: &plannedStart,
		PlannedEndAt:   &plannedEnd,
		StartedAt:      &actualStart,
	}
	start, end := run.EffectiveWindow()
	if !start.Equal(actualStart) || end != nil {
		t.Fatalf("effective window = %v..%v, want actual start with unknown end", start, end)
	}
	if !run.PlannedStartAt.Equal(plannedStart) || !run.PlannedEndAt.Equal(plannedEnd) {
		t.Fatalf("planned window mutated: %+v", run)
	}
}

func TestEffectiveWindowUsesActualEndWhenRunCompletes(t *testing.T) {
	plannedStart := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	plannedEnd := plannedStart.Add(24 * time.Hour)
	actualStart := plannedStart.Add(2 * time.Hour)
	actualEnd := plannedEnd.Add(3 * time.Hour)
	run := FermentationRun{
		PlannedStartAt: &plannedStart,
		PlannedEndAt:   &plannedEnd,
		StartedAt:      &actualStart,
		EndedAt:        &actualEnd,
	}
	start, end := run.EffectiveWindow()
	if !start.Equal(actualStart) || end == nil || !end.Equal(actualEnd) {
		t.Fatalf("effective window = %v..%v, want actual timestamps", start, end)
	}
}

func TestWindowsOverlapHalfOpen(t *testing.T) {
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	middle := start.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	if WindowsOverlap(&start, &middle, &middle, &end) {
		t.Fatal("adjacent half-open windows must not overlap")
	}
	overlapEnd := end.Add(time.Hour)
	if !WindowsOverlap(&start, &end, &middle, &overlapEnd) {
		t.Fatal("overlapping windows must be detected")
	}
}

func TestVesselWindowFitsPointer(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := from.Add(24 * time.Hour)
	vessel := Vessel{AvailableFrom: &from, AvailableUntil: &until}
	start := from.Add(time.Hour)
	end := until.Add(time.Hour)
	if vessel.WindowFitsPointer(&start, &end) {
		t.Fatal("window outside availability must not fit")
	}
	end = until
	if !vessel.WindowFitsPointer(&start, &end) {
		t.Fatal("window touching both availability bounds must fit")
	}
	if vessel.WindowFitsPointer(&start, nil) {
		t.Fatal("bounded vessel requires a complete window")
	}
}
