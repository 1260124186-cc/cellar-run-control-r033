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
		Code:             code,
		FormulaID:        input.FormulaID,
		PlannedVolumeL:   input.PlannedVolumeL,
		Notes:            input.Notes,
		State:            domain.RunPlanned,
		PlannedStartAt:   normalizeTimePtr(input.PlannedStartAt),
		StartWindowEndAt: normalizeTimePtr(input.StartWindowEndAt),
		PlannedEndAt:     normalizeTimePtr(input.PlannedEndAt),
		Observations:     []domain.GravityObservation{},
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := run.Validate(); err != nil {
		return domain.FermentationRun{}, err
	}
	if err := run.ValidateSchedule(now); err != nil {
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
		if vessel.State == domain.VesselRetired {
			return domain.NewError(domain.CodeVesselState, "vessel is retired")
		}
		if run.PlannedVolumeL > vessel.CapacityL {
			return domain.FieldError(domain.CodeInvalidInput,
				"planned_volume_liters", "exceeds vessel capacity")
		}
		if run.HasSchedule() {
			if err := run.ValidateSchedule(s.clock.Now()); err != nil {
				return err
			}
			if err := ensureVesselAvailableForWindow(snapshot, vessel.ID, run); err != nil {
				return err
			}
		} else if vessel.State != domain.VesselAvailable || vessel.ActiveRunID != nil {
			return domain.NewError(domain.CodeVesselUnavailable,
				"vessel is not available for reservation")
		}
		if vessel.State == domain.VesselAvailable {
			if err := vessel.Reserve(run.ID, s.clock.Now()); err != nil {
				return err
			}
		}
		if err := run.ReserveVessel(vessel.ID, s.clock.Now()); err != nil {
			return err
		}
		// Publish the mutated run and vessel before recomputing the derived
		// vessel state, so the booking calendar observes the new lifecycle.
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		recomputeVesselBooking(snapshot, vessel.ID)
		updatedRun = run.Clone()
		updatedVessel = snapshot.Vessels[vessel.ID]
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

// ensureVesselAvailableForWindow checks that no other non-terminal run on the
// vessel occupies a window overlapping the candidate run's window. A run with
// no occupancy interval (an unscheduled reservation, or a run that started
// without a planned end) is treated as an open-ended booking and blocks the
// vessel until it is resolved.
func ensureVesselAvailableForWindow(
	snapshot *domain.Snapshot,
	vesselID string,
	candidate domain.FermentationRun,
) error {
	start, end, ok := candidate.OccupancyInterval()
	if !ok {
		return nil
	}
	for _, other := range snapshot.Runs {
		if other.ID == candidate.ID || other.VesselID == nil || *other.VesselID != vesselID {
			continue
		}
		if isTerminalRun(other.State) {
			continue
		}
		_, _, otherHasInterval := other.OccupancyInterval()
		if !otherHasInterval {
			return domain.NewError(domain.CodeScheduleConflict,
				"vessel is already booked for an open-ended window")
		}
		if other.OverlapsInterval(start, end) {
			return domain.NewError(domain.CodeScheduleConflict,
				"vessel is already booked for an overlapping window")
		}
	}
	return nil
}

func isTerminalRun(state domain.RunState) bool {
	return state == domain.RunCompleted || state == domain.RunAborted
}

// recomputeVesselBooking derives the vessel's state and active run from its
// non-terminal runs. Vessels in cleaning or retired are left untouched.
func recomputeVesselBooking(snapshot *domain.Snapshot, vesselID string) {
	vessel, ok := snapshot.Vessels[vesselID]
	if !ok {
		return
	}
	if vessel.State == domain.VesselCleaning || vessel.State == domain.VesselRetired {
		return
	}
	var inUse *domain.FermentationRun
	var upcoming []domain.FermentationRun
	for _, run := range snapshot.Runs {
		if run.VesselID == nil || *run.VesselID != vesselID {
			continue
		}
		switch run.State {
		case domain.RunFermenting, domain.RunConditioning:
			value := run
			inUse = &value
		case domain.RunPlanned, domain.RunReserved:
			upcoming = append(upcoming, run)
		}
	}
	if inUse != nil {
		vessel.State = domain.VesselInUse
		vessel.ActiveRunID = &inUse.ID
		snapshot.Vessels[vesselID] = vessel
		return
	}
	if len(upcoming) > 0 {
		sort.Slice(upcoming, func(i, j int) bool {
			return occupancyStart(upcoming[i]).Before(occupancyStart(upcoming[j]))
		})
		vessel.State = domain.VesselReserved
		vessel.ActiveRunID = &upcoming[0].ID
		snapshot.Vessels[vesselID] = vessel
		return
	}
	vessel.State = domain.VesselAvailable
	vessel.ActiveRunID = nil
	snapshot.Vessels[vesselID] = vessel
}

func occupancyStart(run domain.FermentationRun) time.Time {
	if run.PlannedStartAt != nil {
		return *run.PlannedStartAt
	}
	return run.CreatedAt
}

func normalizeTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
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
		if err := vessel.StartRun(run.ID, s.clock.Now()); err != nil {
			return err
		}
		if err := run.StartFermentation(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		recomputeVesselBooking(snapshot, vessel.ID)
		updatedRun = run.Clone()
		updatedVessel = snapshot.Vessels[vessel.ID]
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
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
			vessel, ok := snapshot.Vessels[*run.VesselID]
			if !ok {
				return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
			}
			if hadStarted {
				if err := vessel.StartCleaning(run.ID, s.clock.Now()); err != nil {
					return err
				}
			} else if err := vessel.ReleaseReservation(run.ID, s.clock.Now()); err != nil {
				return err
			}
			snapshot.Runs[id] = run.Clone()
			snapshot.Vessels[vessel.ID] = vessel
			recomputeVesselBooking(snapshot, vessel.ID)
			updatedVessel = snapshot.Vessels[vessel.ID]
		} else {
			snapshot.Runs[id] = run.Clone()
		}
		updatedRun = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) GetRun(id string) (domain.FermentationRun, error) {
	var found domain.FermentationRun
	err := s.store.View(func(snapshot domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		found = run.Clone()
		found.Variance = found.ScheduleVariance()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return found, nil
}

// ListRunsInput filters the run list. From and To are inclusive bounds on the
// planned occupancy window; a run is included when its window intersects the
// requested range. Runs without a schedule are only included when no time
// filter is supplied.
type ListRunsInput struct {
	From *time.Time
	To   *time.Time
}

func (s *Service) ListRuns(input ListRunsInput) ([]domain.FermentationRun, error) {
	var runs []domain.FermentationRun
	err := s.store.View(func(snapshot domain.Snapshot) error {
		runs = make([]domain.FermentationRun, 0, len(snapshot.Runs))
		for _, run := range snapshot.Runs {
			if !runMatchesRange(run, input.From, input.To) {
				continue
			}
			cloned := run.Clone()
			cloned.Variance = cloned.ScheduleVariance()
			runs = append(runs, cloned)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		left, right := runs[i], runs[j]
		leftScheduled, rightScheduled := left.HasSchedule(), right.HasSchedule()
		if leftScheduled != rightScheduled {
			return leftScheduled
		}
		if leftScheduled {
			leftStart, rightStart := *left.PlannedStartAt, *right.PlannedStartAt
			if !leftStart.Equal(rightStart) {
				return leftStart.Before(rightStart)
			}
		}
		if left.Code == right.Code {
			return left.ID < right.ID
		}
		return left.Code < right.Code
	})
	return runs, nil
}

func runMatchesRange(run domain.FermentationRun, from, to *time.Time) bool {
	if from == nil && to == nil {
		return true
	}
	if !run.HasSchedule() {
		return false
	}
	start := *run.PlannedStartAt
	end := *run.PlannedEndAt
	if from != nil && end.Before(from.UTC()) {
		return false
	}
	if to != nil && start.After(to.UTC()) {
		return false
	}
	return true
}
