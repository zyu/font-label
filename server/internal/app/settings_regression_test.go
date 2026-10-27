package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dbmigrations "github.com/shangyue/font-label/server/db"
)

func TestSettingsValidationPreservesStoredValue(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	values := map[string]any{
		"lock_ttl_hours": 48, "max_hold_per_user": 50, "claim_default_count": 5,
		"max_claim_count": 20, "max_upload_files": 100, "max_file_mb": 0,
		"auto_next_after_submit": false,
		"comment_templates":      []string{"有效评语"}, "reject_reason_tags": []string{"分数偏高"},
		"exception_reasons": []map[string]string{{"key": "blur", "label": "模糊"}},
	}
	for key, value := range values {
		t.Run(key, func(t *testing.T) {
			w := regressionRequest(t, router, token, "PUT", "/settings/"+key, value)
			if w.Code != 200 {
				t.Fatalf("valid value rejected: %s", w.Body)
			}
			var before string
			if err := s.pool().QueryRow(ctx, `SELECT value::text FROM settings WHERE key=$1`, key).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []any{map[string]any{"value": value}, map[string]int{"a": 1}, []int{1, 2, 3}, []map[string]int{{"x": 1}}, "yes", nil} {
				w = regressionRequest(t, router, token, "PUT", "/settings/"+key, bad)
				if w.Code != 422 {
					t.Fatalf("invalid value accepted: %s", w.Body)
				}
				var after string
				if err := s.pool().QueryRow(ctx, `SELECT value::text FROM settings WHERE key=$1`, key).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if after != before {
					t.Fatalf("invalid write changed stored value: %s -> %s", before, after)
				}
			}
		})
	}
	w := regressionRequest(t, router, token, "GET", "/auth/me", nil)
	if w.Code != 200 {
		t.Fatalf("auth/me: %s", w.Body)
	}
	var response struct {
		Data struct {
			Limits struct {
				AutoNext bool `json:"auto_next"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Limits.AutoNext {
		t.Fatalf("false boolean ignored: %s", w.Body)
	}
	w = regressionRequest(t, router, token, "PUT", "/settings/auto_next_after_submit", true)
	if w.Code != 200 {
		t.Fatalf("true boolean rejected: %s", w.Body)
	}
	if !s.settingBool(ctx, "auto_next_after_submit", false) {
		t.Fatal("true boolean ignored")
	}
	// Legacy malformed values must not escape through configuration consumers.
	if _, err := s.pool().Exec(ctx, `UPDATE settings SET value='{"value":[]}'::jsonb WHERE key IN ('comment_templates','exception_reasons','reject_reason_tags','lock_ttl_hours')`); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"comment_templates", "exception_reasons", "reject_reason_tags"} {
		if s.settingValue(ctx, key) != nil {
			t.Fatalf("invalid %s leaked", key)
		}
	}
	if got := s.settingInt(ctx, "lock_ttl_hours", 24); got != 24 {
		t.Fatalf("integer fallback=%d", got)
	}
	if _, err := s.pool().Exec(ctx, `UPDATE settings SET value='0'::jsonb WHERE key='lock_ttl_hours'`); err != nil {
		t.Fatal(err)
	}
	if got := s.settingInt(ctx, "lock_ttl_hours", 24); got != 24 {
		t.Fatalf("out-of-range fallback=%d", got)
	}
}

func TestTableFiveGradeBandsMigration(t *testing.T) {
	s, _, _ := regressionServer(t)
	ctx := context.Background()
	// Exercise an upgrade of an already customized active rubric.
	oldBands := `[{"label":"优秀","min":90},{"label":"良好","min":80},{"label":"中等","min":60},{"label":"较差","min":0}]`
	if _, err := s.pool().Exec(ctx, `UPDATE rubrics SET grade_bands=$1::jsonb WHERE is_active`, oldBands); err != nil {
		t.Fatal(err)
	}
	raw, err := dbmigrations.FS.ReadFile(dbmigrations.Dir + "/00008_table5_grade_bands.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool().Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	rubric, err := s.activeRubric(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rubric.Bands) != 5 {
		t.Fatalf("bands=%+v", rubric.Bands)
	}
	for _, tc := range []struct {
		total float64
		want  string
	}{{0, "需加油"}, {50, "需加油"}, {59.9, "需加油"}, {60, "及格"}, {69.9, "及格"}, {70, "中等"}, {79.9, "中等"}, {80, "良好"}, {89.9, "良好"}, {90, "优秀"}, {100, "优秀"}} {
		if got := rubric.Grade(tc.total); got != tc.want {
			t.Errorf("grade(%v)=%q want %q", tc.total, got, tc.want)
		}
	}
	total, grade := rubric.Total(map[string]int{"white": 10, "black": 10, "variation": 10, "structure": 15, "control": 5})
	if total != 50 || grade != "需加油" {
		t.Fatalf("reported example: total=%v grade=%s", total, grade)
	}
}
