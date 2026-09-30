package domain

import "time"

type VesselState string

const (
	VesselAvailable VesselState = "available"
	VesselReserved  VesselState = "reserved"
	VesselInUse     VesselState = "in_use"
	VesselCleaning  VesselState = "cleaning"
	VesselRetired   VesselState = "retired"
)

type Vessel struct {
	ID             string      `json:"id"`
	Code           string      `json:"code"`
	CapacityL      float64     `json:"capacity_liters"`
	State          VesselState `json:"state"`
	ActiveRunID    *string     `json:"active_run_id,omitempty"`
	AvailableFrom  *time.Time  `json:"available_from,omitempty"`
	AvailableUntil *time.Time  `json:"available_until,omitempty"`
	Version        int64       `json:"version"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

func (v Vessel) Clone() Vessel {
	cloned := v
	if v.ActiveRunID != nil {
		value := *v.ActiveRunID
		cloned.ActiveRunID = &value
	}
	if v.AvailableFrom != nil {
		value := *v.AvailableFrom
		cloned.AvailableFrom = &value
	}
	if v.AvailableUntil != nil {
		value := *v.AvailableUntil
		cloned.AvailableUntil = &value
	}
	return cloned
}

func (v Vessel) Validate() error {
	if err := ValidateVesselCode(v.Code, "code"); err != nil {
		return err
	}
	if v.CapacityL < 10 || v.CapacityL > 5000 {
		return FieldError(CodeInvalidInput, "capacity_liters",
			"must be between 10 and 5000 liters")
	}
	if (v.AvailableFrom == nil) != (v.AvailableUntil == nil) {
		return FieldError(CodeInvalidInput, "available_from",
			"availability period requires both available_from and available_until")
	}
	if v.AvailableFrom != nil && !v.AvailableUntil.After(*v.AvailableFrom) {
		return FieldError(CodeInvalidInput, "available_until",
			"must be after available_from")
	}
	return nil
}

func (v *Vessel) Reserve(runID string, now time.Time) error {
	if v.State != VesselAvailable || v.ActiveRunID != nil {
		return NewError(CodeVesselUnavailable, "vessel is not available for reservation")
	}
	v.assignRun(runID, now, VesselReserved)
	return nil
}

func (v *Vessel) assignRun(runID string, now time.Time, state VesselState) {
	run := runID
	v.State = state
	v.ActiveRunID = &run
	v.UpdatedAt = now.UTC()
	v.Version++
}

// AssignReservation records the nearest reservation on a vessel that already
// has other non-overlapping schedule entries. State remains reserved for a
// future-only schedule, and in_use while a current run occupies the vessel.
func (v *Vessel) AssignReservation(runID string, now time.Time) error {
	switch v.State {
	case VesselAvailable, VesselRetired, VesselCleaning:
		return NewError(CodeVesselUnavailable, "vessel is not available for reservation")
	case VesselReserved, VesselInUse:
		if v.ActiveRunID != nil && *v.ActiveRunID == runID {
			return NewError(CodeScheduleConflict, "run already owns the vessel pointer")
		}
		run := runID
		v.ActiveRunID = &run
		v.UpdatedAt = now.UTC()
		v.Version++
		return nil
	default:
		return NewError(CodeVesselState, "vessel cannot accept a reservation")
	}
}

// HoldReservation records a secondary, non-overlapping reservation on the run
// schedule. Vessel state is derived from its nearest/current entry, so adding
// a later entry does not require a vessel-pointer change.
func (v *Vessel) HoldReservation(runID string) error {
	if v.State != VesselReserved && v.State != VesselInUse {
		return NewError(CodeVesselUnavailable, "vessel is not available for reservation")
	}
	if v.ActiveRunID != nil && *v.ActiveRunID == runID {
		return NewError(CodeScheduleConflict, "run already owns the vessel pointer")
	}
	return nil
}

// BeginRun starts the run that currently owns the vessel. A future
// reservation can start even if its window began earlier than the current
// service time.
func (v *Vessel) BeginRun(runID string, now time.Time) error {
	if (v.State != VesselReserved && v.State != VesselAvailable) ||
		v.ActiveRunID == nil || *v.ActiveRunID != runID {
		return NewError(CodeVesselState, "vessel is not reserved by this run")
	}
	run := runID
	v.State = VesselInUse
	v.ActiveRunID = &run
	v.UpdatedAt = now.UTC()
	v.Version++
	return nil
}

// AssignNextReservation moves the vessel pointer to its nearest upcoming run.
// If none exists, the vessel becomes available.
func (v *Vessel) AssignNextReservation(runID *string, now time.Time) {
	if runID == nil {
		v.State = VesselAvailable
		v.ActiveRunID = nil
	} else {
		value := *runID
		v.ActiveRunID = &value
		v.State = VesselReserved
	}
	v.UpdatedAt = now.UTC()
	v.Version++
}

func (v *Vessel) StartCleaning(runID string, now time.Time) error {
	if v.State != VesselInUse ||
		v.ActiveRunID == nil || *v.ActiveRunID != runID {
		return NewError(CodeVesselState, "vessel cannot enter cleaning for this run")
	}
	v.State = VesselCleaning
	v.UpdatedAt = now.UTC()
	v.Version++
	return nil
}

func (v *Vessel) FinishCleaning(nextRunID *string, now time.Time) error {
	if v.State != VesselCleaning || v.ActiveRunID == nil {
		return NewError(CodeVesselState, "vessel is not waiting for cleaning")
	}
	v.AssignNextReservation(nextRunID, now)
	return nil
}

func (v *Vessel) ReleaseReservation(runID string, nextRunID *string, now time.Time) error {
	if v.State != VesselReserved && v.State != VesselInUse {
		return NewError(CodeVesselState, "vessel does not have a releasable run")
	}
	if v.ActiveRunID == nil || *v.ActiveRunID != runID {
		return NewError(CodeVesselState, "vessel belongs to a different run")
	}
	v.AssignNextReservation(nextRunID, now)
	return nil
}

func (v *Vessel) Retire(now time.Time) error {
	if v.State == VesselRetired {
		return NewError(CodeVesselState, "vessel is already retired")
	}
	if v.State != VesselAvailable || v.ActiveRunID != nil {
		return NewError(CodeVesselState, "only an available vessel can be retired")
	}
	v.State = VesselRetired
	v.UpdatedAt = now.UTC()
	v.Version++
	return nil
}
