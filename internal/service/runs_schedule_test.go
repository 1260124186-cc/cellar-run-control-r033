package service

import (
	"os"
	"testing"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

type scheduleFixture struct {
	service *Service
	clock   *clock.Fixed
	formula domain.Formula
	vessel  domain.Vessel
	dir     string
}

func newScheduleFixture(t *testing.T) *scheduleFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "cellar-schedule-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	store, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	fixedClock := clock.NewFixed(base)
	svc := New(store, fixedClock)

	formula, err := svc.CreateFormula(CreateFormulaInput{
		Name:                  "Schedule Test Ale",
		Style:                 "Ale",
		TargetOriginalGravity: 1.050,
		TargetFinalGravity:    1.010,
		MaxFermentationTempC:  20,
		MinimumDays:           7,
		MaximumDays:           21,
	})
	if err != nil {
		t.Fatalf("create formula: %v", err)
	}
	steps := []domain.FormulaStep{
		{Sequence: 1, Label: "ferment", TemperatureC: 18, HoldMinutes: 60},
	}
	if _, err := svc.SetFormulaSteps(formula.ID, SetFormulaStepsInput{Steps: steps}); err != nil {
		t.Fatalf("set steps: %v", err)
	}
	if _, err := svc.ApproveFormula(formula.ID, VersionInput{}); err != nil {
		t.Fatalf("approve formula: %v", err)
	}

	vessel, err := svc.CreateVessel(CreateVesselInput{
		Code:      "VSL-SCHED-01",
		CapacityL: 500,
	})
	if err != nil {
		t.Fatalf("create vessel: %v", err)
	}

	return &scheduleFixture{
		service: svc,
		clock:   fixedClock,
		formula: formula,
		vessel:  vessel,
		dir:     dir,
	}
}

func (f *scheduleFixture) createScheduledRun(
	t *testing.T,
	code string,
	start, windowEnd, end time.Time,
) domain.FermentationRun {
	t.Helper()
	run, err := f.service.CreateRun(CreateRunInput{
		Code:             code,
		FormulaID:        f.formula.ID,
		PlannedVolumeL:   200,
		PlannedStartAt:   &start,
		StartWindowEndAt: &windowEnd,
		PlannedEndAt:     &end,
	})
	if err != nil {
		t.Fatalf("create run %s: %v", code, err)
	}
	return run
}

func (f *scheduleFixture) createUnscheduledRun(t *testing.T, code string) domain.FermentationRun {
	t.Helper()
	run, err := f.service.CreateRun(CreateRunInput{
		Code:           code,
		FormulaID:      f.formula.ID,
		PlannedVolumeL: 200,
	})
	if err != nil {
		t.Fatalf("create run %s: %v", code, err)
	}
	return run
}

func TestCreateRunWithSchedule(t *testing.T) {
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("valid schedule is stored", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		windowEnd := start.Add(2 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := f.createScheduledRun(t, "RUN-SCHED-0001", start, windowEnd, end)

		if run.State != domain.RunPlanned {
			t.Fatalf("state = %s, want planned", run.State)
		}
		if run.PlannedStartAt == nil || !run.PlannedStartAt.Equal(start) {
			t.Fatalf("planned_start_at = %v, want %v", run.PlannedStartAt, start)
		}
		if run.StartWindowEndAt == nil || !run.StartWindowEndAt.Equal(windowEnd) {
			t.Fatalf("start_window_end_at = %v, want %v", run.StartWindowEndAt, windowEnd)
		}
		if run.PlannedEndAt == nil || !run.PlannedEndAt.Equal(end) {
			t.Fatalf("planned_end_at = %v, want %v", run.PlannedEndAt, end)
		}
	})

	t.Run("past start is rejected without creating a run", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(-1 * time.Hour)
		end := start.Add(48 * time.Hour)
		_, err := f.service.CreateRun(CreateRunInput{
			Code:           "RUN-SCHED-0002",
			FormulaID:      f.formula.ID,
			PlannedVolumeL: 200,
			PlannedStartAt: &start,
			PlannedEndAt:   &end,
		})
		if !domain.IsCode(err, domain.CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
		runs, _ := f.service.ListRuns(ListRunsInput{})
		for _, r := range runs {
			if r.Code == "RUN-SCHED-0002" {
				t.Fatal("run with past start was persisted")
			}
		}
	})

	t.Run("window end before start is rejected", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		windowEnd := start.Add(-1 * time.Hour)
		end := start.Add(48 * time.Hour)
		_, err := f.service.CreateRun(CreateRunInput{
			Code:             "RUN-SCHED-0003",
			FormulaID:        f.formula.ID,
			PlannedVolumeL:   200,
			PlannedStartAt:   &start,
			StartWindowEndAt: &windowEnd,
			PlannedEndAt:     &end,
		})
		if !domain.IsCode(err, domain.CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})

	t.Run("end not after window is rejected", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		windowEnd := start.Add(2 * time.Hour)
		end := windowEnd
		_, err := f.service.CreateRun(CreateRunInput{
			Code:             "RUN-SCHED-0004",
			FormulaID:        f.formula.ID,
			PlannedVolumeL:   200,
			PlannedStartAt:   &start,
			StartWindowEndAt: &windowEnd,
			PlannedEndAt:     &end,
		})
		if !domain.IsCode(err, domain.CodeInvalidInput) {
			t.Fatalf("expected invalid_input, got %v", err)
		}
	})
}

