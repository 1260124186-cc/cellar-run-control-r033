package service

import (
	"sort"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateRun(input CreateRunInput) (domain.FermentationRun, error) {
	now := s.clock.Now()
	code := domain.NormalizeRunCode(input.Code)
	if code == "" {
		generated, err := generatedRunCode()
		if err != nil {
			return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
		}
		code = generated
	}
	run := domain.FermentationRun{
		Code:           code,
		FormulaID:      input.FormulaID,
		PlannedVolumeL: input.PlannedVolumeL,
		Notes:          input.Notes,
		State:          domain.RunPlanned,
		Observations:   []domain.GravityObservation{},
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if input.PlannedStartAt != nil {
		start := input.PlannedStartAt.UTC()
		run.PlannedStartAt = &start
	}
	if input.PlannedEndAt != nil {
		end := input.PlannedEndAt.UTC()
		run.PlannedEndAt = &end
	}
	if err := run.Validate(); err != nil {
		return domain.FermentationRun{}, err
	}
	id, err := generateEntityID("run")
	if err != nil {
		return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	run.ID = id

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		formula, ok := snapshot.Formulas[run.FormulaID]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if formula.State != domain.FormulaApproved {
			return domain.NewError(domain.CodeFormulaState,
				"run requires an approved formula")
		}
		if _, exists := snapshot.RunIndex[run.Code]; exists {
			return domain.NewError(domain.CodeRunConflict,
				"a run with the same code already exists")
		}
		snapshot.Runs[run.ID] = run.Clone()
		snapshot.RunIndex[run.Code] = run.ID
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return run.Clone(), nil
}

func (s *Service) ReserveRun(
	id string,
	input ReserveRunInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	if err := domain.ValidatePlannedWindow(input.PlannedStartAt, input.PlannedEndAt); err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		vessel, ok := snapshot.Vessels[input.VesselID]
		if !ok {
			return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
		}
		if !vesselCanAcceptReservation(vessel.State) {
			return domain.NewError(domain.CodeVesselUnavailable,
				"vessel is not available for reservation")
		}
		if run.State != domain.RunPlanned {
			return domain.NewError(domain.CodeRunState, "only a planned run can reserve a vessel")
		}
		if run.PlannedVolumeL > vessel.CapacityL {
			return domain.FieldError(domain.CodeInvalidInput,
				"planned_volume_liters", "exceeds vessel capacity")
		}
		now := s.clock.Now()
		window := reservationWindow(run, input, now)
		if err := checkVesselSchedule(vessel, snapshot, window, run.ID,
			!window.start.After(now)); err != nil {
			return err
		}

		var activeWindow scheduleWindow
		if vessel.ActiveRunID != nil {
			if active, found := snapshot.Runs[*vessel.ActiveRunID]; found {
				activeWindow = effectiveWindow(active)
			}
		}
		if err := applyVesselReservation(&vessel, run.ID, window, activeWindow, now); err != nil {
			return err
		}
		if err := run.ReserveVessel(vessel.ID, input.PlannedStartAt, input.PlannedEndAt, now); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func reservationWindow(
	run domain.FermentationRun,
	input ReserveRunInput,
	now time.Time,
) scheduleWindow {
	start := utcPointer(run.PlannedStartAt)
	end := utcPointer(run.PlannedEndAt)
	if input.PlannedStartAt != nil {
		start = utcPointer(input.PlannedStartAt)
	}
	if input.PlannedEndAt != nil {
		end = utcPointer(input.PlannedEndAt)
	}
	if start == nil {
		stamp := now.UTC()
		start = &stamp
	}
	return scheduleWindow{start: start, end: end}
}

func applyVesselReservation(
	vessel *domain.Vessel,
	runID string,
	window scheduleWindow,
	activeWindow scheduleWindow,
	now time.Time,
) error {
	now = now.UTC()
	isImmediate := !window.start.After(now)
	if isImmediate {
		switch vessel.State {
		case domain.VesselAvailable:
			return vessel.Reserve(runID, now)
		case domain.VesselReserved, domain.VesselInUse:
			if vessel.ActiveRunID == nil {
				return vessel.AssignReservation(runID, now)
			}
			return vessel.HoldReservation(runID)
		default:
			return domain.NewError(domain.CodeVesselUnavailable,
				"vessel is not available for reservation")
		}
	}

	switch vessel.State {
	case domain.VesselAvailable:
		return vessel.Reserve(runID, now)
	case domain.VesselReserved, domain.VesselInUse:
		if vessel.ActiveRunID == nil {
			return vessel.AssignReservation(runID, now)
		}
		if activeWindow.start == nil || window.start.Before(*activeWindow.start) {
			return vessel.AssignReservation(runID, now)
		}
		return vessel.HoldReservation(runID)
	default:
		return domain.NewError(domain.CodeVesselUnavailable,
			"vessel is not available for reservation")
	}
}

func (s *Service) StartRun(
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if run.VesselID == nil {
			return domain.NewError(domain.CodeRunState, "run has no vessel")
		}
		vessel, ok := snapshot.Vessels[*run.VesselID]
		if !ok {
			return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
		}
		now := s.clock.Now()
		window := startingWindow(run, now)
		if err := checkVesselSchedule(vessel, snapshot, window, run.ID, true); err != nil {
			return err
		}

		activeIsRun := vessel.ActiveRunID != nil && *vessel.ActiveRunID == run.ID
		if !activeIsRun {
			return domain.NewError(domain.CodeVesselState, "vessel is not reserved by this run")
		}
		if err := vessel.BeginRun(run.ID, now); err != nil {
			return err
		}
		if err := run.StartFermentation(now); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func startingWindow(run domain.FermentationRun, now time.Time) scheduleWindow {
	start := now.UTC()
	end := utcPointer(run.PlannedEndAt)
	if end != nil && !end.After(start) {
		end = nil
	}
	return scheduleWindow{start: &start, end: end}
}

func (s *Service) AbortRun(
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		hadStarted := run.HadStarted()
		if err := run.Abort(s.clock.Now()); err != nil {
			return err
		}
		if run.VesselID != nil {
			now := s.clock.Now()
			vessel, ok := snapshot.Vessels[*run.VesselID]
			if !ok {
				return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
			}
			if vessel.ActiveRunID == nil || *vessel.ActiveRunID != run.ID {
				// A non-pointer reservation occupies no current vessel state;
				// only the run aggregate changes when it is cancelled.
				snapshot.Runs[id] = run.Clone()
				updatedRun = run.Clone()
				return nil
			}
			if hadStarted {
				if err := vessel.StartCleaning(run.ID, now); err != nil {
					return err
				}
			} else {
				next := earliestReservedRun(snapshot, vessel.ID, run.ID)
				if err := vessel.ReleaseReservation(run.ID, runIDPointer(next), now); err != nil {
					return err
				}
			}
			snapshot.Vessels[vessel.ID] = vessel
			updatedVessel = vessel
		}
		snapshot.Runs[id] = run.Clone()
		updatedRun = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func runIDPointer(run *domain.FermentationRun) *string {
	if run == nil {
		return nil
	}
	value := run.ID
	return &value
}

func (s *Service) GetRun(id string) (domain.FermentationRun, error) {
	var found domain.FermentationRun
	err := s.store.View(func(snapshot domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		found = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return found, nil
}

func (s *Service) ListRuns(query RunListQuery) ([]domain.FermentationRun, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	var runs []domain.FermentationRun
	err = s.store.View(func(snapshot domain.Snapshot) error {
		runs = make([]domain.FermentationRun, 0, len(snapshot.Runs))
		for _, run := range snapshot.Runs {
			if runWindowInRange(run, query) {
				runs = append(runs, run.Clone())
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		return compareRunSchedule(runs[i], runs[j])
	})
	return runs, nil
}
