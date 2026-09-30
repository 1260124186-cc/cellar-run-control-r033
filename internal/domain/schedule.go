package domain

import "time"

// ValidatePlannedWindow checks the structural rules of a planned run window.
// A window is either fully absent or carries both bounds, and the estimated
// end must fall strictly after the planned start.
func ValidatePlannedWindow(start, end *time.Time) error {
	if start == nil && end == nil {
		return nil
	}
	if start == nil || end == nil {
		return FieldError(CodeInvalidInput, "planned_start_at",
			"planned window requires both planned_start_at and planned_end_at")
	}
	if !end.After(*start) {
		return FieldError(CodeInvalidInput, "planned_end_at",
			"must be after planned_start_at")
	}
	return nil
}

// EffectiveWindow resolves the occupancy window of a run. Actual timestamps
// take precedence over the planned window so that, once a run has started,
// range queries and conflict checks follow the real timeline while the
// original plan stays available for deviation reading. An estimated end in
// the past relative to an actual start is treated as unknown rather than as
// a backwards-facing window.
func (r FermentationRun) EffectiveWindow() (start, end *time.Time) {
	start = r.PlannedStartAt
	if r.StartedAt != nil {
		start = r.StartedAt
	}
	end = r.PlannedEndAt
	if r.EndedAt != nil {
		end = r.EndedAt
	} else if start != nil && end != nil && end.Before(*start) {
		end = nil
	}
	return start, end
}

// WindowsOverlap reports whether two half-open windows share any time.
// A nil bound means the window is unbounded on that side.
func WindowsOverlap(aStart, aEnd, bStart, bEnd *time.Time) bool {
	if aStart != nil && bEnd != nil && !aStart.Before(*bEnd) {
		return false
	}
	if bStart != nil && aEnd != nil && !bStart.Before(*aEnd) {
		return false
	}
	return true
}

// WindowFits reports whether a complete window lies inside the vessel
// availability period. A vessel without a registered period accepts any
// complete window.
func (v Vessel) WindowFits(start, end time.Time) bool {
	if !end.After(start) {
		return false
	}
	if v.AvailableFrom != nil && start.Before(*v.AvailableFrom) {
		return false
	}
	if v.AvailableUntil != nil && end.After(*v.AvailableUntil) {
		return false
	}
	return true
}

// HasAvailabilityPeriod reports whether the vessel has bounded availability.
func (v Vessel) HasAvailabilityPeriod() bool {
	return v.AvailableFrom != nil && v.AvailableUntil != nil
}

// WindowFitsPointer checks a possibly unbounded window against the vessel
// availability period. A bounded vessel requires the complete window to fit.
func (v Vessel) WindowFitsPointer(start, end *time.Time) bool {
	if start != nil && end != nil && !end.After(*start) {
		return false
	}
	if !v.HasAvailabilityPeriod() {
		return true
	}
	if start == nil || end == nil {
		return false
	}
	return !start.Before(*v.AvailableFrom) && !end.After(*v.AvailableUntil)
}
