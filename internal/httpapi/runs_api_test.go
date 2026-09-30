package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

type apiFixture struct {
	server  *httptest.Server
	service *service.Service
	clock   *clock.Fixed
	formula domain.Formula
	vessel  domain.Vessel
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "cellar-api-*")
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
	svc := service.New(store, fixedClock)

	formula, err := svc.CreateFormula(service.CreateFormulaInput{
		Name:                  "API Test Ale",
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
	if _, err := svc.SetFormulaSteps(formula.ID, service.SetFormulaStepsInput{Steps: steps}); err != nil {
		t.Fatalf("set steps: %v", err)
	}
	if _, err := svc.ApproveFormula(formula.ID, service.VersionInput{}); err != nil {
		t.Fatalf("approve formula: %v", err)
	}

	vessel, err := svc.CreateVessel(service.CreateVesselInput{
		Code:      "VSL-API-01",
		CapacityL: 500,
	})
	if err != nil {
		t.Fatalf("create vessel: %v", err)
	}

	server := httptest.NewServer(NewHandler(svc))
	t.Cleanup(server.Close)

	return &apiFixture{
		server:  server,
		service: svc,
		clock:   fixedClock,
		formula: formula,
		vessel:  vessel,
	}
}

func (f *apiFixture) request(
	t *testing.T,
	method, path string,
	body any,
	expectedVersion *int64,
) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if expectedVersion != nil {
		req.Header.Set("X-Expected-Version", strconv.FormatInt(*expectedVersion, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, decoded
}

func TestListRunsTimeRangeQuery(t *testing.T) {
	f := newAPIFixture(t)
	base := f.clock.Now()

	nearStart := base.Add(24 * time.Hour)
	nearEnd := nearStart.Add(48 * time.Hour)
	farStart := base.Add(30 * 24 * time.Hour)
	farEnd := farStart.Add(48 * time.Hour)

	createRun := func(code string, start, end time.Time) string {
		t.Helper()
		status, body := f.request(t, "POST", "/v1/runs", map[string]any{
			"code":               code,
			"formula_id":         f.formula.ID,
			"planned_volume_liters": 200,
			"planned_start_at":   start.Format(time.RFC3339),
			"planned_end_at":     end.Format(time.RFC3339),
		}, nil)
		if status != http.StatusCreated {
			t.Fatalf("create run %s: status %d, body %v", code, status, body)
		}
		return body["id"].(string)
	}

	nearID := createRun("RUN-API-0001", nearStart, nearEnd)
	createRun("RUN-API-0002", farStart, farEnd)

	t.Run("filter returns intersecting runs", func(t *testing.T) {
		from := base.Format(time.RFC3339)
		to := base.Add(7 * 24 * time.Hour).Format(time.RFC3339)
		status, body := f.request(t, "GET", "/v1/runs?from="+from+"&to="+to, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		runs := body["runs"].([]any)
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1", len(runs))
		}
		run := runs[0].(map[string]any)
		if run["id"].(string) != nearID {
			t.Fatalf("got run %s, want near", run["id"])
		}
	})

	t.Run("invalid from returns 400", func(t *testing.T) {
		status, _ := f.request(t, "GET", "/v1/runs?from=not-a-timestamp", nil, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
	})

	t.Run("no filter returns all runs", func(t *testing.T) {
		status, body := f.request(t, "GET", "/v1/runs", nil, nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		runs := body["runs"].([]any)
		if len(runs) != 2 {
			t.Fatalf("got %d runs, want 2", len(runs))
		}
	})
}

func TestReserveConflictReturns409(t *testing.T) {
	f := newAPIFixture(t)
	base := f.clock.Now()

	start := base.Add(24 * time.Hour)
	end := start.Add(48 * time.Hour)

	createRun := func(code string) string {
		t.Helper()
		status, body := f.request(t, "POST", "/v1/runs", map[string]any{
			"code":               code,
			"formula_id":         f.formula.ID,
			"planned_volume_liters": 200,
			"planned_start_at":   start.Format(time.RFC3339),
			"planned_end_at":     end.Format(time.RFC3339),
		}, nil)
		if status != http.StatusCreated {
			t.Fatalf("create run: status %d, body %v", status, body)
		}
		return body["id"].(string)
	}

	runA := createRun("RUN-API-0003")
	runB := createRun("RUN-API-0004")

	// Reserve A.
	status, _ := f.request(t, "POST", "/v1/runs/"+runA+"/reserve", map[string]any{
		"vessel_id": f.vessel.ID,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("reserve A: status %d", status)
	}

	// Reserve B with overlapping window → 409.
	status, body := f.request(t, "POST", "/v1/runs/"+runB+"/reserve", map[string]any{
		"vessel_id": f.vessel.ID,
	}, nil)
	if status != http.StatusConflict {
		t.Fatalf("reserve B: status = %d, want 409", status)
	}
	errBody := body["error"].(map[string]any)
	if errBody["code"].(string) != string(domain.CodeScheduleConflict) {
		t.Fatalf("error code = %s, want schedule_conflict", errBody["code"])
	}

	// B must remain planned.
	status, body = f.request(t, "GET", "/v1/runs/"+runB, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("get B: status %d", status)
	}
	if body["state"].(string) != string(domain.RunPlanned) {
		t.Fatalf("B state = %s, want planned", body["state"])
	}
}
