package service

import (
	"testing"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

func expectedVersion(value int64) *int64 {
	return &value
}

func newTestService(t *testing.T) (*Service, *storage.Store, time.Time) {
	t.Helper()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return New(store, clock.NewFixed(now)), store, now
}

func insertApprovedFormula(t *testing.T, store *storage.Store) domain.Formula {
	t.Helper()
	var formula domain.Formula
	err := store.Execute(func(snapshot *domain.Snapshot) error {
		formula = domain.Formula{
			ID:                    "form_1",
			Name:                  "Test Formula",
			Style:                 "Ale",
			TargetOriginalGravity: 1.050,
			TargetFinalGravity:    1.010,
			MaxFermentationTempC:  20,
			MinimumDays:           1,
			MaximumDays:           10,
			Steps: []domain.FormulaStep{{
				Sequence:     1,
				Label:        "Ferment",
				TemperatureC: 20,
				HoldMinutes:  60,
			}},
			State:     domain.FormulaApproved,
			Version:   1,
			CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		}
		snapshot.Formulas[formula.ID] = formula
		snapshot.NameIndex[domain.NameKey(formula.Name)] = formula.ID
		return nil
	})
	if err != nil {
		t.Fatalf("insert formula: %v", err)
	}
	return formula
}

func insertTestVessel(t *testing.T, store *storage.Store, from, until *time.Time) domain.Vessel {
	t.Helper()
	var vessel domain.Vessel
	err := store.Execute(func(snapshot *domain.Snapshot) error {
		vessel = domain.Vessel{
			ID:             "vsl_1",
			Code:           "VSL-TEST",
			CapacityL:      500,
			State:          domain.VesselAvailable,
			AvailableFrom:  from,
			AvailableUntil: until,
			Version:        1,
			CreatedAt:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		}
		snapshot.Vessels[vessel.ID] = vessel
		snapshot.VesselIndex[vessel.Code] = vessel.ID
		return nil
	})
	if err != nil {
		t.Fatalf("insert vessel: %v", err)
	}
	return vessel
}

func insertTestRun(
	t *testing.T,
	store *storage.Store,
	id, code string,
	state domain.RunState,
	vesselID *string,
	start, end *time.Time,
) domain.FermentationRun {
	t.Helper()
	var run domain.FermentationRun
	err := store.Execute(func(snapshot *domain.Snapshot) error {
		run = domain.FermentationRun{
			ID:             id,
			Code:           code,
			FormulaID:      "form_1",
			VesselID:       vesselID,
			PlannedVolumeL: 100,
			State:          state,
			PlannedStartAt: start,
			PlannedEndAt:   end,
			Observations:   []domain.GravityObservation{},
			Version:        1,
			CreatedAt:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		}
		snapshot.Runs[id] = run
		snapshot.RunIndex[code] = id
		return nil
	})
	if err != nil {
		t.Fatalf("insert run %s: %v", id, err)
	}
	return run
}

func TestReserveRunAcceptsNonOverlappingSchedule(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	insertTestVessel(t, store, nil, nil)
	firstStart := now.Add(24 * time.Hour)
	firstEnd := now.Add(48 * time.Hour)
	secondStart := now.Add(48 * time.Hour)
	secondEnd := now.Add(72 * time.Hour)
	insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	insertTestRun(t, store, "run_2", "RUN-2", domain.RunPlanned, nil, nil, nil)

	first, vessel, err := service.ReserveRun("run_1", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &firstStart,
		PlannedEndAt:   &firstEnd,
	})
	if err != nil {
		t.Fatalf("reserve first run: %v", err)
	}
	if first.State != domain.RunReserved || vessel.State != domain.VesselReserved {
		t.Fatalf("first reservation changed states to %s/%s", first.State, vessel.State)
	}
	if vessel.ActiveRunID == nil || *vessel.ActiveRunID != "run_1" {
		t.Fatalf("vessel pointer = %v, want run_1", vessel.ActiveRunID)
	}

	second, vessel, err := service.ReserveRun("run_2", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &secondStart,
		PlannedEndAt:   &secondEnd,
	})
	if err != nil {
		t.Fatalf("reserve adjacent run: %v", err)
	}
	if second.State != domain.RunReserved || vessel.ActiveRunID == nil ||
		*vessel.ActiveRunID != "run_1" || vessel.Version != 2 {
		t.Fatalf("adjacent reservation changed vessel: %+v", vessel)
	}
}

