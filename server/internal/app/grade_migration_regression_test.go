package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dbmigrations "github.com/shangyue/font-label/server/db"
	"github.com/shangyue/font-label/server/internal/platform/score"
)

func ratingTestDims() []score.Dim {
	return []score.Dim{{Key: "white", Label: "白平衡（匀）"}, {Key: "black", Label: "黑平衡（稳）"},
		{Key: "variation", Label: "参差"}, {Key: "structure", Label: "结构"}, {Key: "control", Label: "控笔"}}
}
func ratingTestGrades() map[string]string {
	return map[string]string{"white": "S", "black": "A", "variation": "B", "structure": "C", "control": "D"}
}

func TestLetterRatingsMigrateHistoricalData(t *testing.T) {
	const migration = "00013_letter_ratings.sql"
	s, _, _ := regressionServerBefore(t, migration)
	ctx := context.Background()
	_, id := regressionImage(t, s, "approved")
	for _, statement := range []string{
		`INSERT INTO annotations (image_id,annotator_id,kind,scores,total,grade,dim_comments,rubric_snapshot)
		 VALUES ($1,1,'score','{"white":8,"black":15}',75,'中等','{"white":"历史维度评语"}','{"dims":[{"key":"white","max":10}]}')`,
		`INSERT INTO annotation_versions (image_id,annotator_id,version,payload)
		 VALUES ($1,1,1,'{"kind":"score","scores":{"white":8,"black":15},"total":75,"grade":"中等","dim_comments":{"white":"历史维度评语"}}')`,
		`INSERT INTO drafts (user_id,image_id,payload) VALUES (1,$1,'{"kind":"score","scores":{"control":20}}')`,
		`INSERT INTO reviews (image_id,annotation_id,annotation_version,reviewer_id,action,score_patch)
		 SELECT $1,id,1,1,'approve','{"white":10}' FROM annotations WHERE image_id=$1`,
	} {
		if _, err := s.pool().Exec(ctx, statement, id); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := dbmigrations.FS.ReadFile(dbmigrations.Dir + "/" + migration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool().Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var grades, patch, draft map[string]string
	var version, comments []byte
	if err := s.pool().QueryRow(ctx, `
		SELECT a.grades,a.dim_comments,r.grade_patch,v.payload,d.payload->'grades'
		FROM annotations a JOIN reviews r ON r.annotation_id=a.id
		JOIN annotation_versions v ON v.image_id=a.image_id JOIN drafts d ON d.image_id=a.image_id
		WHERE a.image_id=$1`, id).Scan(&grades, &comments, &patch, &version, &draft); err != nil {
		t.Fatal(err)
	}
	if grades["white"] != "A" || grades["black"] != "A" || patch["white"] != "S" || draft["control"] != "S" {
		t.Fatalf("grades=%v patch=%v draft=%v", grades, patch, draft)
	}
	var payload struct {
		Grades   map[string]string `json:"grades"`
		Comments map[string]string `json:"dim_comments"`
	}
	if err := json.Unmarshal(version, &payload); err != nil || payload.Grades["white"] != "A" ||
		payload.Comments["white"] != "历史维度评语" || !strings.Contains(string(comments), "历史维度评语") || strings.Contains(string(version), `"total"`) {
		t.Fatalf("history: %s, %s (%v)", version, comments, err)
	}
}
