package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

type ossExportUploader interface {
	NewObjectKey() string
	ObjectURL(key string) string
	Upload(ctx context.Context, key, filename string) error
}

func (s *Server) ossExportEnabled() bool {
	return s.oss != nil && s.cfg.WorkerInAPI
}

func (s *Server) handleCreateOSSExport(c *gin.Context) {
	if !s.ossExportEnabled() {
		httpx.Fail(c, httpx.New(http.StatusServiceUnavailable, "OSS_UNAVAILABLE", "OSS 上传未配置或后台任务未启用"))
		return
	}
	var input struct {
		Filters exportFilters `json:"filters"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !validOSSExportFilters(input.Filters) {
		httpx.Fail(c, httpx.BadRequest("上传筛选条件不合法"))
		return
	}
	ctx := c.Request.Context()
	clause, args := input.Filters.ossWhere()
	var eligible bool
	err := s.pool().QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM images i JOIN annotations a ON a.image_id=i.id AND a.version>i.voided_through_version WHERE `+clause+`
	)`, args...).Scan(&eligible)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if !eligible {
		httpx.Fail(c, httpx.NotFound("没有已走完流程且五维等级、评语齐全的正常标注"))
		return
	}
	key := s.oss.NewObjectKey()
	url := s.oss.ObjectURL(key)
	me := auth.MustCurrent(c)
	var id int64
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO export_jobs (user_id,format,filters,destination,object_key,object_url)
			VALUES ($1,'jsonl',$2,'oss',$3,$4) RETURNING id`, me.ID, input.Filters, key, url).Scan(&id); err != nil {
			return err
		}
		return s.insertAudit(c, tx, "export.oss.enqueue", "export", id, map[string]any{
			"object_key": key, "filters": input.Filters,
		})
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"job_id": id}, "request_id": httpx.RequestIDOf(c)})
}

func validOSSExportFilters(f exportFilters) bool {
	switch f.Status {
	case "", "approved", "reviewed", "all":
	default:
		return false
	}
	switch f.DateField {
	case "", "created", "submitted":
	default:
		return false
	}
	for _, date := range []string{f.From, f.To} {
		if date != "" {
			if _, err := time.Parse(time.DateOnly, date); err != nil {
				return false
			}
		}
	}
	return f.From == "" || f.To == "" || f.From <= f.To
}

func (f exportFilters) ossWhere() (string, []any) {
	// Historical jobs may carry broader status filters. All new archives contain
	// only finalized valid ratings with complete training answers.
	f.Status = "approved"
	clause, args := f.where()
	return clause + ` AND NOT EXISTS (
		SELECT FROM unnest(ARRAY['white','black','variation','structure','control']) AS dim(key)
		WHERE COALESCE(a.grades->>dim.key,'') NOT IN ('S','A','B','C','D')
		   OR COALESCE(a.dim_comments->>dim.key,'') !~ '[^[:space:]]'
	)`, args
}

func (s *Server) runPendingOSSExports(ctx context.Context) error {
	if !s.ossExportEnabled() {
		return nil
	}
	rows, err := s.pool().Query(ctx, `SELECT id FROM export_jobs
		WHERE destination='oss' AND status IN ('pending','running') ORDER BY id LIMIT 10`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		taken, err := s.tryOSSExport(ctx, id)
		if taken || err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) tryOSSExport(ctx context.Context, id int64) (bool, error) {
	conn, err := s.pool().Acquire(ctx)
	if err != nil {
		return false, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, -id).Scan(&locked); err != nil {
		// The server could have acquired the lock before context cancellation.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(closeCtx)
		return false, err
	}
	if !locked {
		conn.Release()
		return false, nil
	}
	// A dead worker releases its session lock. The next worker can rebuild and
	// upload the same object key before completing publication of the job.
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(releaseCtx, `SELECT pg_advisory_unlock($1)`, -id); err != nil {
			_ = conn.Hijack().Close(releaseCtx)
		} else {
			conn.Release()
		}
	}()

	var f exportFilters
	var key, objectURL string
	err = conn.QueryRow(ctx, `UPDATE export_jobs SET status='running',format='jsonl'
		WHERE id=$1 AND destination='oss' AND status IN ('pending','running')
		RETURNING filters,object_key,object_url`, id).Scan(&f, &key, &objectURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var count int
	var uploadErr error
	if s.oss.ObjectURL(key) != objectURL {
		uploadErr = errors.New("OSS 目标已变更，请重新创建上传任务")
	} else {
		count, uploadErr = s.packageAndUploadOSSExport(ctx, f, key)
	}
	if uploadErr != nil && ctx.Err() != nil {
		return true, ctx.Err()
	}
	status, errorText, action := "done", "", "export.oss.complete"
	if uploadErr != nil {
		status, errorText, action = "failed", uploadErr.Error(), "export.oss.failed"
		slog.Warn("OSS export failed", "job_id", id, "reason", errorText)
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = conn.Exec(finishCtx, `
		WITH finished AS (
			UPDATE export_jobs SET status=$2, error=NULLIF($3,''), row_count=$4, finished_at=now()
			WHERE id=$1 AND destination='oss' AND status='running'
			RETURNING id,user_id,object_key,row_count
		)
		INSERT INTO audit_logs (actor_id,action,target_type,target_id,detail)
		SELECT user_id,$5,'export',id,jsonb_build_object('object_key',object_key,'rows',row_count)
		FROM finished`, id, status, errorText, count, action)
	return true, err
}

func (s *Server) packageAndUploadOSSExport(ctx context.Context, f exportFilters, key string) (int, error) {
	if err := os.MkdirAll(s.exportDir(), 0o755); err != nil {
		return 0, fmt.Errorf("创建导出目录: %w", err)
	}
	file, err := os.CreateTemp(s.exportDir(), ".oss-export-*.zip")
	if err != nil {
		return 0, fmt.Errorf("创建导出临时文件: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	count, err := s.writeOSSArchive(ctx, file, f)
	if err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := s.oss.Upload(ctx, key, file.Name()); err != nil {
		return 0, err
	}
	return count, nil
}

type ossArchiveImage struct {
	localPath   string
	archivePath string
}

func (s *Server) writeOSSArchive(ctx context.Context, w io.Writer, f exportFilters) (int, error) {
	archive := zip.NewWriter(w)
	defer archive.Close()
	data, err := archive.Create("data.jsonl")
	if err != nil {
		return 0, err
	}
	images, count, err := s.writeOSSManifest(ctx, data, f)
	if err != nil {
		return 0, err
	}
	for _, img := range images {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := appendArchiveImage(archive, s.store, img.localPath, img.archivePath, zip.Store); err != nil {
			return 0, err
		}
	}
	return count, archive.Close()
}

func (s *Server) writeOSSManifest(ctx context.Context, training io.Writer, f exportFilters) ([]ossArchiveImage, int, error) {
	clause, args := f.ossWhere()
	rows, err := s.pool().Query(ctx, exportSelect+" WHERE "+clause+" ORDER BY f.name,i.seq,i.id", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	encoder := json.NewEncoder(training)
	encoder.SetEscapeHTML(false)
	var images []ossArchiveImage
	sharedPaths := map[int64]string{}
	count := 0
	for rows.Next() {
		r, err := scanExportRow(rows)
		if err != nil {
			return nil, 0, err
		}
		content, ok := assistantContent(r, trainingDimensions)
		if !ok {
			continue
		}
		imagePath, exists := sharedPaths[r.SourceID]
		if !exists {
			imagePath = trainingImagePath(r)
			sharedPaths[r.SourceID] = imagePath
			images = append(images, ossArchiveImage{localPath: r.RelPath, archivePath: imagePath})
		}
		if err := encoder.Encode(trainingRecord(content, imagePath)); err != nil {
			return nil, 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if count == 0 {
		return nil, 0, errors.New("没有已走完流程且五维等级、评语齐全的正常标注")
	}
	return images, count, nil
}
