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
				rows[i] = exportRow{ID: int64(i + 1), FolderID: 1, StoredName: "same.png", RelPath: "source.png", Status: "approved", Grades: ratingTestGrades()}
			}
			rows = append(rows, exportRow{ID: int64(n + 1), Status: "submitted"})
			var buf bytes.Buffer
			count, err := renderTrainingZIP(&buf, rows, ratingTestDims(), store)
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
				if len(rec.Messages) != 2 || !strings.Contains(rec.Messages[1].Content, "控笔：D") {
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
	_, err = renderTrainingZIP(&buf, []exportRow{{ID: 1, StoredName: "missing.png", RelPath: "missing.png", Status: "approved", Grades: ratingTestGrades()}}, ratingTestDims(), store)
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
	dims := ratingTestDims()
	row := exportRow{ID: 1, StoredName: "字.png", RelPath: "source.png", Status: "approved", Grades: ratingTestGrades(), DimComments: map[string]string{"white": "间距均匀", "black": "重心稳定"}}
	incomplete := row
	incomplete.ID = 2
	incomplete.Grades = map[string]string{"white": "S"}
	unreviewed := row
	unreviewed.ID = 3
	unreviewed.Status = "submitted"
	var buf bytes.Buffer
	count, err := renderTrainingZIP(&buf, []exportRow{row, incomplete, unreviewed}, dims, store)
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
	for _, text := range []string{"白平衡（匀）：S；点评：间距均匀", "黑平衡（稳）：A；点评：重心稳定", "控笔：D"} {
		if !strings.Contains(rec.Messages[1].Content, text) {
			t.Fatalf("missing %s: %+v", text, rec)
		}
	}
}

func TestExportStatusFilter(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{"approved", " i.id > 0 AND " + workflowImageSQL + " AND i.status = 'approved' AND NOT i.quality_pending AND a.kind = 'score' AND a.grades <> '{}'::jsonb"},
		{"", " i.id > 0 AND " + workflowImageSQL + " AND i.status = 'approved' AND NOT i.quality_pending AND a.kind = 'score' AND a.grades <> '{}'::jsonb"},
		{"reviewed", " i.id > 0 AND " + workflowImageSQL + " AND i.status IN ('approved','excluded','rejected') AND NOT i.quality_pending"},
		{"all", " i.id > 0 AND " + workflowImageSQL + " AND i.status<>'exception_locked' AND (i.voided_through_version=0 OR a.id IS NOT NULL)"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			got, _ := (exportFilters{Status: tc.status}).where()
			if got != tc.want {
				t.Fatalf("filter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownloadRoutesRetired(t *testing.T) {
	_, router, token := regressionServer(t)
	for _, request := range []struct{ method, path string }{{"POST", "/exports"}, {"GET", "/exports/1/download"}} {
		if res := regressionRequest(t, router, token, request.method, request.path, nil); res.Code != 404 {
			t.Fatalf("old export route: %d %s", res.Code, res.Body)
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
			w := regressionRequest(t, router, token, method, path, map[string]any{"kind": "exception", "exception_reasons": []string{"blurry"}})
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

func TestConcurrentSubmitRetryIsIdempotent(t *testing.T) {
	s, router, token := regressionServer(t)
	_, id := regressionImage(t, s, "assigned")
	payload := map[string]any{"kind": "exception", "exception_reasons": []string{"blurry"}, "client_submit_id": "22222222-2222-4222-8222-222222222222"}
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
