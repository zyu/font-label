package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestClaimBatchHasStableTaskOrder(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	// Reverse physical row order with equal timestamps to exercise the tie-breaker.
	var fid int64
	if err := s.pool().QueryRow(ctx, `INSERT INTO folders(name) VALUES('batch-order') RETURNING id`).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{68, 67, 66} {
		if _, err := s.pool().Exec(ctx, `INSERT INTO images(id,folder_id,stored_name,day,seq,orig_name,ext,mime,bytes,sha256,rel_path,status,annotator_id,claimed_at,submitted_at)
   VALUES($1,$2,$3,'2003-01-01',$4,'test.png','png','image/png',1,md5($3)||md5($3),'test.png','assigned',1,'2026-10-27 09:00:00+08','2026-10-27 10:00:00+08')`, id, fid, fmt.Sprintf("order-%d.png", id), int(id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"active", "submitted", "rejected", "approved"} {
		if status != "active" {
			if _, err := s.pool().Exec(ctx, `UPDATE images SET status=$1`, status); err != nil {
				t.Fatal(err)
			}
		}
		want := []int64{66, 67, 68}
		if status != "active" {
			want = []int64{68, 67, 66}
		}
		for page, id := range want {
			w := regressionRequest(t, router, token, "GET", fmt.Sprintf("/claims/my?status=%s&page=%d&page_size=1", status, page+1), nil)
			var res struct{ Data struct{ Items []ImageDTO } }
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(res.Data.Items) != 1 || res.Data.Items[0].ID != id {
				t.Fatalf("%s page %d: %s, want id=%d", status, page+1, w.Body, id)
			}
		}
	}
	// A newly claimed batch must start with the same first image as /claims/my.
	if _, err := s.pool().Exec(ctx, `UPDATE images SET status='unassigned', annotator_id=NULL, claimed_at=NULL`); err != nil {
		t.Fatal(err)
	}
	w := regressionRequest(t, router, token, "POST", "/claims", map[string]any{"mode": "folder", "folder_id": fid})
	var claim struct {
		Data struct {
			Images  []struct{ ID int64 }
			Granted int
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || claim.Data.Granted != 3 {
		t.Fatalf("claim: %s", w.Body)
	}
	for index, id := range []int64{66, 67, 68} {
		if claim.Data.Images[index].ID != id {
			t.Fatalf("claim order: %+v", claim.Data.Images)
		}
	}
}

func TestPersonalRejectStatsKeepReviewHistory(t *testing.T) {
	s, router, admin := regressionServer(t)
	owner, annotator := statsUser(t, s, "annotator", "original")
	other, otherToken := statsUser(t, s, "annotator", "new-owner")
	_, reviewer := statsUser(t, s, "reviewer", "reviewer")
	imageID := statsImage(t, s, owner)
	statsSubmit(t, router, annotator, imageID, "score")
	statsReview(t, router, reviewer, imageID, "reject")
	assertMine := func(token string, current, cumulative int) {
		t.Helper()
		w := regressionRequest(t, router, token, "GET", "/stats/me", nil)
		var res struct {
			Data struct {
				Rejected     int
				RejectEvents int `json:"reject_events"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || res.Data.Rejected != current || res.Data.RejectEvents != cumulative {
			t.Fatalf("mine: %s, want current=%d cumulative=%d", w.Body, current, cumulative)
		}
	}
	assertMine(annotator, 1, 1)
	statsSubmit(t, router, annotator, imageID, "score")
	assertMine(annotator, 0, 1)
	statsReview(t, router, reviewer, imageID, "reject")
	assertMine(annotator, 1, 2)
	w := regressionRequest(t, router, admin, "POST", fmt.Sprintf("/images/%d/reassign", imageID), map[string]any{"annotator_id": other})
	if w.Code != 200 {
		t.Fatalf("reassign: %s", w.Body)
	}
	assertMine(annotator, 0, 2)
	assertMine(otherToken, 1, 0)
}

func TestFolderCountsIncludeEveryStatus(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	var fid int64
	if err := s.pool().QueryRow(ctx, `INSERT INTO folders(name) VALUES('folder-counts') RETURNING id`).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	for index, status := range []string{"unassigned", "assigned", "submitted", "approved", "rejected", "excluded"} {
		if _, err := s.pool().Exec(ctx, `INSERT INTO images(folder_id,stored_name,day,seq,orig_name,ext,mime,bytes,sha256,rel_path,status)
   VALUES($1,$2,'2004-01-01',$3,'test.png','png','image/png',1,md5($2)||md5($2),'test.png',$4)`, fid, fmt.Sprintf("counts-%d.png", index), index+1, status); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/folders?include_archived=1", "/stats/folders"} {
		w := regressionRequest(t, router, token, "GET", path, nil)
		var res struct{ Data struct{ Items []FolderDTO } }
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(res.Data.Items) != 1 {
			t.Fatalf("%s: %s", path, w.Body)
		}
		folder := res.Data.Items[0]
		if folder.Excluded != 1 || folder.Assigned != 1 || folder.Total != 6 || folder.Total != folder.Unassigned+folder.Assigned+folder.Submitted+folder.Approved+folder.Rejected+folder.Excluded {
			t.Fatalf("unbalanced folder: %+v", folder)
		}
	}
}
