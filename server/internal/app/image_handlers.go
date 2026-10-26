package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

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

	if me.Role == "annotator" {
		add(" AND i.annotator_id=$%d", me.ID)
	} else if aid := httpx.QueryInt64(c, "annotator_id"); aid > 0 {
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
		clause += fmt.Sprintf(" AND (i.stored_name ILIKE $%d OR i.orig_name ILIKE $%d)", n, n)
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
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	row, err := s.imageByID(ctx, id)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if me.Role == "annotator" && row.AnnotatorID != me.ID {
		httpx.Fail(c, httpx.Forbidden("这不是你的任务"))
		return
	}
	dto := s.toDTO(row)
	var draft any
	var raw string
	if err := s.pool().QueryRow(ctx,
		`SELECT payload::text FROM drafts WHERE user_id=$1 AND image_id=$2`, me.ID, id).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &draft)
	}
	reviews, err := s.reviewsOf(ctx, id)
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

func (s *Server) reviewsOf(ctx context.Context, imageID int64) ([]ReviewDTO, error) {
	rows, err := s.pool().Query(ctx,
		`SELECT r.id, r.action, COALESCE(r.comment,''), COALESCE(r.reason_tags,'{}'),
		        COALESCE(u.display_name,''), COALESCE(r.score_patch::text,''), r.created_at, r.annotation_version
		   FROM reviews r
		   LEFT JOIN users u ON u.id = r.reviewer_id
		  WHERE r.image_id = $1
		  ORDER BY r.created_at DESC`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDTO{}
	for rows.Next() {
		var rv ReviewDTO
		var patch string
		if err := rows.Scan(&rv.ID, &rv.Action, &rv.Comment, &rv.ReasonTags, &rv.Reviewer,
			&patch, &rv.CreatedAt, &rv.Version); err != nil {
			return nil, err
		}
		if patch != "" && patch != "null" {
			var v any
			_ = json.Unmarshal([]byte(patch), &v)
			rv.ScorePatch = v
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
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	row, err := s.imageByID(ctx, id)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if me.Role == "annotator" && row.AnnotatorID != me.ID {
		httpx.Fail(c, httpx.Forbidden("这不是你的任务"))
		return
	}
	rows, err := s.pool().Query(ctx,
		`SELECT v.version, COALESCE(u.display_name,''), v.payload::text, v.submitted_at
		   FROM annotation_versions v
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
	reviews, err := s.reviewsOf(ctx, id)
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
	if _, err := s.pool().Exec(ctx, `DELETE FROM images WHERE id=$1`, id); err != nil {
		httpx.Fail(c, err)
		return
	}
	_ = s.store.Remove(relPath)
	_ = s.store.Remove(thumb)
	_ = s.store.Remove(preview)
	s.audit(c, "image.delete", "image", id, nil)
	httpx.OK(c, gin.H{"ok": true})
}
