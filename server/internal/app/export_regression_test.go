package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/shangyue/font-label/server/internal/platform/score"
	"github.com/shangyue/font-label/server/internal/platform/storage"
)

func TestTrainingZIP(t *testing.T) {
	for _, n := range []int{1, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			store, err := storage.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Write("source.png", []byte("original image")); err != nil {
				t.Fatal(err)
			}
			rows := make([]exportRow, n)
			for i := range rows {
				rows[i] = exportRow{ID: int64(i + 1), FolderID: 1, StoredName: "same.png", RelPath: "source.png", Status: "excluded", Exceptions: "blur"}
			}
			rows = append(rows, exportRow{ID: int64(n + 1), Status: "submitted"})
			var buf bytes.Buffer
			count, err := renderTrainingZIP(&buf, rows, nil, map[string]string{"blur": "模糊"}, store)
			if err != nil {
				t.Fatal(err)
			}
			if count != n {
				t.Fatalf("count = %d", count)
			}
			zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if len(zr.File) != n+1 {
				t.Fatalf("entries = %d, want %d", len(zr.File), n+1)
			}
			entries := map[string]*zip.File{}
			for _, f := range zr.File {
				if entries[f.Name] != nil {
					t.Fatalf("duplicate %s", f.Name)
				}
				entries[f.Name] = f
			}
			if entries["data.jsonl"] == nil {
				t.Fatal("missing data.jsonl")
			}
			data, err := entries["data.jsonl"].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			dec := json.NewDecoder(data)
			seen := map[string]bool{}
			for i := 0; i < n; i++ {
				var rec trainRecord
				if err := dec.Decode(&rec); err != nil {
					t.Fatal(err)
				}
				if len(rec.Messages) != 2 || rec.Messages[1].Content != "无效：模糊，建议重拍" {
					t.Fatalf("bad messages: %+v", rec)
				}
				if len(rec.Images) != 1 {
					t.Fatal("sample must have one image")
				}
				name := rec.Images[0]
				if !strings.HasPrefix(name, "images/") || strings.Count(name, "/") != 1 || seen[name] || entries[name] == nil {
					t.Fatalf("invalid image reference %s", name)
				}
				seen[name] = true
				r, err := entries[name].Open()
				if err != nil {
					t.Fatal(err)
				}
				image, err := io.ReadAll(r)
				r.Close()
				if err != nil || string(image) != "original image" {
					t.Fatalf("image bytes: %q, %v", image, err)
				}
			}
			if err := dec.Decode(&trainRecord{}); err != io.EOF {
				t.Fatalf("extra record: %v", err)
			}
		})
	}
}

func TestTrainingZIPMissingImage(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, err = renderTrainingZIP(&buf, []exportRow{{ID: 1, StoredName: "missing.png", RelPath: "missing.png", Status: "excluded"}}, nil, nil, store)
	if err == nil {
		t.Fatal("must fail instead of exporting dangling image references")
	}
}

