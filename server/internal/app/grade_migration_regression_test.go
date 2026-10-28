package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	dbmigrations "github.com/shangyue/font-label/server/db"
	"github.com/xuri/excelize/v2"
)

func TestStoredGradeCorrectionMigration(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	rubric, err := s.activeRubric(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// An already-deployed V4 database has old grades/snapshots but current bands.
	snapshot := rubric.Snapshot()
	snapshot["grade_bands"] = []map[string]any{{"min": 90, "label": "优秀"}, {"min": 80, "label": "良好"}, {"min": 60, "label": "中等"}, {"min": 0, "label": "较差"}}
	snapshotJSON, _ := json.Marshal(snapshot)
	type fixture struct {
		id, folder int64
		total      float64
		want       string
		status     string
	}
	var fixtures []fixture
	statuses := []string{"approved", "submitted", "rejected", "assigned"}
	for n, tc := range []struct {
		total float64
		want  string
	}{{40, "需加油"}, {0, "需加油"}, {59.9, "需加油"}, {60, "及格"}, {65, "及格"}, {69.9, "及格"}, {70, "中等"}, {79.9, "中等"}, {80, "良好"}, {89.9, "良好"}, {90, "优秀"}, {100, "优秀"}} {
		fid, id := regressionImage(t, s, statuses[n%len(statuses)])
		storedSnapshot := snapshotJSON
		switch n % 4 {
		case 1: // Missing historical snapshot falls back to the linked rubric.
			storedSnapshot = nil
		case 2: // Equivalent bands may have been saved in a different order.
			storedSnapshot = []byte(`{"grade_bands":[{"label":"较差","min":0},{"label":"中等","min":60},{"label":"良好","min":80},{"label":"优秀","min":90}]}`)
		case 3: // Already using table5, but the saved grade is stale.
			storedSnapshot, _ = json.Marshal(rubric.Snapshot())
		}
		// Do not derive total from scores: historical weights/overrides must survive.
		if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,version,kind,scores,total,grade,comment,rubric_id,rubric_snapshot,dim_comments)
		 VALUES($1,1,2,'score','{"white":10,"black":10,"variation":10,"structure":5,"control":5}', $2,'较差','原点评',$3,$4::jsonb,'{"white":"原维度点评"}')`, id, tc.total, rubric.ID, storedSnapshot); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool().Exec(ctx, `INSERT INTO annotation_versions(image_id,annotator_id,version,payload) VALUES($1,1,1,'{"total":40,"grade":"较差"}'),($1,1,2,'{"total":65,"grade":"中等"}')`, id); err != nil {
			t.Fatal(err)
		}
		status := statuses[n%len(statuses)]
		if status == "assigned" {
			status = "active"
		}
		fixtures = append(fixtures, fixture{id, fid, tc.total, tc.want, status})
	}
	_, exceptionID := regressionImage(t, s, "excluded")
	_, noTotalID := regressionImage(t, s, "submitted")
	if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind,total,grade,rubric_id) VALUES($1,1,'exception',0,'',$3),($2,1,'score',NULL,NULL,$3)`, exceptionID, noTotalID, rubric.ID); err != nil {
		t.Fatal(err)
	}
	// Admin-defined bands must not be replaced by this standard-band correction.
	customBands := `[{"min":80,"label":"达标"},{"min":0,"label":"待提升"}]`
	var customRubricID int64
	if err := s.pool().QueryRow(ctx, `INSERT INTO rubrics(name,dims,grade_bands) SELECT '自定义方案',dims,$1::jsonb FROM rubrics WHERE is_active RETURNING id`, customBands).Scan(&customRubricID); err != nil {
		t.Fatal(err)
	}
	var customIDs []int64
	for _, withSnapshot := range []bool{true, false} {
		_, id := regressionImage(t, s, "approved")
		var customSnapshot any
		if withSnapshot {
			customSnapshot = `{"grade_bands":` + customBands + `}`
		}
		if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind,total,grade,rubric_id,rubric_snapshot) VALUES($1,1,'score',85,'达标',$2,$3::jsonb)`, id, customRubricID, customSnapshot); err != nil {
			t.Fatal(err)
		}
		customIDs = append(customIDs, id)
	}
	// Capture full rows so the migration cannot silently change scores, comments,
	// snapshots, submission versions, image status or review history.
	state := func() string {
		t.Helper()
		var raw string
		err := s.pool().QueryRow(ctx, `SELECT jsonb_build_object(
		 'annotations',(SELECT jsonb_agg(to_jsonb(a)-'grade'-'updated_at' ORDER BY id) FROM annotations a),
		 'images',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM images i),
		 'versions',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM annotation_versions v),
		 'reviews',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM reviews r),
		 'rubrics',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM rubrics r))::text`).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := state()
	raw, err := dbmigrations.FS.ReadFile(dbmigrations.Dir + "/00009_correct_stored_grades.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		if _, err := s.pool().Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
			t.Fatal(err)
		}
		if state() != before {
			t.Fatal("grade correction rewrote scores, snapshots, workflow state or history")
		}
	}
	for _, id := range customIDs {
		var grade string
		if err := s.pool().QueryRow(ctx, `SELECT grade FROM annotations WHERE image_id=$1`, id).Scan(&grade); err != nil || grade != "达标" {
			t.Fatalf("custom rubric grade was overwritten: %q (%v)", grade, err)
		}
	}
	for _, f := range fixtures {
		var total float64
		var grade string
		if err := s.pool().QueryRow(ctx, `SELECT total,grade FROM annotations WHERE image_id=$1`, f.id).Scan(&total, &grade); err != nil {
			t.Fatal(err)
		}
		if total != f.total || grade != f.want {
			t.Errorf("stored total=%v grade=%s; want %v %s", total, grade, f.total, f.want)
		}
		for _, path := range []string{fmt.Sprintf("/images/%d", f.id), fmt.Sprintf("/images?folder_id=%d", f.folder), "/claims/my?status=" + f.status} {
			w := regressionRequest(t, router, token, "GET", path, nil)
			var result struct {
				Data struct {
					Image ImageDTO
					Items []ImageDTO
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
				t.Fatalf("%s: %d %s (%v)", path, w.Code, w.Body, err)
			}
			image := result.Data.Image
			for _, item := range result.Data.Items {
				if item.ID == f.id {
					image = item
				}
			}
			if image.ID != f.id || image.Total == nil || *image.Total != f.total || image.Grade != f.want {
				t.Fatalf("%s: total/grade differs from stored record: %+v", path, image)
			}
		}
	}
	var untouched bool
	if err := s.pool().QueryRow(ctx, `SELECT (SELECT grade='' FROM annotations WHERE image_id=$1) AND (SELECT grade IS NULL FROM annotations WHERE image_id=$2)`, exceptionID, noTotalID).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("exception/no-total grades were modified: %v", err)
	}
	// The reported approved 40-point record must agree in every download format.
	f := fixtures[0]
	for _, format := range []string{"json", "csv", "xlsx"} {
		w := regressionRequest(t, router, token, "POST", "/exports", map[string]any{"format": format, "filters": map[string]any{"folders": []int64{f.folder}}})
		if w.Code != 200 {
			t.Fatalf("%s export: %d %s", format, w.Code, w.Body)
		}
		if format == "json" {
			var rows []struct {
				Total float64
				Grade string
			}
			if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Total != 40 || rows[0].Grade != f.want {
				t.Fatalf("JSON grade mismatch: %s (%v)", w.Body, err)
			}
			continue
		}
		var rows [][]string
		if format == "csv" {
			rows, err = csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
		} else {
			var book *excelize.File
			book, err = excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
			if err == nil {
				rows, err = book.GetRows(book.GetSheetName(0))
				book.Close()
			}
		}
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s export rows=%v: %v", format, rows, err)
		}
		for _, column := range []struct{ name, want string }{{"总分", "40.0"}, {"等级", f.want}} {
			found := false
			for i, header := range rows[0] {
				if header == column.name {
					found = true
					if rows[1][i] != column.want {
						t.Errorf("%s %s=%s want %s", format, column.name, rows[1][i], column.want)
					}
				}
			}
			if !found {
				t.Errorf("%s missing column %s", format, column.name)
			}
		}
	}
}
