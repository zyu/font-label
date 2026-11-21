package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type testOSSUploader struct {
	upload func(key, filename string) error
}

func (u testOSSUploader) NewObjectKey() string { return "font-label/exports/test.zip" }
func (u testOSSUploader) ObjectURL(key string) string {
	return "https://fixture.oss-cn-chengdu.aliyuncs.com/" + key
}
func (u testOSSUploader) Upload(_ context.Context, key, filename string) error {
	return u.upload(key, filename)
}

func TestOSSExportPackagesSelectedAnnotationsAndImages(t *testing.T) {
	s, router, admin := regressionServer(t)
	s.cfg.WorkerInAPI = true
	ctx := context.Background()
	folder, source := regressionImage(t, s, "unassigned")
	a1, _ := statsUser(t, s, "annotator", "oss-a")
	r1, _ := statsUser(t, s, "reviewer", "oss-r")
	for _, name := range []string{"first", "second", "pending", "invalid", "rejected", "incomplete"} {
		res := regressionRequest(t, router, admin, "POST", "/groups", map[string]any{"name": name, "member_ids": []int64{a1, r1}})
		if res.Code != 200 {
			t.Fatal(res.Body)
		}
	}
	res := regressionRequest(t, router, admin, "POST", "/groups/dispatch", map[string]any{
		"group_ids": []int64{1, 2, 3, 4, 5, 6}, "image_ids": []int64{source},
	})
	if res.Code != 200 {
		t.Fatal(res.Body)
	}
	if _, err := s.pool().Exec(ctx, `
		UPDATE images SET status=CASE group_id WHEN 4 THEN 'excluded' WHEN 5 THEN 'rejected' ELSE 'approved' END,
			quality_pending=(group_id=3)
		WHERE source_image_id=$1`, source); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool().Exec(ctx, `
		INSERT INTO annotations(image_id,annotator_id,kind,grades,dim_comments)
		SELECT id,annotator_id,CASE WHEN group_id=4 THEN 'exception' ELSE 'score' END,
			CASE group_id WHEN 4 THEN NULL WHEN 6 THEN '{"white":"A"}'::jsonb ELSE $2::jsonb END,
			CASE WHEN group_id=2 THEN $3::jsonb || '{"control":"收笔果断"}'::jsonb ELSE $3::jsonb END
		FROM images WHERE source_image_id=$1`, source, ratingTestGrades(), ratingTestComments()); err != nil {
		t.Fatal(err)
	}

	if err := s.store.Write("test.png", []byte("source image")); err != nil {
		t.Fatal(err)
	}
	_, annotator := statsUser(t, s, "annotator", "oss-annotator")
	body := map[string]any{"filters": map[string]any{"folders": []int64{folder}, "status": "approved"}}
	for _, tc := range []struct {
		token string
		want  int
	}{{annotator, http.StatusForbidden}, {admin, http.StatusServiceUnavailable}} {
		if res := regressionRequest(t, router, tc.token, "POST", "/exports/oss", body); res.Code != tc.want {
			t.Fatalf("precondition: %d %s", res.Code, res.Body)
		}
	}

	var uploadedFile string
	s.oss = testOSSUploader{upload: func(key, filename string) error {
		uploadedFile = filename
		if key != "font-label/exports/test.zip" {
			return fmt.Errorf("wrong object: %s", key)
		}
		z, err := zip.OpenReader(filename)
		if err != nil {
			return err
		}
		defer z.Close()
		if len(z.File) != 2 {
			return fmt.Errorf("unexpected ZIP entries: %d", len(z.File))
		}
		training, err := z.Open("data.jsonl")
		if err != nil {
			return err
		}
		defer training.Close()
		decoder := json.NewDecoder(training)
		var first, second trainRecord
		if err := decoder.Decode(&first); err != nil {
			return err
		}
		if err := decoder.Decode(&second); err != nil {
			return err
		}
		if err := decoder.Decode(&trainRecord{}); err != io.EOF {
			return fmt.Errorf("unexpected third sample: %v", err)
		}
		if len(first.Images) != 1 || len(second.Images) != 1 || first.Images[0] != second.Images[0] ||
			len(first.Messages) != 2 || len(second.Messages) != 2 ||
			!strings.Contains(first.Messages[1].Content, "匀（白平衡）：S，间距均匀\n") ||
			!strings.Contains(second.Messages[1].Content, "控笔：D，收笔果断") {
			return fmt.Errorf("unexpected training samples: %+v / %+v", first, second)
		}
		img, err := z.Open(first.Images[0])
		if err != nil {
			return err
		}
		defer img.Close()
		data, err := io.ReadAll(img)
		if err != nil || string(data) != "source image" {
			return fmt.Errorf("unexpected image: %q, %v", data, err)
		}
		return nil
	}}
	res = regressionRequest(t, router, admin, "POST", "/exports/oss", body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", res.Code, res.Body)
	}

	// A formerly running job is recovered after the worker loses its lock.
	if _, err := s.pool().Exec(ctx, `UPDATE export_jobs SET status='running' WHERE destination='oss'`); err != nil {
		t.Fatal(err)
	}
	if err := s.runPendingOSSExports(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(uploadedFile); !os.IsNotExist(err) {
		t.Fatalf("temporary ZIP was retained: %v", err)
	}

	if _, err := s.pool().Exec(ctx, `INSERT INTO export_jobs(user_id,format,status,filters,destination,object_key,object_url)
		VALUES (1,'json','done','{}','oss','legacy-export.zip','https://fixture.example/legacy-export.zip')`); err != nil {
		t.Fatal(err)
	}
	res = regressionRequest(t, router, admin, "GET", "/exports", nil)
	if strings.Contains(res.Body.String(), "legacy-export.zip") {
		t.Fatalf("legacy export listed among training ZIPs: %s", res.Body)
	}
	for _, fragment := range []string{`"oss_enabled":true`, `"destination":"oss"`, `"status":"done"`, `"object_url":"https://fixture.oss-cn-chengdu.aliyuncs.com/font-label/exports/test.zip"`} {
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), fragment) {
			t.Fatalf("missing %s in completed history: %d %s", fragment, res.Code, res.Body)
		}
	}
}

func TestOSSExportDoesNotUploadWhenImageIsMissing(t *testing.T) {
	s, router, admin := regressionServer(t)
	s.cfg.WorkerInAPI = true
	ctx := context.Background()
	folder, image := regressionImage(t, s, "approved")
	if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind,grades,dim_comments)
		VALUES ($1,1,'score',$2,$3)`, image, ratingTestGrades(), ratingTestComments()); err != nil {
		t.Fatal(err)
	}
	s.oss = testOSSUploader{upload: func(_, _ string) error { t.Fatal("incomplete ZIP was uploaded"); return nil }}
	unannotatedFolder, _ := regressionImage(t, s, "assigned")
	res := regressionRequest(t, router, admin, "POST", "/exports/oss", map[string]any{"filters": map[string]any{"folders": []int64{unannotatedFolder}, "status": "all"}})
	if res.Code != http.StatusNotFound {
		t.Fatalf("unannotated only: %d %s", res.Code, res.Body)
	}
	res = regressionRequest(t, router, admin, "POST", "/exports/oss", map[string]any{"filters": map[string]any{"folders": []int64{folder}, "status": "reviewed"}})
	if res.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", res.Code, res.Body)
	}
	if err := s.runPendingOSSExports(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.pool().QueryRow(ctx, `SELECT status FROM export_jobs WHERE destination='oss'`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("failed history: %s %v", status, err)
	}
}
