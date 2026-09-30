package httpapi

import (
	"net/http"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

type createRunRequest struct {
	Code           string     `json:"code"`
	FormulaID      string     `json:"formula_id"`
	PlannedVolumeL float64    `json:"planned_volume_liters"`
	Notes          string     `json:"notes"`
	PlannedStartAt *time.Time `json:"planned_start_at"`
	PlannedEndAt   *time.Time `json:"planned_end_at"`
}

type reserveRunRequest struct {
	VesselID       string     `json:"vessel_id"`
	PlannedStartAt *time.Time `json:"planned_start_at"`
	PlannedEndAt   *time.Time `json:"planned_end_at"`
}

type observationRequest struct {
	Sequence     int       `json:"sequence"`
	Gravity      float64   `json:"gravity"`
	TemperatureC float64   `json:"temperature_c"`
	ObservedAt   time.Time `json:"observed_at"`
	Note         string    `json:"note"`
}

func runListQuery(request *http.Request) (service.RunListQuery, error) {
	var query service.RunListQuery
	values := request.URL.Query()
	for _, name := range []string{"from", "to"} {
		raw := values.Get(name)
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, domain.FieldError(domain.CodeInvalidInput, name,
				"must be an RFC3339 timestamp")
		}
		if name == "from" {
			query.From = &parsed
		} else {
			query.To = &parsed
		}
	}
	return query, nil
}

func registerRunRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("POST /v1/runs", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[createRunRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		run, err := app.CreateRun(service.CreateRunInput{
			Code:           body.Code,
			FormulaID:      body.FormulaID,
			PlannedVolumeL: body.PlannedVolumeL,
			Notes:          body.Notes,
			PlannedStartAt: body.PlannedStartAt,
			PlannedEndAt:   body.PlannedEndAt,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, run)
	})

	mux.HandleFunc("GET /v1/runs", func(response http.ResponseWriter, request *http.Request) {
		query, err := runListQuery(request)
		if !handleDecode(response, err) {
			return
		}
		runs, err := app.ListRuns(query)
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"runs": runs})
	})

	mux.HandleFunc("GET /v1/runs/{id}", func(response http.ResponseWriter, request *http.Request) {
		run, err := app.GetRun(request.PathValue("id"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, run)
	})

	mux.HandleFunc("POST /v1/runs/{id}/reserve", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[reserveRunRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		run, vessel, err := app.ReserveRun(request.PathValue("id"), service.ReserveRunInput{
			ExpectedVersion: expected,
			VesselID:        body.VesselID,
			PlannedStartAt:  body.PlannedStartAt,
			PlannedEndAt:    body.PlannedEndAt,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"run": run, "vessel": vessel})
	})

	mux.HandleFunc("POST /v1/runs/{id}/start", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		run, vessel, err := app.StartRun(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"run": run, "vessel": vessel})
	})

	mux.HandleFunc("POST /v1/runs/{id}/observations", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[observationRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		run, err := app.AppendObservation(request.PathValue("id"), service.ObservationInput{
			Sequence:     body.Sequence,
			Gravity:      body.Gravity,
			TemperatureC: body.TemperatureC,
			ObservedAt:   body.ObservedAt,
			Note:         body.Note,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, run)
	})

	mux.HandleFunc("POST /v1/runs/{id}/conditioning", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		run, err := app.BeginConditioning(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, run)
	})

	mux.HandleFunc("POST /v1/runs/{id}/complete", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		run, vessel, err := app.CompleteRun(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"run": run, "vessel": vessel})
	})

	mux.HandleFunc("POST /v1/runs/{id}/abort", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		run, vessel, err := app.AbortRun(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"run": run, "vessel": vessel})
	})
}
