package workflowcheck

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func scheduledRuns(api *client) error {
	formula, err := ensureApprovedFormula(api, "Scheduled Formula")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Add(time.Hour)
	availableFrom := now.Add(-time.Hour)
	availableUntil := now.Add(96 * time.Hour)
	vesselResponse, err := api.call("POST", "/v1/vessels", map[string]any{
		"code":            "VSL-303",
		"capacity_liters": 500.0,
		"available_from":  availableFrom.Format(time.RFC3339),
		"available_until": availableUntil.Format(time.RFC3339),
	}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(vesselResponse, 201, "create bounded vessel"); err != nil {
		return err
	}
	var vessel vesselView
	if err := vesselResponse.decode(&vessel); err != nil {
		return err
	}

	createResponse, err := api.call("POST", "/v1/runs", map[string]any{
		"code":                  "RUN-SCHEDULE",
		"formula_id":            formula.ID,
		"planned_volume_liters": 200.0,
		"planned_start_at":      now.Format(time.RFC3339),
		"planned_end_at":        now.Add(24 * time.Hour).Format(time.RFC3339),
	}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(createResponse, 201, "create scheduled run"); err != nil {
		return err
	}
	var run runView
	if err := createResponse.decode(&run); err != nil {
		return err
	}
	reserveResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vessel.ID}, ptrInt64(run.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(reserveResponse, 200, "reserve scheduled run"); err != nil {
		return err
	}
	if err := reserveResponse.decode(&pairView{}); err != nil {
		return err
	}

	otherStart := now.Add(25 * time.Hour)
	otherEnd := now.Add(49 * time.Hour)
	otherResponse, err := api.call("POST", "/v1/runs", map[string]any{
		"code":                  "RUN-SCHEDULE-LATER",
		"formula_id":            formula.ID,
		"planned_volume_liters": 200.0,
		"planned_start_at":      otherStart.Format(time.RFC3339),
		"planned_end_at":        otherEnd.Format(time.RFC3339),
	}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(otherResponse, 201, "create later run"); err != nil {
		return err
	}
	var other runView
	if err := otherResponse.decode(&other); err != nil {
		return err
	}
	otherReserve, err := api.call("POST", "/v1/runs/"+other.ID+"/reserve",
		map[string]any{"vessel_id": vessel.ID}, ptrInt64(other.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(otherReserve, 200, "reserve later run"); err != nil {
		return err
	}

	from := now.Add(26 * time.Hour)
	listResponse, err := api.call("GET",
		"/v1/runs?from="+url.QueryEscape(from.Format(time.RFC3339)), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(listResponse, 200, "list runs in range"); err != nil {
		return err
	}
	var listed struct {
		Runs []runView `json:"runs"`
	}
	if err := listResponse.decode(&listed); err != nil {
		return err
	}
	if len(listed.Runs) != 1 || listed.Runs[0].ID != other.ID {
		raw, _ := json.Marshal(listed.Runs)
		return fmt.Errorf("range query returned %s, want only later run", raw)
	}

	tooLateEnd := availableUntil.Add(time.Hour)
	unsolvableResponse, err := api.call("POST", "/v1/runs", map[string]any{
		"code":                  "RUN-SCHEDULE-UNSOLVABLE",
		"formula_id":            formula.ID,
		"planned_volume_liters": 200.0,
		"planned_start_at":      availableUntil.Format(time.RFC3339),
		"planned_end_at":        tooLateEnd.Format(time.RFC3339),
	}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(unsolvableResponse, 201, "create unsolvable run"); err != nil {
		return err
	}
	var unsolvable runView
	if err := unsolvableResponse.decode(&unsolvable); err != nil {
		return err
	}
	rejected, err := api.call("POST", "/v1/runs/"+unsolvable.ID+"/reserve",
		map[string]any{"vessel_id": vessel.ID}, ptrInt64(unsolvable.Version))
	if err != nil {
		return err
	}
	if err := requireCode(rejected, 409, string(domain.CodeWindowUnsolvable),
		"reject window beyond vessel availability"); err != nil {
		return err
	}

	storedPair := pairView{}
	if err := reserveResponse.decode(&storedPair); err != nil {
		return err
	}
	startResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/start",
		nil, ptrInt64(storedPair.Run.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(startResponse, 200, "start scheduled run"); err != nil {
		return err
	}
	var startedPair pairView
	if err := startResponse.decode(&startedPair); err != nil {
		return err
	}
	if startedPair.Run.StartedAt == nil ||
		startedPair.Run.PlannedStartAt == nil ||
		startedPair.Run.StartedAt.UTC().Truncate(time.Second).
			Equal(startedPair.Run.PlannedStartAt.UTC().Truncate(time.Second)) {
		return errInvalidWorkflow("actual start did not take precedence while retaining planned start")
	}
	return nil
}
