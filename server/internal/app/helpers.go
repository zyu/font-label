package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/media"
	"github.com/shangyue/font-label/server/internal/platform/score"
)

// ---------- audit ----------

func (s *Server) audit(c *gin.Context, action, targetType string, targetID int64, detail map[string]any) {
	payload, _ := json.Marshal(detail)
	if payload == nil || string(payload) == "null" {
		payload = []byte("{}")
	}
	var actor any
	if p, ok := currentUser(c); ok {
		actor = p.ID
	}
	var ip any
	if v := c.ClientIP(); v != "" {
		ip = v
	}
	_, err := s.pool().Exec(c.Request.Context(),
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, detail, ip, ua)
		 VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)`,
		actor, action, targetType, nullInt(targetID), payload, ip, c.Request.UserAgent())
	if err != nil {
		slog.Error("audit insert failed", "action", action, "err", err)
	}
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// ---------- settings ----------

func (s *Server) settingInt(ctx context.Context, key string, def int) int {
	var raw string
	err := s.pool().QueryRow(ctx, `SELECT value::text FROM settings WHERE key=$1`, key).Scan(&raw)
	if err != nil {
		return def
	}
	var n int
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return def
	}
	return n
}

func (s *Server) settingValue(ctx context.Context, key string) any {
	var raw string
	if err := s.pool().QueryRow(ctx, `SELECT value::text FROM settings WHERE key=$1`, key).Scan(&raw); err != nil {
		return nil
	}
	var v any
	_ = json.Unmarshal([]byte(raw), &v)
	return v
}

// ---------- rubric ----------

func (s *Server) activeRubric(ctx context.Context) (score.Rubric, error) {
	var (
		id                int64
		name, dims, bands string
		cmin, cmax        int
		dmin, dmax        int
		allow             bool
	)
	err := s.pool().QueryRow(ctx,
		`SELECT id, name, dims::text, grade_bands::text, comment_min, comment_max,
		        dim_comment_min, dim_comment_max, allow_total_override
		   FROM rubrics WHERE is_active LIMIT 1`).
		Scan(&id, &name, &dims, &bands, &cmin, &cmax, &dmin, &dmax, &allow)
	if err != nil {
		return score.Rubric{}, fmt.Errorf("没有启用的评分方案: %w", err)
	}
	return score.FromJSON(id, name, []byte(dims), []byte(bands), cmin, cmax, dmin, dmax, allow)
}

// ---------- image row / dto ----------

type imageRow struct {
	ID          int64
	FolderID    int64
	Folder      string
	StoredName  string
	OrigName    string
	Seq         int
	Day         time.Time
	Status      string
	Width       int
	Height      int
	AnnotatorID int64
	Annotator   string
	RejectCount int
	ReviewCount int
	Kind        string
	Total       float64
	Grade       string
	Comment     string
	Scores      string
	DimComments string
	Exceptions  []string
	DurationMS  int
	ClaimedAt   time.Time
	SubmittedAt time.Time
}

const imageSelect = `
SELECT i.id, i.folder_id, f.name, i.stored_name, i.orig_name, i.seq, i.day, i.status,
       COALESCE(i.width,0), COALESCE(i.height,0),
       COALESCE(i.annotator_id,0), COALESCE(u.display_name,''),
       i.reject_count, i.review_count,
       COALESCE(i.claimed_at, to_timestamp(0)),
       COALESCE(i.submitted_at, to_timestamp(0)),
       COALESCE(a.kind,''), COALESCE(a.total,-1), COALESCE(a.grade,''),
       COALESCE(a.comment,''), COALESCE(a.scores::text,'{}'), COALESCE(a.dim_comments::text,'{}'),
       COALESCE(a.exception_reasons,'{}'), COALESCE(a.duration_ms,0)
  FROM images i
  JOIN folders f ON f.id = i.folder_id
  LEFT JOIN users u ON u.id = i.annotator_id
  LEFT JOIN annotations a ON a.image_id = i.id`

const imageSelectByID = imageSelect + ` WHERE i.id = $1`

func scanImage(row pgx.Row) (imageRow, error) {
	var r imageRow
	err := row.Scan(&r.ID, &r.FolderID, &r.Folder, &r.StoredName, &r.OrigName, &r.Seq, &r.Day, &r.Status,
		&r.Width, &r.Height, &r.AnnotatorID, &r.Annotator, &r.RejectCount, &r.ReviewCount,
		&r.ClaimedAt, &r.SubmittedAt, &r.Kind, &r.Total, &r.Grade, &r.Comment, &r.Scores,
		&r.DimComments, &r.Exceptions, &r.DurationMS)
	return r, err
}

func (s *Server) signURL(id int64, variant string) string {
	return "/api/v1" + media.URL(s.cfg.MediaSecret, id, variant, s.cfg.MediaTTL)
}

func (s *Server) toDTO(r imageRow) ImageDTO {
	d := ImageDTO{
		ID:          r.ID,
		FolderID:    r.FolderID,
		Folder:      r.Folder,
		StoredName:  r.StoredName,
		OrigName:    r.OrigName,
		Seq:         r.Seq,
		Day:         r.Day.Format("2006-01-02"),
		Status:      r.Status,
		Width:       r.Width,
		Height:      r.Height,
		Annotator:   r.Annotator,
		RejectCount: r.RejectCount,
		ReviewCount: r.ReviewCount,
		Kind:        r.Kind,
		Grade:       r.Grade,
		Comment:     r.Comment,
		Exceptions:  r.Exceptions,
		ThumbURL:    s.signURL(r.ID, "thumb"),
		PreviewURL:  s.signURL(r.ID, "preview"),
		OriginalURL: s.signURL(r.ID, "original"),
	}
	if d.Exceptions == nil {
		d.Exceptions = []string{}
	}
	if r.AnnotatorID > 0 {
		id := r.AnnotatorID
		d.AnnotatorID = &id
	}
	if r.Total >= 0 {
		t := r.Total
		d.Total = &t
	}
	if r.DurationMS > 0 {
		ms := r.DurationMS
		d.DurationMS = &ms
	}
	if !r.ClaimedAt.IsZero() && r.ClaimedAt.Unix() > 0 {
		t := r.ClaimedAt
		d.ClaimedAt = &t
	}
	if !r.SubmittedAt.IsZero() && r.SubmittedAt.Unix() > 0 {
		t := r.SubmittedAt
		d.SubmittedAt = &t
	}
	d.Scores = map[string]int{}
	if r.Scores != "" && r.Scores != "{}" {
		_ = json.Unmarshal([]byte(r.Scores), &d.Scores)
	}
	d.DimComments = map[string]string{}
	if r.DimComments != "" && r.DimComments != "{}" && r.DimComments != "null" {
		_ = json.Unmarshal([]byte(r.DimComments), &d.DimComments)
	}
	return d
}

func (s *Server) imageByID(ctx context.Context, id int64) (imageRow, error) {
	return scanImage(s.pool().QueryRow(ctx, imageSelectByID, id))
}

// ---------- login rate limit ----------

type loginLimiter struct {
	mu   sync.Mutex
	fail map[string]*loginBucket
	pass map[string]*loginBucket
}

type loginBucket struct {
	count int
	until time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fail: map[string]*loginBucket{}, pass: map[string]*loginBucket{}}
}

func (l *loginLimiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.fail[key]
	if !ok {
		return false
	}
	if time.Now().After(b.until) {
		delete(l.fail, key)
		return false
	}
	return b.count >= 5
}

func (l *loginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.fail[key]
	if !ok || time.Now().After(b.until) {
		b = &loginBucket{}
		l.fail[key] = b
	}
	b.count++
	b.until = time.Now().Add(10 * time.Minute)
}

func (l *loginLimiter) Pass(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fail, key)
}

func currentUser(c *gin.Context) (principal, bool) {
	return principalFrom(c)
}
