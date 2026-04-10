package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yourorg/cloudctrl/internal/api/handler"
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