func TestReserveScheduledRun(t *testing.T) {
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("reserve on available vessel succeeds", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := f.createScheduledRun(t, "RUN-RES-0001", start, start, end)

		reserved, vessel, err := f.service.ReserveRun(run.ID, ReserveRunInput{
			VesselID: f.vessel.ID,
		})
		if err != nil {
			t.Fatalf("reserve: %v", err)
		}
		if reserved.State != domain.RunReserved {
			t.Fatalf("run state = %s, want reserved", reserved.State)
		}
		if vessel.State != domain.VesselReserved {
			t.Fatalf("vessel state = %s, want reserved", vessel.State)
		}
		if vessel.ActiveRunID == nil || *vessel.ActiveRunID != run.ID {
			t.Fatalf("vessel active_run_id = %v, want %s", vessel.ActiveRunID, run.ID)
		}
	})

	t.Run("overlapping reservation is rejected without state change", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		runA := f.createScheduledRun(t, "RUN-RES-0002", start, start, end)
		runB := f.createScheduledRun(t, "RUN-RES-0003",
			start.Add(12*time.Hour), start.Add(12*time.Hour), end.Add(12*time.Hour))

		if _, _, err := f.service.ReserveRun(runA.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve A: %v", err)
		}
		vesselBefore, _ := f.service.GetVessel(f.vessel.ID)

		_, _, err := f.service.ReserveRun(runB.ID, ReserveRunInput{VesselID: f.vessel.ID})
		if !domain.IsCode(err, domain.CodeScheduleConflict) {
			t.Fatalf("expected schedule_conflict, got %v", err)
		}

		vesselAfter, _ := f.service.GetVessel(f.vessel.ID)
		if vesselAfter.State != vesselBefore.State {
			t.Fatalf("vessel state changed from %s to %s", vesselBefore.State, vesselAfter.State)
		}
		if vesselAfter.ActiveRunID == nil || *vesselAfter.ActiveRunID != runA.ID {
			t.Fatalf("vessel active run changed unexpectedly")
		}
		runBAfter, _ := f.service.GetRun(runB.ID)
		if runBAfter.State != domain.RunPlanned {
			t.Fatalf("run B state = %s, want planned (unchanged)", runBAfter.State)
		}
	})

	t.Run("non-overlapping reservation is allowed (multi booking)", func(t *testing.T) {
		f := newScheduleFixture(t)
		startA := base.Add(24 * time.Hour)
		endA := startA.Add(48 * time.Hour)
		runA := f.createScheduledRun(t, "RUN-RES-0004", startA, startA, endA)
		if _, _, err := f.service.ReserveRun(runA.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve A: %v", err)
		}

		startB := endA.Add(24 * time.Hour)
		endB := startB.Add(48 * time.Hour)
		runB := f.createScheduledRun(t, "RUN-RES-0005", startB, startB, endB)
		if _, _, err := f.service.ReserveRun(runB.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve B (non-overlapping): %v", err)
		}

		vessel, _ := f.service.GetVessel(f.vessel.ID)
		if vessel.State != domain.VesselReserved {
			t.Fatalf("vessel state = %s, want reserved", vessel.State)
		}
		if vessel.ActiveRunID == nil || *vessel.ActiveRunID != runA.ID {
			t.Fatalf("active run = %v, want earliest run A", vessel.ActiveRunID)
		}
	})

	t.Run("adjacent windows do not overlap", func(t *testing.T) {
		f := newScheduleFixture(t)
		startA := base.Add(24 * time.Hour)
		endA := startA.Add(48 * time.Hour)
		runA := f.createScheduledRun(t, "RUN-RES-0006", startA, startA, endA)
		if _, _, err := f.service.ReserveRun(runA.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve A: %v", err)
		}

		startB := endA // exactly adjacent
		endB := startB.Add(48 * time.Hour)
		runB := f.createScheduledRun(t, "RUN-RES-0007", startB, startB, endB)
		if _, _, err := f.service.ReserveRun(runB.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve B (adjacent): %v", err)
		}
	})

	t.Run("open ended booking blocks scheduled reservation", func(t *testing.T) {
		f := newScheduleFixture(t)
		unscheduled := f.createUnscheduledRun(t, "RUN-RES-0008")
		if _, _, err := f.service.ReserveRun(unscheduled.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
			t.Fatalf("reserve unscheduled: %v", err)
		}

		start := base.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		scheduled := f.createScheduledRun(t, "RUN-RES-0009", start, start, end)
		_, _, err := f.service.ReserveRun(scheduled.ID, ReserveRunInput{VesselID: f.vessel.ID})
		if !domain.IsCode(err, domain.CodeScheduleConflict) {
			t.Fatalf("expected schedule_conflict, got %v", err)
		}
	})

	t.Run("stale version is rejected without state change", func(t *testing.T) {
		f := newScheduleFixture(t)
		start := base.Add(24 * time.Hour)
		end := start.Add(48 * time.Hour)
		run := f.createScheduledRun(t, "RUN-RES-0010", start, start, end)

		stale := int64(999)
		_, _, err := f.service.ReserveRun(run.ID, ReserveRunInput{
			ExpectedVersion: &stale,
			VesselID:        f.vessel.ID,
		})
		if !domain.IsCode(err, domain.CodeRevisionConflict) {
			t.Fatalf("expected revision_conflict, got %v", err)
		}

		runAfter, _ := f.service.GetRun(run.ID)
		if runAfter.State != domain.RunPlanned {
			t.Fatalf("run state = %s, want planned", runAfter.State)
		}
		vesselAfter, _ := f.service.GetVessel(f.vessel.ID)
		if vesselAfter.State != domain.VesselAvailable {
			t.Fatalf("vessel state = %s, want available", vesselAfter.State)
		}
	})
}

