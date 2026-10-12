package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infoex.local/run/internal/jobs"
)

func TestTenantAndIdempotency(t *testing.T) {
	store, err := jobs.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("x", 32)
	handler := (&API{Store: store, Token: token}).Handler()
	send := func(method, path, tenant, key, body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("X-Tenant-ID", tenant)
		req.Header.Set("Idempotency-Key", key)
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	body := `{"code":"print('ok')","inputs":[],"timeout_seconds":20}`
	if res := send("POST", "/v1/jobs", "oza", "generation-1", body, false); res.Code != 401 {
		t.Fatal(res.Code)
	}
	res := send("POST", "/v1/jobs", "oza", "generation-1", body, true)
	if res.Code != 202 {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	var value struct {
		ID string `json:"id"`
	}
	json.Unmarshal(res.Body.Bytes(), &value)
	if strings.Contains(res.Body.String(), "print") {
		t.Fatal("code leaked")
	}
	if res = send("GET", "/v1/jobs/"+value.ID, "qualita", "", "", true); res.Code != 404 {
		t.Fatal("cross tenant", res.Code)
	}
	if res = send("POST", "/v1/jobs", "oza", "generation-1", body, true); res.Code != 200 {
		t.Fatal("retry", res.Code)
	}
	if res = send("POST", "/v1/jobs", "oza", "generation-1", `{"code":"print('different')"}`, true); res.Code != 409 {
		t.Fatal("conflict", res.Code)
	}
	if res = send("POST", "/v1/jobs", "oza", "generation-2", body+` {}`, true); res.Code != 400 {
		t.Fatal("extra body", res.Code)
	}
	if res = send("POST", "/v1/jobs/"+value.ID+"/cancel", "oza", "", "", true); res.Code != 200 {
		t.Fatal(res.Code)
	}
	if res = send("DELETE", "/v1/jobs/"+value.ID, "oza", "", "", true); res.Code != http.StatusNoContent {
		t.Fatal(res.Code)
	}
}