func TestReserveRunRejectsOverlapWithoutMutation(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	insertTestVessel(t, store, nil, nil)
	firstStart := now.Add(24 * time.Hour)
	firstEnd := now.Add(48 * time.Hour)
	secondStart := now.Add(47 * time.Hour)
	secondEnd := now.Add(72 * time.Hour)
	insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	insertTestRun(t, store, "run_2", "RUN-2", domain.RunPlanned, nil, nil, nil)

	if _, _, err := service.ReserveRun("run_1", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &firstStart,
		PlannedEndAt:   &firstEnd,
	}); err != nil {
		t.Fatalf("reserve first run: %v", err)
	}
	_, _, err := service.ReserveRun("run_2", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &secondStart,
		PlannedEndAt:   &secondEnd,
	})
	if !domain.IsCode(err, domain.CodeScheduleConflict) {
		t.Fatalf("error = %v, want schedule_conflict", err)
	}
	second, err := service.GetRun("run_2")
	if err != nil {
		t.Fatalf("get second run: %v", err)
	}
	if second.State != domain.RunPlanned || second.VesselID != nil || second.Version != 1 {
		t.Fatalf("failed reservation mutated run: %+v", second)
	}
	vessel, err := service.GetVessel("vsl_1")
	if err != nil {
		t.Fatalf("get vessel: %v", err)
	}
	if vessel.Version != 2 || vessel.State != domain.VesselReserved ||
		vessel.ActiveRunID == nil || *vessel.ActiveRunID != "run_1" {
		t.Fatalf("failed reservation mutated vessel: %+v", vessel)
	}
}

func TestReserveRunStaleVersionDoesNotMutate(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	insertTestVessel(t, store, nil, nil)
	run := insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	start := now.Add(24 * time.Hour)
	end := now.Add(48 * time.Hour)

	_, _, err := service.ReserveRun("run_1", ReserveRunInput{
		ExpectedVersion: expectedVersion(run.Version + 99),
		VesselID:        "vsl_1",
		PlannedStartAt:  &start,
		PlannedEndAt:    &end,
	})
	if !domain.IsCode(err, domain.CodeRevisionConflict) {
		t.Fatalf("error = %v, want revision_conflict", err)
	}
	storedRun, err := service.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if storedRun.State != domain.RunPlanned || storedRun.VesselID != nil || storedRun.Version != 1 {
		t.Fatalf("stale reserve mutated run: %+v", storedRun)
	}
	vessel, err := service.GetVessel("vsl_1")
	if err != nil {
		t.Fatalf("get vessel: %v", err)
	}
	if vessel.State != domain.VesselAvailable || vessel.Version != 1 || vessel.ActiveRunID != nil {
		t.Fatalf("stale reserve mutated vessel: %+v", vessel)
	}
}

func TestReserveRunRejectsWindowOutsideVesselAvailability(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	from := now.Add(24 * time.Hour)
	until := now.Add(72 * time.Hour)
	insertTestVessel(t, store, &from, &until)
	insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	tooLateEnd := until.Add(time.Hour)

	_, _, err := service.ReserveRun("run_1", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &from,
		PlannedEndAt:   &tooLateEnd,
	})
	if !domain.IsCode(err, domain.CodeWindowUnsolvable) {
		t.Fatalf("error = %v, want window_unsolvable", err)
	}
}

