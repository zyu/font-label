package app

import (
	"archive/zip"
	"bytes"
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
	if _, err := s.pool().Exec(ctx, `INSERT INTO annotations(image_id,annotator_id,kind,grades)
		VALUES($1,1,'score','{"white":"S","black":"A","variation":"B","structure":"C","control":"D"}'),($2,1,'score','{"white":"A"}')`, image, excluded); err != nil {
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
			TaskID int64             `json:"task_id"`
			Path   string            `json:"image_path"`
			Grades map[string]string `json:"grades"`
		}
		if err := json.NewDecoder(f).Decode(&records); err != nil {
			return err
		}
		if len(records) != 1 || records[0].TaskID != image || records[0].Grades["white"] != "S" {
			return fmt.Errorf("unexpected annotations: %+v", records)
		}
		training, err := z.Open("data.jsonl")
		if err != nil {
			return err
		}
		defer training.Close()
		var sample trainRecord
		if err := json.NewDecoder(training).Decode(&sample); err != nil {
			return err
		}
		if len(sample.Images) != 1 || sample.Images[0] != records[0].Path || len(sample.Messages) != 2 ||
			!strings.Contains(sample.Messages[1].Content, "控笔：D") || strings.Contains(sample.Messages[1].Content, "总分") {
			return fmt.Errorf("unexpected training record: %+v", sample)
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
	var packageBytes bytes.Buffer
	count, err := s.writeOSSArchive(ctx, &packageBytes, exportFilters{Status: "all"})
	if err != nil || count != 2 {
		t.Fatalf("archive with reused original has %d records: %v", count, err)
	}
	reader, err := zip.NewReader(bytes.NewReader(packageBytes.Bytes()), int64(packageBytes.Len()))
	if err != nil {
		t.Fatal(err)
	}
	imageCount := 0
	for _, entry := range reader.File {
		if strings.HasPrefix(entry.Name, "images/") {
			imageCount++
		}
	}
	if imageCount != count {
		t.Fatalf("archive has %d image entries for %d rows", imageCount, count)
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
