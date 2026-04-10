//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/handler"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// performRequest executes an HTTP request against the test router.
func performRequest(router *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// parseJSON decodes the response body into the target struct.
func parseJSON(t *testing.T, w *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("failed to parse response JSON: %v\nbody: %s", err, w.Body.String())
	}
}

// apiResponse is a generic response for assertions.
type apiResponse struct {
	Success bool              `json:"success"`
	Data    json.RawMessage   `json:"data,omitempty"`
	Error   *handler.APIError `json:"error,omitempty"`
	Meta    *handler.APIMeta  `json:"meta,omitempty"`
}

// parseAPIResponse parses the standard API response envelope.
func parseAPIResponse(t *testing.T, w *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var resp apiResponse
	parseJSON(t, w, &resp)
	return resp
}

// assertSuccess asserts the response is a success with the expected status code.
func assertSuccess(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int) apiResponse {
	t.Helper()
	if w.Code != expectedStatus {
		t.Errorf("expected status %d, got %d\nbody: %s", expectedStatus, w.Code, w.Body.String())
	}
	resp := parseAPIResponse(t, w)
	if !resp.Success {
		t.Errorf("expected success=true, got false\nerror: %+v", resp.Error)
	}
	return resp
}

// assertError asserts the response is an error with the expected status and code.
func assertError(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int, expectedCode string) apiResponse {
	t.Helper()
	if w.Code != expectedStatus {
		t.Errorf("expected status %d, got %d\nbody: %s", expectedStatus, w.Code, w.Body.String())
	}
	resp := parseAPIResponse(t, w)
	if resp.Success {
		t.Error("expected success=false, got true")
	}
	if resp.Error == nil {
		t.Fatal("expected error object, got nil")
	}
	if resp.Error.Code != expectedCode {
		t.Errorf("expected error code %q, got %q", expectedCode, resp.Error.Code)
	}
	return resp
}

// dataAs unmarshals the Data field of an apiResponse into the target.
func dataAs(t *testing.T, resp apiResponse, target interface{}) {
	t.Helper()
	if resp.Data == nil {
		t.Fatal("response data is nil")
	}
	if err := json.Unmarshal(resp.Data, target); err != nil {
		t.Fatalf("failed to unmarshal response data: %v\nraw: %s", err, string(resp.Data))
	}
}

// createTestDeviceInDB creates an adopted device directly in the database.
func createTestDeviceInDB(t *testing.T, store *pgstore.Store, tenantID, siteID uuid.UUID) *model.Device {
	t.Helper()
	ctx := context.Background()

	deviceID := uuid.New()
	mac := fmt.Sprintf("AA:BB:CC:%02X:%02X:%02X",
		time.Now().UnixNano()%256,
		(time.Now().UnixNano()/256)%256,
		(time.Now().UnixNano()/65536)%256,
	)

	_, err := store.Pool.Exec(ctx,
		`INSERT INTO devices (id, tenant_id, site_id, mac, serial, name, model, status,
			firmware_version, capabilities, system_info, adopted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'online', '1.0.0',
			'{"bands":["2g","5g"],"max_ssids":16,"wpa3":true,"vlan":true}', '{}', NOW())`,
		deviceID, tenantID, siteID, mac,
		"SN-"+uuid.New().String()[:8],
		"test-ap-"+uuid.New().String()[:8],
		"AP-TEST-01",
	)
	if err != nil {
		t.Fatalf("create test device: %v", err)
	}

	device, err := store.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		t.Fatalf("retrieve test device: %v", err)
	}
	return device
}

// newTestLogger creates a logger for tests.
func newTestLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}