func TestStartRunKeepsOriginalPlanForDeviation(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	insertTestVessel(t, store, nil, nil)
	plannedStart := now.Add(24 * time.Hour)
	plannedEnd := now.Add(48 * time.Hour)
	insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	reserved, _, err := service.ReserveRun("run_1", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &plannedStart,
		PlannedEndAt:   &plannedEnd,
	})
	if err != nil {
		t.Fatalf("reserve run: %v", err)
	}

	started, vessel, err := service.StartRun("run_1", VersionInput{ExpectedVersion: expectedVersion(reserved.Version)})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if started.StartedAt == nil || !started.StartedAt.Equal(now) {
		t.Fatalf("actual start = %v, want %v", started.StartedAt, now)
	}
	if started.PlannedStartAt == nil || !started.PlannedStartAt.Equal(plannedStart) ||
		started.PlannedEndAt == nil || !started.PlannedEndAt.Equal(plannedEnd) {
		t.Fatalf("original plan was not retained: %+v", started)
	}
	if vessel.State != domain.VesselInUse {
		t.Fatalf("vessel state = %s, want in_use", vessel.State)
	}
}

func TestAbortUnstartedPointerReservationSelectsNextRun(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	insertTestVessel(t, store, nil, nil)
	firstStart := now.Add(24 * time.Hour)
	firstEnd := now.Add(48 * time.Hour)
	secondStart := now.Add(48 * time.Hour)
	secondEnd := now.Add(72 * time.Hour)
	insertTestRun(t, store, "run_1", "RUN-1", domain.RunPlanned, nil, nil, nil)
	insertTestRun(t, store, "run_2", "RUN-2", domain.RunPlanned, nil, nil, nil)
	if _, _, err := service.ReserveRun("run_1", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &firstStart,
		PlannedEndAt:   &firstEnd,
	}); err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	second, _, err := service.ReserveRun("run_2", ReserveRunInput{
		VesselID:       "vsl_1",
		PlannedStartAt: &secondStart,
		PlannedEndAt:   &secondEnd,
	})
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}

	_, vessel, err := service.AbortRun("run_1", VersionInput{ExpectedVersion: expectedVersion(2)})
	if err != nil {
		t.Fatalf("abort first: %v", err)
	}
	if vessel.State != domain.VesselReserved || vessel.ActiveRunID == nil ||
		*vessel.ActiveRunID != "run_2" {
		t.Fatalf("vessel after abort = %+v, want pointer to run_2", vessel)
	}
	started, _, err := service.StartRun("run_2", VersionInput{ExpectedVersion: expectedVersion(second.Version)})
	if err != nil {
		t.Fatalf("start next reservation: %v", err)
	}
	if started.State != domain.RunFermenting {
		t.Fatalf("next run state = %s, want fermenting", started.State)
	}
}

func TestListRunsFiltersOverlapsAndSortsStably(t *testing.T) {
	service, store, now := newTestService(t)
	insertApprovedFormula(t, store)
	first := now.Add(24 * time.Hour)
	second := now.Add(48 * time.Hour)
	third := now.Add(72 * time.Hour)
	thirdEnd := third.Add(time.Hour)
	insertTestRun(t, store, "run_later", "RUN-LATER", domain.RunPlanned, nil, &third, &thirdEnd)
	insertTestRun(t, store, "run_earlier", "RUN-EARLIER", domain.RunPlanned, nil, &first, &second)
	outsideStart := now.Add(96 * time.Hour)
	outsideEnd := now.Add(120 * time.Hour)
	insertTestRun(t, store, "run_outside", "RUN-OUTSIDE", domain.RunPlanned, nil,
		&outsideStart, &outsideEnd)
	insertTestRun(t, store, "run_open", "RUN-OPEN", domain.RunFermenting, nil, &second, nil)

	from := now.Add(25 * time.Hour)
	to := now.Add(49 * time.Hour)
	runs, err := service.ListRuns(RunListQuery{From: &from, To: &to})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	gotIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		gotIDs = append(gotIDs, run.ID)
	}
	wantIDs := []string{"run_earlier", "run_open"}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("ids = %v, want %v", gotIDs, wantIDs)
	}
	for index := range wantIDs {
		if gotIDs[index] != wantIDs[index] {
			t.Fatalf("ids = %v, want %v", gotIDs, wantIDs)
		}
	}
}
