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
	folder, image := regressionImage(t, s, "approved")
	_, excluded := regressionImage(t, s, "approved")
	if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind,scores,total)
		VALUES($1,1,'score','{"white":20}',100),($2,1,'score','{"white":15}',75)`, image, excluded); err != nil {
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
		f, err := z.Open("annotations.json")
		if err != nil {
			return err
		}
		defer f.Close()
		var records []struct {
			TaskID int64          `json:"task_id"`
			Path   string         `json:"image_path"`
			Scores map[string]int `json:"scores"`
		}
		if err := json.NewDecoder(f).Decode(&records); err != nil {
			return err
		}
		if len(records) != 1 || records[0].TaskID != image || records[0].Scores["white"] != 20 {
			return fmt.Errorf("unexpected annotations: %+v", records)
		}
		img, err := z.Open(records[0].Path)
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
	res := regressionRequest(t, router, admin, "POST", "/exports/oss", body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", res.Code, res.Body)
	}
	if err := s.runPendingExports(ctx); err != nil {
		t.Fatal(err)
	}
	if uploadedFile != "" {
		t.Fatal("local worker picked up an OSS job")
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
	res = regressionRequest(t, router, admin, "GET", "/exports", nil)
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
	folder, image := regressionImage(t, s, "excluded")
	if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind)
		VALUES ($1,1,'exception')`, image); err != nil {
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
