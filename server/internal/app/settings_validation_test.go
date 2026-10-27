package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPutSettingRejectsInvalidValuesBeforeDatabase(t *testing.T) {
	invalid := map[string][]string{
		"lock_ttl_hours":         {"0", "-1", "8761", "1.5", `"24"`, "true"},
		"max_hold_per_user":      {"0", "-1", "10001", "1.5"},
		"claim_default_count":    {"0", "-1", "10001", "1.5"},
		"max_claim_count":        {"0", "-1", "10001", "1.5"},
		"max_upload_files":       {"0", "-1", "1001", "1.5"},
		"max_file_mb":            {"-1", "1025", "1.5", `"10"`},
		"auto_next_after_submit": {"0", "1", `"yes"`, `"false"`, "[]"},
		"comment_templates":      {"[1,2,3]", `[{"x":1}]`, `[null]`, `[" "]`, `["ok",{}]`},
		"reject_reason_tags":     {"[1]", `[{}]`, `[null]`, `[""]`},
		"exception_reasons":      {"[1,2,3]", `[{"x":1}]`, `[null]`, `["blur"]`, `[{"key":"blur"}]`, `[{"Key":"blur","label":"模糊"}]`, `[{"key":1,"label":"模糊"}]`, `[{"key":" blur","label":"模糊"}]`, `[{"key":" ","label":"模糊"}]`, `[{"key":"blur","label":" "}]`, `[{"key":"blur","label":"模糊","extra":1}]`, `[{"key":"blur","label":"模糊"},{"key":"blur","label":"重复"}]`},
	}
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	for key, values := range invalid {
		values = append(values, `{"value":[]}`, `{"a":1}`, `"yes"`, "null")
		for _, raw := range values {
			t.Run(key+"/"+raw, func(t *testing.T) {
				// No DB is configured: invalid input must return before any persistence.
				s := &Server{}
				router := gin.New()
				router.Use(gin.RecoveryWithWriter(io.Discard))
				router.PUT("/settings/:key", s.handlePutSetting)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("PUT", "/settings/"+key, strings.NewReader(raw)))
				if w.Code != 422 {
					t.Fatalf("status=%d body=%s, want 422", w.Code, w.Body)
				}
				var response struct {
					Error struct {
						Fields map[string]string `json:"fields"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error.Fields[key] == "" {
					t.Fatalf("missing field error: %s", w.Body)
				}
			})
		}
	}
}

func TestPutSettingRejectsUnknownKeyAndMalformedJSON(t *testing.T) {
	s := &Server{}
	router := gin.New()
	router.PUT("/settings/:key", s.handlePutSetting)
	for _, tc := range []struct {
		key, body string
		status    int
	}{{"unknown", "[]", 422}, {"comment_templates", "[abc", 400}, {"comment_templates", "", 400}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("PUT", "/settings/"+tc.key, strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Errorf("%s %q: status=%d, want %d", tc.key, tc.body, w.Code, tc.status)
		}
	}
	if validateSetting("unknown", []byte("[]")) == nil {
		t.Fatal("unknown setting accepted")
	}
}

func TestSettingValidationAcceptsBoundaries(t *testing.T) {
	valid := map[string][]string{
		"lock_ttl_hours":         {"1", "8760"},
		"max_hold_per_user":      {"1", "10000"},
		"claim_default_count":    {"1", "10000"},
		"max_claim_count":        {"1", "10000"},
		"max_upload_files":       {"1", "1000"},
		"max_file_mb":            {"0", "1024"},
		"auto_next_after_submit": {"true", "false"},
		"comment_templates":      {"[]", `["评语"]`},
		"reject_reason_tags":     {"[]", `["分数偏高"]`},
		"exception_reasons":      {"[]", `[{"key":"blur","label":"模糊"}]`},
	}
	for key, values := range valid {
		for _, raw := range values {
			t.Run(fmt.Sprintf("%s/%s", key, raw), func(t *testing.T) {
				if err := validateSetting(key, []byte(raw)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
