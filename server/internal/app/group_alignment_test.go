package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shangyue/font-label/server/internal/platform/auth"
)

func alignmentScore(white, black int, comment string) map[string]any {
	letter := func(value int) string { return string("DCBAS"[value/5]) }
	return map[string]any{
		"kind":         "score",
		"grades":       map[string]string{"white": letter(white), "black": letter(black), "variation": "B", "structure": "B", "control": "B"},
		"dim_comments": map[string]string{"white": comment, "black": comment, "variation": comment, "structure": comment, "control": comment},
	}
}

func TestGroupAlignment(t *testing.T) {
	s, router, admin := regressionServer(t)
	ctx := context.Background()
	a, first := statsUser(t, s, "annotator", "alignment-a")
	b, second := statsUser(t, s, "annotator", "alignment-b")
	r1, firstReview := statsUser(t, s, "reviewer", "alignment-r1")
	r2, secondReview := statsUser(t, s, "reviewer", "alignment-r2")
	_, quality := statsUser(t, s, "quality", "alignment-q")
	request := func(token, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		res := regressionRequest(t, router, token, method, path, body)
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, want, res.Body)
		}
		return res
	}
	acknowledge := func(token, path string, payload map[string]any, expected string) {
		t.Helper()
		response := request(token, "POST", path+"/submit", payload, 409)
		var conflict struct {
			Error struct {
				Code    string
				Details struct{ Confirmation string }
			}
		}
		if err := json.Unmarshal(response.Body.Bytes(), &conflict); err != nil ||
			conflict.Error.Code != "GROUP_KIND_MISMATCH" || conflict.Error.Details.Confirmation == "" ||
			!strings.Contains(response.Body.String(), expected) {
			t.Fatalf("expected mixed-kind warning with %q: %s (%v)", expected, response.Body, err)
		}
		next := make(map[string]any, len(payload)+1)
		for key, value := range payload {
			next[key] = value
		}
		next["group_confirmation"] = conflict.Error.Details.Confirmation
		request(token, "POST", path+"/submit", next, 200)
	}
	for index, users := range [][]int64{{a, r1}, {b, r2}} {
		request(admin, "POST", "/groups", map[string]any{"name": fmt.Sprint(index), "member_ids": users}, 200)
	}
	newPair := func() (string, string, int64) {
		t.Helper()
		_, source := regressionImage(t, s, "unassigned")
		request(admin, "POST", "/groups/dispatch", map[string]any{"group_ids": []int64{1, 2}, "image_ids": []int64{source}}, 200)
		var x, y int64
		if err := s.pool().QueryRow(ctx, `SELECT min(id), max(id) FROM images WHERE source_image_id=$1`, source).Scan(&x, &y); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("/images/%d", x), fmt.Sprintf("/images/%d", y), x
	}
	approve := map[string]any{"action": "approve", "comment": "已核对提交内容"}
	exception := map[string]any{"kind": "exception", "exception_reasons": []string{"blurry"}, "exception_note": "对方的照片异常备注", "duration_ms": 5000}
	x, y, _ := newPair()
	request(admin, "PUT", "/quality-policy", map[string]int{"sample_rate_bps": 0}, 200)
	request(first, "POST", x+"/submit", alignmentScore(20, 10, "第一组的白平衡标注评语"), 200)
	// The totals are equal, but two dimensions disagree by two grades.
	scoreWithGap := alignmentScore(10, 20, "第二组的白平衡标注评语")
	scoreWithGap["client_submit_id"] = "8f14e45f-ea4b-4c1e-9a63-2b0f0a1c7d34"
	for range 2 {
		res := request(second, "POST", y+"/submit", scoreWithGap, 200)
		if body := res.Body.String(); !strings.Contains(body, `"peer_annotators":["alignment-a"]`) ||
			strings.Contains(body, "第一组的白平衡标注评语") {
			t.Fatalf("unexpected submit result: %s", body)
		}
	}
	detail := request(second, "GET", y, nil, 200).Body.String()
	if !strings.Contains(detail, `"white":"B"`) || !strings.Contains(detail, `"peer_annotators":["alignment-a"]`) ||
		strings.Contains(detail, "第一组的白平衡标注评语") {
		t.Fatalf("submission did not preserve the second annotator's grades and disclose only the peer name: %s", detail)
	}
	history := request(second, "GET", y+"/history", nil, 200).Body.String()
	if strings.Contains(history, "第一组的白平衡标注评语") {
		t.Fatalf("peer comments appeared in annotator history: %s", history)
	}
	request(secondReview, "POST", y+"/review", approve, 200)
	disputes := request(quality, "GET", "/quality/disputes", nil, 200).Body.String()
	for _, field := range []string{`"total":1`, `"own_grade":"B"`, `"peer_grade":"S"`, `"gap":2`, "alignment-a", "第一组的白平衡标注评语"} {
		if !strings.Contains(disputes, field) {
			t.Fatalf("automatic dispute missing %q: %s", field, disputes)
		}
	}
	var gradeSampleID, gradeDisputeID int64
	if err := s.pool().QueryRow(ctx, `SELECT q.id, d.id FROM quality_disputes d JOIN quality_samples q ON q.id=d.sample_id`).
		Scan(&gradeSampleID, &gradeDisputeID); err != nil {
		t.Fatal(err)
	}
	request(quality, "POST", fmt.Sprintf("/quality/samples/%d", gradeSampleID), map[string]string{"result": "pass"}, 404)
	request(quality, "POST", fmt.Sprintf("/quality/disputes/%d", gradeDisputeID), map[string]string{"result": "return"}, 200)
	withinOneGrade := request(second, "POST", y+"/submit", alignmentScore(20, 15, "第二组的白平衡标注评语"), 200)
	if strings.Contains(withinOneGrade.Body.String(), `"peer_annotators"`) {
		t.Fatalf("a one-grade difference triggered a dispute: %s", withinOneGrade.Body)
	}

	x, y, exceptionID := newPair()
	request(admin, "PUT", "/quality-policy", map[string]int{"sample_rate_bps": 0}, 200)
	request(first, "POST", x+"/submit", exception, 200)
	voided := alignmentScore(15, 15, "这份标注后来应当被作废")
	voided["client_submit_id"] = "8f14e45f-ea4b-4c1e-9a63-2b0f0a1c7d33"
	acknowledge(second, y, voided, "对方的照片异常备注")
	request(secondReview, "POST", y+"/review", approve, 200)

	request(admin, "PUT", "/quality-policy", map[string]int{"sample_rate_bps": 10000}, 200)
	request(firstReview, "POST", x+"/review", approve, 200)
	locked := request(second, "GET", y, nil, 200).Body.String()
	for _, expected := range []string{"exception_locked", "对方的照片异常备注", "已核对提交内容", `"duration_ms":null`} {
		if !strings.Contains(locked, expected) {
			t.Fatalf("missing %q in %s", expected, locked)
		}
	}
	if strings.Contains(locked, "这份标注后来应当被作废") {
		t.Fatalf("locked detail leaked voided annotation: %s", locked)
	}
	request(second, "POST", y+"/submit", voided, 409)
	request(second, "PUT", y+"/draft", map[string]any{"kind": "score"}, 409)
	request(second, "GET", "/claims/my?status=exception_locked", nil, 200)
	lockedHistory := request(second, "GET", y+"/history", nil, 200)
	if strings.Contains(lockedHistory.Body.String(), "这份标注后来应当被作废") {
		t.Fatalf("voided annotation appeared in history: %s", lockedHistory.Body)
	}
	clause, args := (exportFilters{Status: "all"}).ossWhere()
	var exported int
	if err := s.pool().QueryRow(ctx, `SELECT count(*) FROM images i
		JOIN annotations a ON a.image_id=i.id WHERE `+clause+
		` AND a.dim_comments->>'white'='这份标注后来应当被作废'`, args...).Scan(&exported); err != nil || exported != 0 {
		t.Fatalf("voided annotation was exported: %d (%v)", exported, err)
	}
	stats := request(second, "GET", "/stats/me", nil, 200).Body.String()
	if !strings.Contains(stats, `"approved":0`) || !strings.Contains(stats, `"submitted_today":1`) {
		t.Fatalf("voided submission was counted: %s", stats)
	}

	var sampleID int64
	if err := s.pool().QueryRow(ctx, `SELECT id FROM quality_samples WHERE image_id=$1`, exceptionID).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	outcome := request(quality, "POST", fmt.Sprintf("/quality/samples/%d", sampleID), map[string]string{"result": "fail"}, 200)
	if !strings.Contains(outcome.Body.String(), `"awaiting_arbiter":true`) {
		t.Fatalf("dispute should wait for an independent arbiter: %s", outcome.Body)
	}
	assertUnlocked := func() {
		t.Helper()
		detail := request(second, "GET", y, nil, 200).Body.String()
		if !strings.Contains(detail, `"status":"assigned"`) || strings.Contains(detail, "对方的照片异常备注") ||
			strings.Contains(detail, "这份标注后来应当被作废") {
			t.Fatalf("unexpected unlocked detail: %s", detail)
		}
	}
	assertUnlocked()
	var disputeID int64
	if err := s.pool().QueryRow(ctx, `SELECT id FROM quality_disputes WHERE sample_id=$1`, sampleID).Scan(&disputeID); err != nil {
		t.Fatal(err)
	}
	request(quality, "POST", fmt.Sprintf("/quality/disputes/%d", disputeID), map[string]string{"result": "return"}, 409)
	arbiterID, _ := statsUser(t, s, "quality", "alignment-arbiter")
	if _, err := s.pool().Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, arbiterID); err != nil {
		t.Fatal(err)
	}
	request(admin, "PATCH", fmt.Sprintf("/users/%d", arbiterID), map[string]any{"status": "active"}, 200)
	arbiter, err := auth.Sign(s.cfg.JWTSecret, auth.Principal{ID: arbiterID, Role: "quality"}, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request(arbiter, "POST", fmt.Sprintf("/quality/disputes/%d", disputeID), map[string]string{"result": "return"}, 200)
	assertUnlocked()
	request(second, "POST", y+"/submit", voided, 409)
	request(second, "POST", y+"/submit", alignmentScore(15, 15, "异常推翻后重新提交标注"), 200)

	x, y, _ = newPair()
	request(first, "POST", x+"/submit", exception, 200)
	request(firstReview, "POST", x+"/review", map[string]string{"action": "reject", "comment": "照片符合标注要求"}, 200)
	assertUnlocked()
	request(second, "POST", y+"/submit", alignmentScore(10, 10, "异常驳回后可以继续标注"), 200)

	x, y, exceptionID = newPair()
	request(second, "POST", y+"/submit", alignmentScore(10, 10, "等待质检的对组标注评语"), 200)
	request(secondReview, "POST", y+"/review", approve, 200)
	if err := s.pool().QueryRow(ctx, `SELECT q.id FROM quality_samples q
		JOIN images peer ON peer.id=q.image_id JOIN images other ON other.source_image_id=peer.source_image_id
		WHERE other.id=$1 AND peer.id<>other.id`, exceptionID).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	acknowledge(first, x, exception, "等待质检的对组标注评语")
	request(firstReview, "POST", x+"/review", approve, 200)
	request(quality, "POST", fmt.Sprintf("/quality/samples/%d", sampleID), map[string]string{"result": "pass"}, 404)

	x, y, _ = newPair()
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, tc := range []struct {
		token string
		path  string
		score int
	}{{first, x, 20}, {second, y, 10}} {
		go func() {
			<-start
			results <- regressionRequest(t, router, tc.token, "POST", tc.path+"/submit",
				alignmentScore(tc.score, 10, "同时提交时必须按档差校验")).Code
		}()
	}
	close(start)
	code1, code2 := <-results, <-results
	if code1 != 200 || code2 != 200 {
		t.Fatalf("simultaneous conflicting submissions: %d, %d", code1, code2)
	}
	firstDetail, secondDetail := request(first, "GET", x, nil, 200).Body.String(), request(second, "GET", y, nil, 200).Body.String()
	if firstSeesPeer, secondSeesPeer := strings.Contains(firstDetail, `"peer_annotators":["alignment-b"]`), strings.Contains(secondDetail, `"peer_annotators":["alignment-a"]`); firstSeesPeer == secondSeesPeer {
		t.Fatalf("only the later submitter should see the earlier one: %s / %s", firstDetail, secondDetail)
	}
}