func TestTrainingZIPScoredSamples(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("source.png", []byte("original")); err != nil {
		t.Fatal(err)
	}
	total := 100.0
	dims := []score.Dim{{Key: "white", Label: "白平衡", Max: 20}, {Key: "black", Label: "黑平衡", Max: 20}}
	row := exportRow{ID: 1, StoredName: "字.png", RelPath: "source.png", Status: "approved", Total: &total, Grade: "优秀", Scores: map[string]int{"white": 20, "black": 20}, DimComments: map[string]string{"white": "间距均匀", "black": "重心稳定"}}
	incomplete := row
	incomplete.ID = 2
	incomplete.Scores = map[string]int{"white": 20}
	unreviewed := row
	unreviewed.ID = 3
	unreviewed.Status = "submitted"
	var buf bytes.Buffer
	count, err := renderTrainingZIP(&buf, []exportRow{row, incomplete, unreviewed}, dims, nil, store)
	if err != nil || count != 1 {
		t.Fatalf("eligible samples: %d, %v", count, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := zr.Open("data.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	var rec trainRecord
	if err := json.NewDecoder(manifest).Decode(&rec); err != nil {
		t.Fatal(err)
	}
	if rec.Messages[0].Content != trainUserPrompt || rec.Images[0] != "images/1_字.png" {
		t.Fatalf("bad sample: %+v", rec)
	}
	for _, text := range []string{"白平衡：20/20；点评：间距均匀", "黑平衡：20/20；点评：重心稳定", "总分：100/100", "档位：优秀"} {
		if !strings.Contains(rec.Messages[1].Content, text) {
			t.Fatalf("missing %s: %+v", text, rec)
		}
	}
}

func TestExportHistoryIncludesSynchronousDownloads(t *testing.T) {
	s, router, token := regressionServer(t)
	fid, _ := regressionImage(t, s, "excluded")
	if err := s.store.Write("test.png", []byte("image")); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "json", "xlsx", "jsonl"} {
		w := regressionRequest(t, router, token, "POST", "/exports", map[string]any{"format": format, "filters": map[string]any{"folders": []int64{fid}}})
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", format, w.Code, w.Body)
		}
		if format == "jsonl" {
			if w.Header().Get("Content-Type") != "application/zip" || !strings.Contains(w.Header().Get("Content-Disposition"), ".zip") {
				t.Fatalf("bad ZIP headers: %v", w.Header())
			}
			if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := regressionRequest(t, router, token, "GET", "/exports", nil)
	var result struct {
		Data struct {
			Items []struct {
				ID       int64
				Status   string
				RowCount int `json:"row_count"`
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Data.Items) != 4 {
		t.Fatalf("missing history: %s", w.Body)
	}
	for _, j := range result.Data.Items {
		if j.Status != "done" || j.RowCount != 1 {
			t.Fatalf("bad job: %+v", j)
		}
		w = regressionRequest(t, router, token, "GET", fmt.Sprintf("/exports/%d/download", j.ID), nil)
		if w.Code != 200 {
			t.Fatalf("redownload: %s", w.Body)
		}
	}
}

func TestReadonlyDraftAndSubmit(t *testing.T) {
	s, router, token := regressionServer(t)
	for _, status := range []string{"submitted", "approved", "excluded", "unassigned"} {
		_, id := regressionImage(t, s, status)
		for _, method := range []string{"PUT", "POST"} {
			path := fmt.Sprintf("/images/%d/draft", id)
			if method == "POST" {
				path = fmt.Sprintf("/images/%d/submit", id)
			}
			w := regressionRequest(t, router, token, method, path, map[string]any{"kind": "exception", "exception_reasons": []string{"blur"}})
			if w.Code != 409 {
				t.Fatalf("%s %s: %d %s", status, method, w.Code, w.Body)
			}
		}
		var count int
		if err := s.pool().QueryRow(context.Background(), `SELECT count(*) FROM drafts WHERE image_id=$1`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("readonly draft saved")
		}
	}
}

func TestAsyncDatasetExport(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	fid, _ := regressionImage(t, s, "excluded")
	if err := s.store.Write("test.png", []byte("image")); err != nil {
		t.Fatal(err)
	}
	_, err := s.pool().Exec(ctx, `INSERT INTO images(folder_id,stored_name,day,seq,orig_name,ext,mime,bytes,sha256,rel_path,status,annotator_id)
 SELECT $1,'async-'||n||'.png','2002-01-01',n,'same.png','png','image/png',5,md5(n::text)||md5(n::text),'test.png','excluded',1 FROM generate_series(1,9999) AS n`, fid)
	if err != nil {
		t.Fatal(err)
	}
	w := regressionRequest(t, router, token, "POST", "/exports", map[string]any{"format": "jsonl", "filters": map[string]any{"folders": []int64{fid}}})
	var created struct {
		Data struct {
			Async    bool
			JobID    int64 `json:"job_id"`
			RowCount int   `json:"row_count"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !created.Data.Async || created.Data.RowCount != 10000 {
		t.Fatalf("queue: %s", w.Body)
	}
	// Legacy pending jobs have NULL row_count; history must still be readable.
	if _, err := s.pool().Exec(ctx, `UPDATE export_jobs SET row_count=NULL WHERE id=$1`, created.Data.JobID); err != nil {
		t.Fatal(err)
	}
	w = regressionRequest(t, router, token, "GET", "/exports", nil)
	if w.Code != 200 {
		t.Fatalf("pending history: %s", w.Body)
	}
	if err := s.runPendingExports(ctx); err != nil {
		t.Fatal(err)
	}
	w = regressionRequest(t, router, token, "GET", fmt.Sprintf("/exports/%d/download", created.Data.JobID), nil)
	if w.Code != 200 {
		t.Fatalf("async download: %s", w.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 10001 {
		t.Fatalf("ZIP entries: %d", len(zr.File))
	}
	var count int
	if err := s.pool().QueryRow(ctx, `SELECT row_count FROM export_jobs WHERE id=$1`, created.Data.JobID).Scan(&count); err != nil || count != 10000 {
		t.Fatalf("row_count: %d, %v", count, err)
	}
}

func TestDatasetExportFailureAndEligibility(t *testing.T) {
	s, router, token := regressionServer(t)
	fid, _ := regressionImage(t, s, "excluded")
	w := regressionRequest(t, router, token, "POST", "/exports", map[string]any{"format": "jsonl", "filters": map[string]any{"folders": []int64{fid}}})
	if w.Code != 500 {
		t.Fatalf("missing image should fail: %d %s", w.Code, w.Body)
	}
	var status string
	if err := s.pool().QueryRow(context.Background(), `SELECT status FROM export_jobs ORDER BY id DESC LIMIT 1`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("failure history: %s %v", status, err)
	}
	fid, _ = regressionImage(t, s, "submitted")
	w = regressionRequest(t, router, token, "POST", "/exports", map[string]any{"format": "jsonl", "filters": map[string]any{"folders": []int64{fid}, "status": "all"}})
	if w.Code != 404 {
		t.Fatalf("unreviewed samples should not export: %d %s", w.Code, w.Body)
	}
}

func TestExportFinalStatusSurvivesRequestCancellation(t *testing.T) {
	s, _, _ := regressionServer(t)
	ctx := context.Background()
	var id int64
	if err := s.pool().QueryRow(ctx, `INSERT INTO export_jobs(user_id,format,filters,status) VALUES(1,'json','{}','running') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.writeExportFile(canceled, id, "json", []exportRow{{ID: 1}}, nil, nil); err != nil {
		t.Fatalf("finalization should outlive HTTP request: %v", err)
	}
	var status string
	if err := s.pool().QueryRow(ctx, `SELECT status FROM export_jobs WHERE id=$1`, id).Scan(&status); err != nil || status != "done" {
		t.Fatalf("final status: %s, %v", status, err)
	}
	s.failExportJob(canceled, id, fmt.Errorf("test failure"))
	if err := s.pool().QueryRow(ctx, `SELECT status FROM export_jobs WHERE id=$1`, id).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("failure status: %s, %v", status, err)
	}
}

func TestConcurrentSubmitRetryIsIdempotent(t *testing.T) {
	s, router, token := regressionServer(t)
	_, id := regressionImage(t, s, "assigned")
	payload := map[string]any{"kind": "exception", "exception_reasons": []string{"blur"}, "client_submit_id": "22222222-2222-4222-8222-222222222222"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan string, 24)
	for n := 0; n < 24; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/submit", id), payload)
			if w.Code != 200 {
				results <- fmt.Sprintf("%d %s", w.Code, w.Body)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for failure := range results {
		t.Error(failure)
	}
	var version int
	if err := s.pool().QueryRow(context.Background(), `SELECT version FROM annotations WHERE image_id=$1`, id).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version: %d %v", version, err)
	}
}
