package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	dbmigrations "github.com/shangyue/font-label/server/db"
	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/config"
	"github.com/shangyue/font-label/server/internal/platform/db"
	"github.com/shangyue/font-label/server/internal/platform/storage"
)

// Each regression run gets its own schema and temporary storage. No live tasks or
// settings are modified, including when TEST_DATABASE_URL points at the dev DB.
func regressionServer(t *testing.T) (*Server, *gin.Engine, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run workflow integration tests")
	}
	ctx := context.Background()
	adminDB, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminDB.Close)
	schema := fmt.Sprintf("regression_%d", time.Now().UnixNano())
	if _, err := adminDB.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := adminDB.Pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	database, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	files, err := fs.ReadDir(dbmigrations.FS, dbmigrations.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := dbmigrations.FS.ReadFile(dbmigrations.Dir + "/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(raw), "-- +goose Down")[0]
		if _, err := database.Pool.Exec(ctx, up); err != nil {
			t.Fatalf("migrate %s: %v", file.Name(), err)
		}
	}
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "regression-secret", MediaSecret: "regression-media", MaxFileMB: 10, MaxHold: 100}
	s := New(cfg, database, store)
	var uid int64
	if err := database.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,role) VALUES ('tester','x','测试','admin') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	token, err := auth.Sign(cfg.JWTSecret, auth.Principal{ID: uid, Role: "admin"}, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return s, s.Router(), token
}

