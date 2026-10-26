package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shangyue/font-label/server/internal/platform/auth"
)

type statsCounts struct {
	ID            int64   `json:"id"`
	RejectRate    float64 `json:"reject_rate"`
	FirstPassRate float64 `json:"first_pass_rate"`
	RejectEvents  int     `json:"reject_events"`
	ReviewEvents  int     `json:"review_events"`
	Reviewed      int     `json:"reviewed"`
	FirstPass     int     `json:"first_pass"`
}

func statsUser(t *testing.T, s *Server, role, name string) (int64, string) {
	t.Helper()
	var id int64
	if err := s.pool().QueryRow(context.Background(), `INSERT INTO users(username,password_hash,display_name,role) VALUES ($1,'x',$1,$2) RETURNING id`, name, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	token, err := auth.Sign(s.cfg.JWTSecret, auth.Principal{ID: id, Role: role}, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func statsImage(t *testing.T, s *Server, owner int64) int64 {
	t.Helper()
	_, id := regressionImage(t, s, "assigned")
	if _, err := s.pool().Exec(context.Background(), `UPDATE images SET annotator_id=$2 WHERE id=$1`, id, owner); err != nil {
		t.Fatal(err)
	}
	return id
}

func statsSubmit(t *testing.T, router *gin.Engine, token string, id int64, kind string) {
	t.Helper()
	body := map[string]any{"kind": kind}
	if kind == "exception" {
		body["exception_reasons"] = []string{"blur"}
	} else {
		body["scores"] = map[string]int{"white": 20, "black": 20, "variation": 15, "structure": 20, "control": 15}
		body["dim_comments"] = map[string]string{"white": "撇捺左右空白大致匀，间距一致。", "black": "撇高捺低，字形站得稳，重心稳定。", "variation": "捺略短，长短差可以再拉开。", "structure": "占格居中，左右结构安排合理。", "control": "撇有轻重，捺角可以更明确。"}
	}
	w := regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/submit", id), body)
	if w.Code != 200 {
		t.Fatalf("submit %s: %d %s", kind, w.Code, w.Body)
	}
}

func statsReview(t *testing.T, router *gin.Engine, token string, id int64, action string) {
	t.Helper()
	w := regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/review", id), map[string]any{"action": action, "comment": "回归测试，检查审核统计口径。"})
	if w.Code != 200 {
		t.Fatalf("review %s: %d %s", action, w.Code, w.Body)
	}
}

func statsAssert(t *testing.T, router *gin.Engine, token string, owner int64, want statsCounts) {
	t.Helper()
	w := regressionRequest(t, router, token, "GET", "/stats/annotators", nil)
	if w.Code != 200 {
		t.Fatalf("annotators: %d %s", w.Code, w.Body)
	}
	var response struct {
		Data struct {
			Items []statsCounts `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, got := range response.Data.Items {
		if got.ID != owner {
			continue
		}
		if got.ReviewEvents != want.ReviewEvents || got.RejectEvents != want.RejectEvents || got.Reviewed != want.Reviewed || got.FirstPass != want.FirstPass || math.Abs(got.RejectRate-want.RejectRate) > 0.00001 || math.Abs(got.FirstPassRate-want.FirstPassRate) > 0.00001 {
			t.Errorf("annotator %d: got %+v, want %+v", owner, got, want)
		}
		return
	}
	t.Errorf("missing annotator %d: %s", owner, w.Body)
}

func statsOverviewAssert(t *testing.T, router *gin.Engine, token string, reviewEvents, rejectEvents int) {
	t.Helper()
	w := regressionRequest(t, router, token, "GET", "/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	var response struct {
		Data statsCounts `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	rate := 0.0
	if reviewEvents > 0 {
		rate = float64(rejectEvents) / float64(reviewEvents) * 100
	}
	if got := response.Data; got.ReviewEvents != reviewEvents || got.RejectEvents != rejectEvents || math.Abs(got.RejectRate-rate) > 0.00001 {
		t.Errorf("overview got %+v, want %d/%d = %f%%", got, rejectEvents, reviewEvents, rate)
	}
}

func TestStatsReviewRatesUseEventsAndDistinctImages(t *testing.T) {
	s, router, admin := regressionServer(t)
	owner, annotator := statsUser(t, s, "annotator", "annotator")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	empty, _ := statsUser(t, s, "annotator", "empty")
	statsAssert(t, router, admin, empty, statsCounts{})
	statsOverviewAssert(t, router, admin, 0, 0)

	// 034: normal score passes on first submission.
	first := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, first, "score")
	statsReview(t, router, reviewer, first, "approve")
	// 035: exception passes on first submission (status becomes excluded).
	exception := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, exception, "exception")
	statsReview(t, router, reviewer, exception, "approve")
	// 036: rejected once, resubmitted, then approved.
	reworked := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, reworked, "score")
	statsReview(t, router, reviewer, reworked, "reject")
	statsSubmit(t, router, annotator, reworked, "score")
	t.Run("reviewed images stay in the denominator while resubmitted", func(t *testing.T) {
		statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 3, RejectEvents: 1, Reviewed: 3, FirstPass: 2, RejectRate: 100.0 / 3, FirstPassRate: 200.0 / 3})
	})
	statsReview(t, router, reviewer, reworked, "approve")
	t.Run("one rejection in four reviews is 25 percent; two first passes in three images is 66.7 percent", func(t *testing.T) {
		statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 4, RejectEvents: 1, Reviewed: 3, FirstPass: 2, RejectRate: 25, FirstPassRate: 200.0 / 3})
		statsOverviewAssert(t, router, admin, 4, 1)
	})
	t.Run("unreviewed and unsubmitted images do not dilute rates", func(t *testing.T) {
		pending := statsImage(t, s, owner)
		statsSubmit(t, router, annotator, pending, "score")
		_ = statsImage(t, s, owner)
		statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 4, RejectEvents: 1, Reviewed: 3, FirstPass: 2, RejectRate: 25, FirstPassRate: 200.0 / 3})
		statsOverviewAssert(t, router, admin, 4, 1)
	})
}