func TestStartRunKeepsPlanAndUsesActualTime(t *testing.T) {
	f := newScheduleFixture(t)
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	start := base.Add(24 * time.Hour)
	end := start.Add(48 * time.Hour)
	run := f.createScheduledRun(t, "RUN-START-0001", start, start, end)
	if _, _, err := f.service.ReserveRun(run.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Start two hours after the planned start.
	f.clock.Advance(26 * time.Hour)
	started, vessel, err := f.service.StartRun(run.ID, VersionInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if started.State != domain.RunFermenting {
		t.Fatalf("state = %s, want fermenting", started.State)
	}
	if started.StartedAt == nil {
		t.Fatal("started_at is nil")
	}
	wantStarted := base.Add(26 * time.Hour)
	if !started.StartedAt.Equal(wantStarted) {
		t.Fatalf("started_at = %v, want %v", started.StartedAt, wantStarted)
	}
	// Original plan is retained.
	if started.PlannedStartAt == nil || !started.PlannedStartAt.Equal(start) {
		t.Fatalf("planned_start_at = %v, want retained %v", started.PlannedStartAt, start)
	}
	if started.PlannedEndAt == nil || !started.PlannedEndAt.Equal(end) {
		t.Fatalf("planned_end_at = %v, want retained %v", started.PlannedEndAt, end)
	}
	if vessel.State != domain.VesselInUse {
		t.Fatalf("vessel state = %s, want in_use", vessel.State)
	}

	read, err := f.service.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if read.Variance == nil || read.Variance.StartDelaySeconds == nil {
		t.Fatal("expected start delay variance")
	}
	if *read.Variance.StartDelaySeconds != 7200 {
		t.Fatalf("start delay = %d, want 7200", *read.Variance.StartDelaySeconds)
	}
}

func TestListRunsByTimeRange(t *testing.T) {
	f := newScheduleFixture(t)
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	// Run in the near window.
	nearStart := base.Add(24 * time.Hour)
	nearEnd := nearStart.Add(48 * time.Hour)
	near := f.createScheduledRun(t, "RUN-LIST-0001", nearStart, nearStart, nearEnd)

	// Run in the far window.
	farStart := base.Add(30 * 24 * time.Hour)
	farEnd := farStart.Add(48 * time.Hour)
	far := f.createScheduledRun(t, "RUN-LIST-0002", farStart, farStart, farEnd)

	// Unscheduled run.
	unscheduled := f.createUnscheduledRun(t, "RUN-LIST-0003")

	t.Run("no filter returns all runs", func(t *testing.T) {
		runs, err := f.service.ListRuns(ListRunsInput{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 3 {
			t.Fatalf("got %d runs, want 3", len(runs))
		}
	})

	t.Run("filter excludes unscheduled runs", func(t *testing.T) {
		from := base
		to := base.Add(7 * 24 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{From: &from, To: &to})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1", len(runs))
		}
		if runs[0].ID != near.ID {
			t.Fatalf("got run %s, want near run %s", runs[0].ID, near.ID)
		}
	})

	t.Run("filter across both windows returns both", func(t *testing.T) {
		from := base
		to := base.Add(60 * 24 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{From: &from, To: &to})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 2 {
			t.Fatalf("got %d runs, want 2", len(runs))
		}
	})

	t.Run("filter before all windows returns empty", func(t *testing.T) {
		from := base.Add(-10 * 24 * time.Hour)
		to := base.Add(-9 * 24 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{From: &from, To: &to})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 0 {
			t.Fatalf("got %d runs, want 0", len(runs))
		}
	})

	t.Run("filter after all windows returns empty", func(t *testing.T) {
		from := base.Add(100 * 24 * time.Hour)
		to := base.Add(110 * 24 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{From: &from, To: &to})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 0 {
			t.Fatalf("got %d runs, want 0", len(runs))
		}
	})

	t.Run("from only filter", func(t *testing.T) {
		from := farStart.Add(-1 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{From: &from})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 1 || runs[0].ID != far.ID {
			t.Fatalf("got %d runs, want far run only", len(runs))
		}
	})

	t.Run("to only filter", func(t *testing.T) {
		to := nearEnd.Add(1 * time.Hour)
		runs, err := f.service.ListRuns(ListRunsInput{To: &to})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 1 || runs[0].ID != near.ID {
			t.Fatalf("got %d runs, want near run only", len(runs))
		}
	})

	t.Run("ordering is stable: scheduled by start then unscheduled", func(t *testing.T) {
		runs, err := f.service.ListRuns(ListRunsInput{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(runs) != 3 {
			t.Fatalf("got %d runs, want 3", len(runs))
		}
		if runs[0].ID != near.ID {
			t.Fatalf("first = %s, want near", runs[0].ID)
		}
		if runs[1].ID != far.ID {
			t.Fatalf("second = %s, want far", runs[1].ID)
		}
		if runs[2].ID != unscheduled.ID {
			t.Fatalf("third = %s, want unscheduled", runs[2].ID)
		}
	})
}

func TestAbortReleasesBooking(t *testing.T) {
	f := newScheduleFixture(t)
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	start := base.Add(24 * time.Hour)
	windowEnd := start.Add(2 * time.Hour)
	end := start.Add(48 * time.Hour)
	run := f.createScheduledRun(t, "RUN-ABORT-0001", start, windowEnd, end)
	if _, _, err := f.service.ReserveRun(run.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if _, _, err := f.service.AbortRun(run.ID, VersionInput{}); err != nil {
		t.Fatalf("abort: %v", err)
	}

	vessel, _ := f.service.GetVessel(f.vessel.ID)
	if vessel.State != domain.VesselAvailable {
		t.Fatalf("vessel state = %s, want available", vessel.State)
	}
	if vessel.ActiveRunID != nil {
		t.Fatalf("vessel active_run_id = %v, want nil", vessel.ActiveRunID)
	}

	// A new run can now book the same window.
	other := f.createScheduledRun(t, "RUN-ABORT-0002", start, windowEnd, end)
	if _, _, err := f.service.ReserveRun(other.ID, ReserveRunInput{VesselID: f.vessel.ID}); err != nil {
		t.Fatalf("reserve after abort: %v", err)
	}
}