func regressionRequest(t *testing.T, router http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func regressionImage(t *testing.T, s *Server, status string) (int64, int64) {
	t.Helper()
	var fid, id int64
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	if err := s.pool().QueryRow(ctx, `INSERT INTO folders(name) VALUES ($1) RETURNING id`, fmt.Sprint(suffix)).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	if err := s.pool().QueryRow(ctx, `INSERT INTO images(folder_id,stored_name,day,seq,orig_name,ext,mime,bytes,sha256,rel_path,status,annotator_id) VALUES ($1,$2,'2001-01-01',$3,'test.png','png','image/png',1,repeat('0',64),'test.png',$4,1) RETURNING id`, fid, fmt.Sprintf("test-%d.png", suffix), fid, status).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return fid, id
}

func TestWorkflowArchiveRequiresCompletedImages(t *testing.T) {
	s, router, token := regressionServer(t)
	for _, status := range []string{"unassigned", "assigned", "submitted", "rejected", "approved", "excluded"} {
		t.Run(status, func(t *testing.T) {
			fid, _ := regressionImage(t, s, status)
			w := regressionRequest(t, router, token, "PATCH", fmt.Sprintf("/folders/%d", fid), map[string]any{"archived": true, "note": "不能部分更新"})
			want := 409
			if status == "approved" || status == "excluded" {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("archive %s: want %d, got %d: %s", status, want, w.Code, w.Body)
			}
			if want == 409 {
				var archived bool
				var note string
				if err := s.pool().QueryRow(context.Background(), `SELECT archived_at IS NOT NULL,note FROM folders WHERE id=$1`, fid).Scan(&archived, &note); err != nil {
					t.Fatal(err)
				}
				if archived || note != "" {
					t.Fatalf("rejected archive mutated folder: archived=%v note=%q", archived, note)
				}
			}
			w = regressionRequest(t, router, token, "PATCH", fmt.Sprintf("/folders/%d", fid), map[string]any{"archived": false})
			if w.Code != 200 {
				t.Fatalf("unarchive: %s", w.Body)
			}
		})
	}
}

func TestWorkflowSubmitAndDraftDimComments(t *testing.T) {
	s, router, token := regressionServer(t)
	comments := map[string]string{"white": "撇捺左右空白大致匀，间距一致。", "black": "撇高捺低，字形站得稳，重心稳定。", "variation": "捺略短，长短差可以再拉开。", "structure": "占格居中，左右结构安排合理。", "control": "撇有轻重，捺角可以更明确。"}
	for _, kind := range []string{"score", "exception"} {
		t.Run(kind, func(t *testing.T) {
			_, id := regressionImage(t, s, "assigned")
			draft := map[string]any{"kind": "score", "scores": map[string]int{"white": 20, "black": 20, "variation": 15, "structure": 20, "control": 15}, "dim_comments": comments}
			w := regressionRequest(t, router, token, "PUT", fmt.Sprintf("/images/%d/draft", id), draft)
			if w.Code != 200 {
				t.Fatalf("save draft: %s", w.Body)
			}
			w = regressionRequest(t, router, token, "GET", fmt.Sprintf("/images/%d", id), nil)
			var saved struct {
				Data struct {
					Draft struct {
						DimComments map[string]string `json:"dim_comments"`
					} `json:"draft"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Data.Draft.DimComments["white"] != comments["white"] {
				t.Fatalf("draft lost comments: %s", w.Body)
			}
			payload := draft
			if kind == "exception" {
				payload = map[string]any{"kind": "exception", "exception_reasons": []string{"blur"}, "exception_note": "图片严重模糊"}
			}
			payload["client_submit_id"] = fmt.Sprintf("11111111-1111-4111-8111-%012d", id)
			w = regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/submit", id), payload)
			if w.Code != 200 {
				t.Fatalf("submit %s: %s", kind, w.Body)
			}
			var raw, status string
			var count int
			if err := s.pool().QueryRow(context.Background(), `SELECT status,(SELECT count(*) FROM drafts WHERE image_id=$1),(SELECT dim_comments::text FROM annotations WHERE image_id=$1) FROM images WHERE id=$1`, id).Scan(&status, &count, &raw); err != nil {
				t.Fatal(err)
			}
			if status != "submitted" || count != 0 {
				t.Fatalf("status=%s drafts=%d", status, count)
			}
			if kind == "score" {
				var got map[string]string
				if err := json.Unmarshal([]byte(raw), &got); err != nil {
					t.Fatal(err)
				}
				for key, comment := range comments {
					if got[key] != comment {
						t.Errorf("lost %s comment", key)
					}
				}
			}
			w = regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/submit", id), payload)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
				t.Fatalf("retry not idempotent: %s", w.Body)
			}
		})
	}
}

func TestWorkflowUploadLimitsAndArchivedFolder(t *testing.T) {
	s, router, token := regressionServer(t)
	fid, _ := regressionImage(t, s, "approved")
	archive := regressionRequest(t, router, token, "PATCH", fmt.Sprintf("/folders/%d", fid), map[string]bool{"archived": true})
	if archive.Code != 200 {
		t.Fatal(archive.Body)
	}
	for _, byName := range []bool{false, true} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if byName {
			var name string
			if err := s.pool().QueryRow(context.Background(), `SELECT name FROM folders WHERE id=$1`, fid).Scan(&name); err != nil {
				t.Fatal(err)
			}
			_ = writer.WriteField("folder_name", name)
		} else {
			_ = writer.WriteField("folder_id", fmt.Sprint(fid))
		}
		part, _ := writer.CreateFormFile("files", "test.png")
		_, _ = part.Write([]byte("image"))
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/api/v1/upload", &body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 409 {
			t.Fatalf("upload to archived folder (byName=%v): %d %s", byName, w.Code, w.Body)
		}
	}
	me := regressionRequest(t, router, token, "GET", "/auth/me", nil)
	var data struct {
		Data struct {
			Limits struct {
				MaxMB int `json:"max_file_mb"`
			} `json:"limits"`
		} `json:"data"`
	}
	_ = json.Unmarshal(me.Body.Bytes(), &data)
	if data.Data.Limits.MaxMB != 10 {
		t.Fatalf("default size limit = %d, want 10", data.Data.Limits.MaxMB)
	}
	for _, value := range []any{-1, 1.5, "10", nil} {
		w := regressionRequest(t, router, token, "PUT", "/settings/max_file_mb", value)
		if w.Code != 422 {
			t.Errorf("invalid size setting %v accepted: %d", value, w.Code)
		}
	}
	w := regressionRequest(t, router, token, "PUT", "/settings/max_file_mb", 0)
	if w.Code != 200 {
		t.Fatalf("zero setting: %s", w.Body)
	}
	me = regressionRequest(t, router, token, "GET", "/auth/me", nil)
	_ = json.Unmarshal(me.Body.Bytes(), &data)
	if data.Data.Limits.MaxMB != 0 {
		t.Fatalf("zero limit replaced: %s", me.Body)
	}
}

func TestWorkflowUploadSizeBoundaries(t *testing.T) {
	s, router, token := regressionServer(t)
	fid, _ := regressionImage(t, s, "approved")
	for _, tc := range []struct {
		name        string
		limit, size int
		reason      string
	}{
		{"over 10MB", 10, 10*1024*1024 + 1, "FILE_TOO_LARGE"},
		{"exactly 10MB is accepted by size validation", 10, 10 * 1024 * 1024, "UNSUPPORTED_TYPE"},
		{"zero rejects nonempty file", 0, 1, "FILE_TOO_LARGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := regressionRequest(t, router, token, "PUT", "/settings/max_file_mb", tc.limit)
			if w.Code != 200 {
				t.Fatal(w.Body)
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			_ = writer.WriteField("folder_id", fmt.Sprint(fid))
			part, err := writer.CreateFormFile("files", "boundary.png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(make([]byte, tc.size)); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/api/v1/upload", &body)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			result := httptest.NewRecorder()
			router.ServeHTTP(result, req)
			if !strings.Contains(result.Body.String(), `"reason":"`+tc.reason+`"`) {
				t.Fatalf("%s: %s", tc.name, result.Body)
			}
		})
	}
}

func TestWorkflowDimCommentSettings(t *testing.T) {
	s, router, token := regressionServer(t)
	rubric, err := s.activeRubric(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"name": "短评方案", "dims": rubric.Dims, "grade_bands": rubric.Bands, "dim_comment_min": 3, "dim_comment_max": 50}
	w := regressionRequest(t, router, token, "POST", "/rubrics", body)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	w = regressionRequest(t, router, token, "POST", fmt.Sprintf("/rubrics/%d/activate", created.Data.ID), nil)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	_, id := regressionImage(t, s, "assigned")
	// The user's concrete example includes the concise structure comment 占格居中。
	body = map[string]any{"kind": "score", "scores": map[string]int{"white": 20, "black": 20, "variation": 15, "structure": 20, "control": 15}, "dim_comments": map[string]string{"white": "撇捺左右空白大致匀。", "black": "撇高捺低，字形站得稳。", "variation": "捺略短，长短差可再拉开。", "structure": "占格居中。", "control": "撇有轻重，捺角可更明确。"}}
	w = regressionRequest(t, router, token, "POST", fmt.Sprintf("/images/%d/submit", id), body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":90`) {
		t.Fatalf("configured short comments: %s", w.Body)
	}
}

func TestWorkflowMigrationRestoresUnfinishedFolders(t *testing.T) {
	s, _, _ := regressionServer(t)
	ctx := context.Background()
	incomplete, _ := regressionImage(t, s, "unassigned")
	complete, _ := regressionImage(t, s, "approved")
	if _, err := s.pool().Exec(ctx, `UPDATE folders SET archived_at=now() WHERE id=ANY($1)`, []int64{incomplete, complete}); err != nil {
		t.Fatal(err)
	}
	raw, err := dbmigrations.FS.ReadFile("migrations/00006_workflow_limits.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(raw), "-- +goose Down")[0]
	for _, limit := range []int{0, 20} {
		if _, err := s.pool().Exec(ctx, `UPDATE settings SET value=$1::jsonb WHERE key='max_file_mb'`, fmt.Sprint(limit)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool().Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
		want := limit
		if limit == 20 {
			want = 10
		}
		if got := s.settingInt(ctx, "max_file_mb", -1); got != want {
			t.Errorf("limit %d -> %d, want %d", limit, got, want)
		}
	}
	var archived bool
	if err := s.pool().QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM folders WHERE id=$1`, incomplete).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived {
		t.Error("unfinished folder is still archived")
	}
	if err := s.pool().QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM folders WHERE id=$1`, complete).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Error("completed folder should stay archived")
	}
}

func TestWorkflowConcurrentArchiveAndUpload(t *testing.T) {
	s, router, token := regressionServer(t)
	ctx := context.Background()
	raw, err := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 10; n++ {
		var fid int64
		if err := s.pool().QueryRow(ctx, `INSERT INTO folders(name) VALUES ($1) RETURNING id`, fmt.Sprintf("parallel-%d", n)).Scan(&fid); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("folder_id", fmt.Sprint(fid))
		part, _ := writer.CreateFormFile("files", "tiny.gif")
		_, _ = part.Write(raw)
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/api/v1/upload", &body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-ready
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 200 && w.Code != 409 {
				t.Errorf("upload: %d %s", w.Code, w.Body)
			}
		}()
		go func() {
			defer wg.Done()
			<-ready
			w := regressionRequest(t, router, token, "PATCH", fmt.Sprintf("/folders/%d", fid), map[string]bool{"archived": true})
			if w.Code != 200 && w.Code != 409 {
				t.Errorf("archive: %d %s", w.Code, w.Body)
			}
		}()
		close(ready)
		wg.Wait()
		var invalid bool
		if err := s.pool().QueryRow(ctx, `SELECT archived_at IS NOT NULL AND EXISTS (SELECT 1 FROM images WHERE folder_id=$1 AND status NOT IN ('approved','excluded')) FROM folders WHERE id=$1`, fid).Scan(&invalid); err != nil {
			t.Fatal(err)
		}
		if invalid {
			t.Fatalf("concurrent upload left unfinished images in archived folder %d", fid)
		}
	}
}