func TestStatsRepeatedRejectionsCountEveryReview(t *testing.T) {
	s, router, admin := regressionServer(t)
	owner, annotator := statsUser(t, s, "annotator", "annotator")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	id := statsImage(t, s, owner)
	for i := 0; i < 2; i++ {
		statsSubmit(t, router, annotator, id, "score")
		statsReview(t, router, reviewer, id, "reject")
	}
	statsSubmit(t, router, annotator, id, "score")
	statsReview(t, router, reviewer, id, "approve")
	statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 3, RejectEvents: 2, Reviewed: 1, FirstPass: 0, RejectRate: 200.0 / 3, FirstPassRate: 0})
	statsOverviewAssert(t, router, admin, 3, 2)
}

func TestStatsReviewAttributionSurvivesReassignment(t *testing.T) {
	s, router, admin := regressionServer(t)
	original, firstAnnotator := statsUser(t, s, "admin", "original")
	replacement, secondAnnotator := statsUser(t, s, "annotator", "replacement")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	id := statsImage(t, s, original)
	statsSubmit(t, router, firstAnnotator, id, "score")
	statsReview(t, router, reviewer, id, "reject")
	w := regressionRequest(t, router, admin, "POST", fmt.Sprintf("/images/%d/reassign", id), map[string]any{"annotator_id": replacement})
	if w.Code != 200 {
		t.Fatalf("reassign: %s", w.Body)
	}
	statsSubmit(t, router, secondAnnotator, id, "score")
	statsReview(t, router, reviewer, id, "approve")
	statsAssert(t, router, admin, original, statsCounts{ReviewEvents: 1, RejectEvents: 1, Reviewed: 1, FirstPass: 0, RejectRate: 100})
	statsAssert(t, router, admin, replacement, statsCounts{ReviewEvents: 1, RejectEvents: 0, Reviewed: 1, FirstPass: 0, RejectRate: 0})
	statsOverviewAssert(t, router, admin, 2, 1)
}

func TestStatsLaterVersionApprovalIsNotFirstPass(t *testing.T) {
	s, router, admin := regressionServer(t)
	owner, annotator := statsUser(t, s, "annotator", "annotator")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	id := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, id, "score")
	statsReview(t, router, reviewer, id, "reject")
	statsSubmit(t, router, annotator, id, "score")
	statsReview(t, router, reviewer, id, "approve")
	// Legacy data may lack the first version's review. Version 2 still cannot
	// become a first-submission pass just because it is the earliest stored review.
	if _, err := s.pool().Exec(context.Background(), `DELETE FROM reviews WHERE image_id=$1 AND annotation_version=1`, id); err != nil {
		t.Fatal(err)
	}
	statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 1, Reviewed: 1, FirstPass: 0})
	statsOverviewAssert(t, router, admin, 1, 0)
}

func TestStatsLegacyReviewsWithoutVersionSnapshot(t *testing.T) {
	s, router, admin := regressionServer(t)
	owner, annotator := statsUser(t, s, "annotator", "annotator")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	id := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, id, "exception")
	statsReview(t, router, reviewer, id, "approve")
	if _, err := s.pool().Exec(context.Background(), `DELETE FROM annotation_versions WHERE image_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	statsAssert(t, router, admin, owner, statsCounts{ReviewEvents: 1, Reviewed: 1, FirstPass: 1, FirstPassRate: 100})
	statsOverviewAssert(t, router, admin, 1, 0)
}
