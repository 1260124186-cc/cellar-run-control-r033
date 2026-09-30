package service

import (
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

type scheduleWindow struct {
	start *time.Time
	end   *time.Time
}

func (q RunListQuery) normalized() (RunListQuery, error) {
	if q.From != nil {
		from := q.From.UTC()
		q.From = &from
	}
	if q.To != nil {
		to := q.To.UTC()
		q.To = &to
	}
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		return q, domain.FieldError(domain.CodeInvalidInput, "from",
			"must not be after to")
	}
	return q, nil
}

func runIsOpen(run domain.FermentationRun) bool {
	return run.State != domain.RunCompleted && run.State != domain.RunAborted
}

func vesselCanAcceptReservation(state domain.VesselState) bool {
	return state == domain.VesselAvailable ||
		state == domain.VesselReserved ||
		state == domain.VesselInUse
}

func effectiveWindow(run domain.FermentationRun) scheduleWindow {
	start, end := run.EffectiveWindow()
	return scheduleWindow{start: utcPointer(start), end: utcPointer(end)}
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// checkVesselSchedule verifies that a candidate window is feasible for the
// vessel and does not overlap another open run attached to it.
func checkVesselSchedule(
	vessel domain.Vessel,
	snapshot *domain.Snapshot,
	candidate scheduleWindow,
	excludedRunID string,
	immediate bool,
) error {
	if vessel.HasAvailabilityPeriod() && !vessel.WindowFitsPointer(candidate.start, candidate.end) {
		return domain.FieldError(domain.CodeWindowUnsolvable, "planned_start_at",
			"run window must lie inside the vessel availability period")
	}
	if candidate.start != nil && candidate.end != nil && !candidate.end.After(*candidate.start) {
		return domain.FieldError(domain.CodeInvalidInput, "planned_end_at",
			"must be after planned_start_at")
	}
	for _, other := range snapshot.Runs {
		if other.ID == excludedRunID {
			continue
		}
		if other.VesselID == nil || *other.VesselID != vessel.ID || !runIsOpen(other) {
			continue
		}
		otherWindow := effectiveWindow(other)
		if domain.WindowsOverlap(candidate.start, candidate.end,
			otherWindow.start, otherWindow.end) {
			code := domain.CodeScheduleConflict
			message := "vessel is already occupied during the requested run window"
			if immediate {
				code = domain.CodeVesselUnavailable
				message = "vessel is currently occupied"
			}
			return domain.FieldError(code, "planned_start_at", message)
		}
	}
	return nil
}

// earliestReservedRun finds the nearest unstarted reservation for a vessel.
// It repoints the vessel after a reservation is released or cleaning finishes.
func earliestReservedRun(
	snapshot *domain.Snapshot,
	vesselID, excludedRunID string,
) *domain.FermentationRun {
	var earliest *domain.FermentationRun
	for _, run := range snapshot.Runs {
		if run.ID == excludedRunID ||
			run.VesselID == nil || *run.VesselID != vesselID ||
			run.State != domain.RunReserved || run.StartedAt != nil {
			continue
		}
		candidate := run.Clone()
		if earliest == nil {
			earliest = &candidate
			continue
		}
		if reservationComesBefore(candidate, *earliest) {
			earliest = &candidate
		}
	}
	return earliest
}

func reservationComesBefore(candidate, current domain.FermentationRun) bool {
	switch {
	case candidate.PlannedStartAt == nil && current.PlannedStartAt == nil:
		return candidate.ID < current.ID
	case candidate.PlannedStartAt == nil:
		return false
	case current.PlannedStartAt == nil:
		return true
	case candidate.PlannedStartAt.Equal(*current.PlannedStartAt):
		return candidate.ID < current.ID
	default:
		return candidate.PlannedStartAt.Before(*current.PlannedStartAt)
	}
}

func runWindowInRange(run domain.FermentationRun, query RunListQuery) bool {
	if query.From == nil && query.To == nil {
		return true
	}
	window := effectiveWindow(run)
	if window.start == nil {
		return query.To != nil
	}
	if query.To != nil && !window.start.Before(*query.To) {
		return false
	}
	if query.From != nil && window.end != nil && !window.end.After(*query.From) {
		return false
	}
	return true
}

func compareRunSchedule(left, right domain.FermentationRun) bool {
	leftWindow := effectiveWindow(left)
	rightWindow := effectiveWindow(right)
	switch {
	case leftWindow.start == nil && rightWindow.start == nil:
		return left.ID < right.ID
	case leftWindow.start == nil:
		return false
	case rightWindow.start == nil:
		return true
	case leftWindow.start.Equal(*rightWindow.start):
		return left.ID < right.ID
	default:
		return leftWindow.start.Before(*rightWindow.start)
	}
}
