package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	dbmigrations "github.com/shangyue/font-label/server/db"
)

func TestReviewerScopeAndVersionedFeedback(t *testing.T) {
	s, router, admin := regressionServer(t)
	ctx := context.Background()
	a, annotator := statsUser(t, s, "annotator", "review-v2-a")
	r1, reviewer1 := statsUser(t, s, "reviewer", "review-v2-r1")
	r2, reviewer2 := statsUser(t, s, "reviewer", "review-v2-r2")
	req := func(token, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		res := regressionRequest(t, router, token, method, path, body)
		if res.Code != want {
			t.Fatalf("%s %s = %d: %s", method, path, res.Code, res.Body)
		}
		return res
	}
	seed := func(group int, reviewer int64, count int) []int64 {
		t.Helper()
		req(admin, "POST", "/groups", map[string]any{"name": fmt.Sprint(group), "member_ids": []int64{a, reviewer}}, 200)
		var sources []int64
		for range count {
			_, id := regressionImage(t, s, "unassigned")
			sources = append(sources, id)
		}
		req(admin, "POST", "/groups/dispatch", map[string]any{"group_ids": []int{group}, "image_ids": sources}, 200)
		var ids []int64
		if err := s.pool().QueryRow(ctx, `SELECT array_agg(id ORDER BY id) FROM images WHERE group_id=$1`, group).Scan(&ids); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			req(annotator, "POST", fmt.Sprintf("/images/%d/submit", id), alignmentScore(20, 15, "当前测试使用的五维具体评语"), 200)
		}
		return ids
	}
	id := seed(1, r1, 1)[0]
	seed(2, r2, 2)
	for _, tc := range []struct {
		token   string
		pending int
	}{{reviewer1, 1}, {reviewer2, 2}} {
		stats := req(tc.token, "GET", "/stats/review", nil, 200)
		queue := req(tc.token, "GET", "/reviews/queue", nil, 200)
		if !strings.Contains(stats.Body.String(), fmt.Sprintf(`"pending":%d`, tc.pending)) ||
			!strings.Contains(queue.Body.String(), fmt.Sprintf(`"total":%d`, tc.pending)) {
			t.Fatalf("scope differs from queue: %s / %s", stats.Body, queue.Body)
		}
	}
	path := fmt.Sprintf("/images/%d", id)
	req(reviewer1, "POST", path+"/review", map[string]any{"action": "reject", "comment": "这是第一版的驳回原因"}, 200)
	var pastReview int64
	if err := s.pool().QueryRow(ctx, `SELECT id FROM reviews WHERE image_id=$1`, id).Scan(&pastReview); err != nil {
		t.Fatal(err)
	}
	req(annotator, "POST", path+"/submit", alignmentScore(15, 15, "第二版标注使用的五维评语"), 200)
	seen := req(reviewer1, "GET", "/reviews/queue?status=reviewed", nil, 200)
	var queue struct {
		Data struct {
			Items []struct {
				CanReview bool              `json:"can_review"`
				ReviewID  int64             `json:"review_id"`
				Grades    map[string]string `json:"grades"`
			}
			Total int
		}
	}
	if err := json.Unmarshal(seen.Body.Bytes(), &queue); err != nil || queue.Data.Total != 1 ||
		len(queue.Data.Items) != 1 || queue.Data.Items[0].CanReview ||
		queue.Data.Items[0].ReviewID != pastReview || queue.Data.Items[0].Grades["white"] != "S" {
		t.Fatalf("reviewed list did not preserve the historical snapshot: %s (%v)", seen.Body, err)
	}
	snapshot := req(reviewer1, "GET", fmt.Sprintf("%s?review_id=%d", path, pastReview), nil, 200).Body.String()
	if !strings.Contains(snapshot, "这是第一版的驳回原因") || !strings.Contains(snapshot, `"status":"rejected"`) ||
		strings.Contains(snapshot, "第二版标注使用的五维评语") {
		t.Fatalf("unexpected review history: %s", snapshot)
	}
	req(reviewer2, "GET", fmt.Sprintf("%s?review_id=%d", path, pastReview), nil, 403)
	req(admin, "PUT", "/quality-policy", map[string]int{"sample_rate_bps": 0}, 200)
	req(reviewer1, "POST", path+"/review", map[string]any{"action": "approve", "grade_patch": map[string]string{"white": "B"}}, 200)
	detail := req(annotator, "GET", path, nil, 200).Body.String()
	if strings.Contains(detail, "这是第一版的驳回原因") || !strings.Contains(detail, `"white":{"from":"A","to":"B"}`) {
		t.Fatalf("expected only current version and review correction: %s", detail)
	}
	summary := req(reviewer1, "GET", "/stats/review", nil, 200).Body.String()
	if !strings.Contains(summary, `"approved":1`) || !strings.Contains(summary, `"pending":0`) || !strings.Contains(summary, `"rejected":0`) {
		t.Fatalf("unexpected reviewer totals: %s", summary)
	}
}

func TestUpgradeReleasesLegacySampledRejections(t *testing.T) {
	const migration = "00016_approved_quality_only.sql"
	s, router, admin := regressionServerBefore(t, migration)
	ctx := context.Background()
	_, inspector := statsUser(t, s, "quality", "legacy-reject-inspector")
	_, id := regressionImage(t, s, "rejected")
	var sampleID int64
	if err := s.pool().QueryRow(ctx, `
		WITH updated AS (
			UPDATE images SET quality_pending=true WHERE id=$1 RETURNING id
		), ann AS (
			INSERT INTO annotations (image_id,annotator_id,kind,grades)
			SELECT id,1,'score','{}' FROM updated RETURNING id,image_id
		), review AS (
			INSERT INTO reviews (image_id,annotation_id,annotation_version,reviewer_id,action,comment)
			SELECT image_id,id,1,1,'reject','历史审核驳回' FROM ann RETURNING id,image_id
		)
		INSERT INTO quality_samples (review_id,image_id,sample_order,inspector_id)
		SELECT id,image_id,1,(SELECT id FROM users WHERE username='legacy-reject-inspector')
		FROM review RETURNING id`, id).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	raw, err := dbmigrations.FS.ReadFile(dbmigrations.Dir + "/" + migration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool().Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	res := regressionRequest(t, router, inspector, "POST", fmt.Sprintf("/quality/samples/%d", sampleID), map[string]string{"result": "pass"})
	if res.Code != 404 {
		t.Fatalf("legacy sample still actionable: %d %s", res.Code, res.Body)
	}
	res = regressionRequest(t, router, admin, "POST", fmt.Sprintf("/images/%d/submit", id), alignmentScore(15, 15, "历史被驳回的图片现在可以重新提交"))
	if res.Code != 200 {
		t.Fatalf("legacy rejected task still blocked: %d %s", res.Code, res.Body)
	}
}
