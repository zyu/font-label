package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGroupDispatchAndQualityWorkflow(t *testing.T) {
	s, router, admin := regressionServer(t)
	ctx := context.Background()
	a1, annotator := statsUser(t, s, "annotator", "group-a1")
	a2, annotator2 := statsUser(t, s, "annotator", "group-a2")
	r1, reviewer := statsUser(t, s, "reviewer", "group-r1")
	r2, otherReviewer := statsUser(t, s, "reviewer", "group-r2")
	q1, quality := statsUser(t, s, "quality", "group-q1")
	q2, otherQuality := statsUser(t, s, "quality", "group-q2")

	request := func(token, method, path string, body any, want int) {
		t.Helper()
		res := regressionRequest(t, router, token, method, path, body)
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, want, res.Body)
		}
	}
	for _, users := range [][]int64{{a1, a2, r1}, {a2, r2}} {
		request(admin, http.MethodPost, "/groups", map[string]any{
			"name": fmt.Sprint(users[0]), "member_ids": users,
		}, 200)
	}
	request(admin, http.MethodPost, "/groups", map[string]any{"name": "invalid", "member_ids": []int64{q1}}, 400)

	var sources []int64
	for range 3 {
		_, id := regressionImage(t, s, "unassigned")
		sources = append(sources, id)
	}
	for range 2 {
		request(admin, http.MethodPost, "/groups/dispatch", map[string]any{
			"group_ids": []int64{1, 2}, "image_ids": sources,
		}, 200)
	}
	var count, first, second int
	if err := s.pool().QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE annotator_id=$1), count(*) FILTER (WHERE annotator_id=$2) FROM images WHERE group_id=1`, a1, a2).Scan(&count, &first, &second); err != nil {
		t.Fatal(err)
	}
	if count != 3 || first != 2 || second != 1 {
		t.Fatalf("group distribution: %d tasks, %d / %d", count, first, second)
	}
	var id int64
	if err := s.pool().QueryRow(ctx, `SELECT id FROM images WHERE group_id=1 AND annotator_id=$1 ORDER BY id LIMIT 1`, a1).Scan(&id); err != nil {
		t.Fatal(err)
	}
	imagePath := fmt.Sprintf("/images/%d", id)
	request(admin, http.MethodPost, "/claims", map[string]any{"mode": "count", "count": 1}, 403)
	request(annotator, http.MethodGet, fmt.Sprintf("/claims/my/%d/neighbors", id), nil, 200)
	request(annotator2, http.MethodGet, fmt.Sprintf("/claims/my/%d/neighbors", id), nil, 404)
	request(otherReviewer, http.MethodGet, imagePath, nil, 403)
	request(admin, http.MethodPut, "/quality-policy", map[string]any{"sample_rate_bps": 10000}, 200)
	request(annotator, http.MethodPost, imagePath+"/submit", map[string]any{
		"kind": "exception", "exception_reasons": []string{"blurry"}, "exception_note": "图片模糊",
	}, 200)
	request(otherReviewer, http.MethodPost, imagePath+"/review", map[string]any{"action": "approve"}, 403)
	request(reviewer, http.MethodPost, imagePath+"/review", map[string]any{"action": "approve"}, 200)
	request(admin, http.MethodGet, "/quality/disputes", nil, 200)

	var sample, inspector int64
	if err := s.pool().QueryRow(ctx, `SELECT id, inspector_id FROM quality_samples WHERE image_id=$1`, id).Scan(&sample, &inspector); err != nil {
		t.Fatal(err)
	}
	if inspector != q1 {
		t.Fatalf("first sample assigned to %d, want %d", inspector, q1)
	}
	samplePath := fmt.Sprintf("/quality/samples/%d", sample)
	request(otherQuality, http.MethodPost, samplePath, map[string]any{"result": "fail"}, 403)
	request(quality, http.MethodPost, samplePath, map[string]any{"result": "fail", "note": "质检意见"}, 200)
	request(quality, http.MethodPost, samplePath, map[string]any{"result": "fail"}, 409)

	var dispute, arbiter int64
	if err := s.pool().QueryRow(ctx, `SELECT id, arbiter_id FROM quality_disputes WHERE sample_id=$1`, sample).Scan(&dispute, &arbiter); err != nil {
		t.Fatal(err)
	}
	if arbiter != q2 {
		t.Fatalf("first dispute arbiter: %d", arbiter)
	}
	disputePath := fmt.Sprintf("/quality/disputes/%d", dispute)
	request(quality, http.MethodPost, disputePath, map[string]any{"result": "return"}, 403)
	request(otherQuality, http.MethodPost, disputePath, map[string]any{"result": "return", "note": "请重做"}, 200)

	var status string
	if err := s.pool().QueryRow(ctx, `SELECT status FROM images WHERE id=$1`, id).Scan(&status); err != nil || status != "rejected" {
		t.Fatalf("return status = %s, err = %v", status, err)
	}
	var secondID int64
	if err := s.pool().QueryRow(ctx, `SELECT id FROM images WHERE group_id=1 AND annotator_id=$1`, a2).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	secondPath := fmt.Sprintf("/images/%d", secondID)
	submission := map[string]any{"kind": "exception", "exception_reasons": []string{"blurry"}}
	request(annotator2, http.MethodPost, secondPath+"/submit", submission, 200)
	request(reviewer, http.MethodPost, secondPath+"/review", map[string]any{"action": "reject", "comment": "重新判断模糊程度"}, 200)
	var pending bool
	if err := s.pool().QueryRow(ctx, `SELECT quality_pending FROM images WHERE id=$1`, secondID).Scan(&pending); err != nil || pending {
		t.Fatalf("review rejects must be immediately editable: %v, %v", pending, err)
	}
	var samples int
	if err := s.pool().QueryRow(ctx, `SELECT count(*) FROM quality_samples WHERE image_id=$1`, secondID).Scan(&samples); err != nil || samples != 0 {
		t.Fatalf("review rejects must not be sampled: %d, %v", samples, err)
	}
	request(annotator2, http.MethodPost, secondPath+"/submit", submission, 200)
	request(reviewer, http.MethodPost, secondPath+"/review", map[string]any{"action": "approve"}, 200)
	if err := s.pool().QueryRow(ctx, `SELECT id,inspector_id FROM quality_samples WHERE image_id=$1`, secondID).Scan(&sample, &inspector); err != nil || inspector != q2 {
		t.Fatalf("second inspector: %d, err: %v", inspector, err)
	}
	request(otherQuality, http.MethodGet, "/quality/samples", nil, 200)
	request(otherQuality, http.MethodPost, fmt.Sprintf("/quality/samples/%d", sample), map[string]any{"result": "pass"}, 200)
	if err := s.pool().QueryRow(ctx, `SELECT count(*) FROM quality_disputes WHERE sample_id=$1`, sample).Scan(&samples); err != nil || samples != 0 {
		t.Fatalf("passing quality must close without arbitration: %d, %v", samples, err)
	}

	if err := s.pool().QueryRow(ctx, `SELECT status FROM images WHERE id=$1`, secondID).Scan(&status); err != nil || status != "excluded" {
		t.Fatalf("approved exception status: %s, err: %v", status, err)
	}
	items := regressionRequest(t, router, admin, http.MethodGet, "/audit-logs?action=quality.arbitrate", nil)
	if items.Code != 200 || strings.Count(items.Body.String(), `"action":"quality.arbitrate"`) != 1 {
		t.Fatalf("missing arbitration audit trail: %s", items.Body)
	}
	request(quality, http.MethodGet, "/audit-logs", nil, 403)
}

func TestWorkbookSamplingSequence(t *testing.T) {
	for _, tc := range []struct {
		number, rate int64
		order        int64
	}{
		{19, 500, 0}, {20, 500, 1}, {40, 500, 2}, {90000, 500, 4500},
		{20, 0, 0}, {3, 10000, 3}, {0, 10000, 0}, {3, 3000, 0}, {4, 3000, 1}, {7, 3000, 2},
	} {
		if got := qualitySampleOrder(tc.number, tc.rate); got != tc.order {
			t.Errorf("sample %d at %d bps: got %d want %d", tc.number, tc.rate, got, tc.order)
		}
	}
}

func TestStratifiedQualitySampling(t *testing.T) {
	s, router, admin := regressionServer(t)
	ctx := context.Background()
	a, annotator := statsUser(t, s, "annotator", "strata-a")
	r, reviewer := statsUser(t, s, "reviewer", "strata-r")
	statsUser(t, s, "quality", "strata-q")
	request := func(token, method, path string, body any, want int) {
		t.Helper()
		res := regressionRequest(t, router, token, method, path, body)
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, want, res.Body)
		}
	}
	request(admin, "PUT", "/quality-policy", map[string]any{"sample_rate_bps": 5000, "strata": []string{"upload"}}, 400)
	request(admin, "PUT", "/quality-policy", map[string]any{"sample_rate_bps": 5000, "strata": []string{"group"}}, 200)
	for _, name := range []string{"first", "second"} {
		request(admin, "POST", "/groups", map[string]any{"name": name, "member_ids": []int64{a, r}}, 200)
	}
	_, source1 := regressionImage(t, s, "unassigned")
	_, source2 := regressionImage(t, s, "unassigned")
	request(admin, "POST", "/groups/dispatch", map[string]any{"group_ids": []int64{1, 2}, "image_ids": []int64{source1, source2}}, 200)
	for _, group := range []int64{1, 2} {
		for pos, source := range []int64{source2, source1} {
			var task int64
			if err := s.pool().QueryRow(ctx, `SELECT id FROM images WHERE group_id=$1 AND source_image_id=$2`, group, source).Scan(&task); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/images/%d", task)
			request(annotator, "POST", path+"/submit", alignmentScore(10, 10, "用于分层抽样的维度评语"), 200)
			request(reviewer, "POST", path+"/review", map[string]any{"action": "approve"}, 200)
			var picked bool
			if err := s.pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quality_samples WHERE image_id=$1)`, task).Scan(&picked); err != nil || picked != (pos == 1) {
				t.Fatalf("group %d review %d picked=%v, err=%v", group, pos+1, picked, err)
			}
		}
	}
}
