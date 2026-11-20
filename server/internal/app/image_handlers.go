package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

func (s *Server) handleListImages(c *gin.Context) {
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	page, size := httpx.Paging(c)

	clause := "i.id > 0"
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		clause += fmt.Sprintf(cond, len(args))
	}

	switch me.Role {
	case "annotator":
		add(" AND i.annotator_id=$%d", me.ID)
	case "reviewer":
		args = append(args, me.Role, me.ID)
		clause += " AND " + reviewScope("$1", "$2")
	case "admin":
		if c.Query("sources") == "1" {
			clause += " AND i.group_id IS NULL"
		} else {
			clause += " AND " + workflowImageSQL
		}
	default:
		httpx.Fail(c, httpx.Forbidden("请在质检或仲裁队列查看任务"))
		return
	}
	if aid := httpx.QueryInt64(c, "annotator_id"); aid > 0 {
		add(" AND i.annotator_id=$%d", aid)
	}
	if fid := httpx.QueryInt64(c, "folder_id"); fid > 0 {
		add(" AND i.folder_id=$%d", fid)
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" {
		parts := strings.Split(st, ",")
		add(" AND i.status = ANY($%d)", parts)
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		args = append(args, "%"+q+"%")
		n := len(args)
		clause += fmt.Sprintf(" AND (i.stored_name ILIKE $%d OR i.orig_name ILIKE $%d 			OR ('P'||lpad(i.sample_no::text,greatest(6,length(i.sample_no::text)),'0')) ILIKE $%d)", n, n, n)
	}
	if from := c.Query("from"); from != "" {
		add(" AND i.created_at >= $%d::date", from)
	}
	if to := c.Query("to"); to != "" {
		add(" AND i.created_at < $%d::date + 1", to)
	}

	var total int
	if err := s.pool().QueryRow(ctx, `SELECT count(*) FROM images i WHERE `+clause, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool().Query(ctx,
		imageSelect+` WHERE `+clause+` ORDER BY i.id DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)),
		args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []ImageDTO{}
	for rows.Next() {
		r, err := scanImage(rows)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, s.toDTO(r))
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) handleGetImage(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	tx, err := s.pool().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer tx.Rollback(ctx)
	row, err := scanImage(tx.QueryRow(ctx, imageSelectByID, id))
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if err := requireImageAccess(c, tx, id, row.AnnotatorID); err != nil {
		httpx.Fail(c, err)
		return
	}
	dto := s.toDTO(row)
	var draft any
	var raw string
	if row.Status == "assigned" || row.Status == "rejected" {
		if err := tx.QueryRow(ctx,
			`SELECT payload::text FROM drafts WHERE user_id=$1 AND image_id=$2`, auth.MustCurrent(c).ID, id).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &draft)
		}
	}
	reviewImageID, reviewVersion := id, 0
	if row.ExceptionLockID != 0 {
		reviewImageID, reviewVersion = row.ExceptionLockID, row.AnnotationVersion
	}
	reviews, err := reviewsOf(ctx, tx, reviewImageID, reviewVersion)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{
		"image":   dto,
		"reviews": reviews,
		"draft":   draft,
	})
}

type imageQueries interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reviewsOf(ctx context.Context, db imageQueries, imageID int64, version int) ([]ReviewDTO, error) {
	rows, err := db.Query(ctx,
		`SELECT r.id,r.action,r.comment,r.reason_tags,COALESCE(u.display_name,''),
		        COALESCE(r.grade_patch::text,''),r.created_at,r.annotation_version,'review'
		   FROM reviews r JOIN images i ON i.id=r.image_id
		   LEFT JOIN users u ON u.id=r.reviewer_id
		  WHERE r.image_id=$1 AND r.annotation_version>i.voided_through_version
		    AND ($2=0 OR r.annotation_version=$2)
		  UNION ALL
		 SELECT -d.id,CASE WHEN d.result='return' THEN 'reject' ELSE 'approve' END,
		        COALESCE(NULLIF(d.note,''),NULLIF(q.note,''),'仲裁已完成'),'{}',
		        COALESCE(u.display_name,''),'',d.finished_at,r.annotation_version,'arbitration'
		   FROM quality_disputes d JOIN quality_samples q ON q.id=d.sample_id
		   JOIN reviews r ON r.id=q.review_id JOIN images i ON i.id=q.image_id
		   LEFT JOIN users u ON u.id=d.arbiter_id
		  WHERE q.image_id=$1 AND d.result IS NOT NULL AND r.annotation_version>i.voided_through_version
		    AND ($2=0 OR r.annotation_version=$2)
		  ORDER BY created_at DESC`, imageID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDTO{}
	for rows.Next() {
		var rv ReviewDTO
		var patch string
		if err := rows.Scan(&rv.ID, &rv.Action, &rv.Comment, &rv.ReasonTags, &rv.Reviewer,
			&patch, &rv.CreatedAt, &rv.Version, &rv.Source); err != nil {
			return nil, err
		}
		if patch != "" && patch != "null" {
			var v any
			_ = json.Unmarshal([]byte(patch), &v)
			rv.GradePatch = v
		}
		if rv.ReasonTags == nil {
			rv.ReasonTags = []string{}
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

func (s *Server) handleHistory(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	row, err := s.imageByID(ctx, id)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if err := requireImageAccess(c, s.pool(), id, row.AnnotatorID); err != nil {
		httpx.Fail(c, err)
		return
	}
	rows, err := s.pool().Query(ctx,
		`SELECT v.version, COALESCE(u.display_name,''), v.payload::text, v.submitted_at
		   FROM annotation_versions v
		   JOIN images i ON i.id=v.image_id AND v.version>i.voided_through_version
		   LEFT JOIN users u ON u.id = v.annotator_id
		  WHERE v.image_id=$1 ORDER BY v.version DESC`, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	versions := []gin.H{}
	for rows.Next() {
		var (
			ver      int
			who, raw string
			at       time.Time
		)
		if err := rows.Scan(&ver, &who, &raw, &at); err != nil {
			httpx.Fail(c, err)
			return
		}
		var payload any
		_ = json.Unmarshal([]byte(raw), &payload)
		versions = append(versions, gin.H{"version": ver, "annotator": who, "payload": payload, "submitted_at": at})
	}
	reviews, err := reviewsOf(ctx, s.pool(), id, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"versions": versions, "reviews": reviews})
}

func (s *Server) handleDeleteImage(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	var status, relPath, thumb, preview string
	err = s.pool().QueryRow(ctx,
		`SELECT status, rel_path, COALESCE(thumb_path,''), COALESCE(preview_path,'') FROM images WHERE id=$1`, id).
		Scan(&status, &relPath, &thumb, &preview)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if status != "unassigned" {
		httpx.Fail(c, httpx.Conflict("图片已进入标注流程，不能删除"))
		return
	}
	tag, err := s.pool().Exec(ctx, `DELETE FROM images i WHERE i.id=$1 AND i.group_id IS NULL
		AND i.status='unassigned' AND `+undispatchedSQL, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.Conflict("图片已进入标注流程，不能删除"))
		return
	}
	_ = s.store.Remove(relPath)
	_ = s.store.Remove(thumb)
	_ = s.store.Remove(preview)
	s.audit(c, "image.delete", "image", id, nil)
	httpx.OK(c, gin.H{"ok": true})
}

func requireImageAccess(c *gin.Context, db imageQueries, id, annotatorID int64) error {
	me := auth.MustCurrent(c)
	switch me.Role {
	case "admin":
		return nil
	case "annotator":
		if me.ID == annotatorID {
			return nil
		}
	case "reviewer":
		var allowed bool
		err := db.QueryRow(c.Request.Context(), `SELECT `+reviewScope("$2", "$3")+` FROM images i WHERE i.id=$1`,
			id, me.Role, me.ID).Scan(&allowed)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
	}
	return httpx.Forbidden("你没有权限查看该任务")
}
