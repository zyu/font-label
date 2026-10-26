package app

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

type claimedImage struct {
	ID         int64
	FolderID   int64
	StoredName string
	Seq        int
}

const claimPickSQL = `
WITH picked AS (
    SELECT id FROM images
     WHERE status = 'unassigned'
       AND ($1::bigint = 0 OR folder_id = $1)
     ORDER BY folder_id, seq
     LIMIT $2
     FOR UPDATE SKIP LOCKED
)
UPDATE images i
   SET status='assigned', annotator_id=$3, claimed_at=now(),
       lock_expires_at = now() + make_interval(hours => $4), updated_at=now()
  FROM picked p
 WHERE i.id = p.id
RETURNING i.id, i.folder_id, i.stored_name, i.seq`

func (s *Server) handleClaim(c *gin.Context) {
	var in struct {
		Mode     string `json:"mode"`
		Count    int    `json:"count"`
		FolderID int64  `json:"folder_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()

	maxHold := s.settingInt(ctx, "max_hold_per_user", s.cfg.MaxHold)
	maxCount := s.settingInt(ctx, "max_claim_count", 100)
	ttlHours := s.settingInt(ctx, "lock_ttl_hours", 24)

	var hold int
	if err := s.pool().QueryRow(ctx,
		`SELECT count(*) FROM images WHERE annotator_id=$1 AND status='assigned'`, me.ID).Scan(&hold); err != nil {
		httpx.Fail(c, err)
		return
	}
	if hold >= maxHold {
		httpx.Fail(c, httpx.LimitExceeded("你已持有 "+itoa(hold)+" 个未完成任务（上限 "+itoa(maxHold)+"），请先完成或放弃"))
		return
	}

	switch in.Mode {
	case "folder":
		if in.FolderID <= 0 {
			httpx.Fail(c, httpx.Validation(map[string]string{"folder_id": "请选择文件夹"}, "缺少文件夹"))
			return
		}
	case "count":
		if in.Count <= 0 {
			in.Count = s.settingInt(ctx, "claim_default_count", 10)
		}
		if in.Count > maxCount {
			httpx.Fail(c, httpx.Validation(map[string]string{"count": "单次最多 " + itoa(maxCount) + " 张"}, "领取数量超出上限"))
			return
		}
		if in.Count > maxHold-hold {
			in.Count = maxHold - hold
		}
	default:
		httpx.Fail(c, httpx.Validation(map[string]string{"mode": "count 或 folder"}, "领取模式不合法"))
		return
	}

	// 文件夹模式也是「领到持有上限为止」：整包领取不能绕过 max_hold_per_user
	limit := in.Count
	if in.Mode == "folder" {
		limit = maxHold - hold
		if limit <= 0 {
			httpx.Fail(c, httpx.LimitExceeded("你已持有 "+itoa(hold)+" 个未完成任务（上限 "+itoa(maxHold)+"），请先完成或放弃"))
			return
		}
	}
	var claimed []claimedImage
	var claimID int64
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if in.Mode == "folder" {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, in.FolderID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, claimPickSQL, in.FolderID, limit, me.ID, ttlHours)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c claimedImage
			if err := rows.Scan(&c.ID, &c.FolderID, &c.StoredName, &c.Seq); err != nil {
				rows.Close()
				return err
			}
			claimed = append(claimed, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(claimed))
		for _, c := range claimed {
			ids = append(ids, c.ID)
		}
		var folderArg any
		if in.FolderID > 0 {
			folderArg = in.FolderID
		}
		return tx.QueryRow(ctx,
			`INSERT INTO claims (user_id, mode, requested, granted, folder_id, image_ids)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			me.ID, in.Mode, in.Count, len(claimed), folderArg, ids).Scan(&claimID)
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}

	var remaining int
	_ = s.pool().QueryRow(ctx,
		`SELECT count(*) FROM images WHERE status='unassigned' AND ($1::bigint = 0 OR folder_id=$1)`,
		in.FolderID).Scan(&remaining)

	s.audit(c, "claim.grant", "claim", claimID, map[string]any{
		"mode": in.Mode, "granted": len(claimed), "folder_id": in.FolderID,
	})

	out := make([]gin.H, 0, len(claimed))
	for _, c := range claimed {
		out = append(out, gin.H{
			"id": c.ID, "folder_id": c.FolderID, "stored_name": c.StoredName,
			"seq": c.Seq, "thumb_url": s.signURL(c.ID, "thumb"), "preview_url": s.signURL(c.ID, "preview"),
		})
	}
	httpx.OK(c, gin.H{
		"granted":           len(claimed),
		"requested":         in.Count,
		"remaining_in_pool": remaining,
		"images":            out,
		"hold":              gin.H{"current": hold + len(claimed), "limit": maxHold},
	})
}

func (s *Server) handleRelease(c *gin.Context) {
	var in struct {
		ImageIDs []int64 `json:"image_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || len(in.ImageIDs) == 0 {
		httpx.Fail(c, httpx.BadRequest("请选择要放弃的图片"))
		return
	}
	me := auth.MustCurrent(c)
	tag, err := s.pool().Exec(c.Request.Context(),
		`UPDATE images SET status='unassigned', annotator_id=NULL, claimed_at=NULL,
		        lock_expires_at=NULL, updated_at=now()
		  WHERE id = ANY($1) AND annotator_id=$2 AND status='assigned'`,
		in.ImageIDs, me.ID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	_, _ = s.pool().Exec(c.Request.Context(),
		`UPDATE claims SET released_at=now(), release_reason='manual'
		  WHERE user_id=$1 AND released_at IS NULL AND image_ids && $2`, me.ID, in.ImageIDs)
	s.audit(c, "claim.release", "image", 0, map[string]any{"count": tag.RowsAffected()})
	httpx.OK(c, gin.H{"released": tag.RowsAffected()})
}

func (s *Server) handleMyTasks(c *gin.Context) {
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	status := c.DefaultQuery("status", "active")
	page, size := httpx.Paging(c)

	clause := ""
	switch status {
	case "active":
		clause = "i.annotator_id=$1 AND i.status='assigned'"
	case "rejected":
		clause = "i.annotator_id=$1 AND i.status='rejected'"
	case "submitted":
		clause = "i.annotator_id=$1 AND i.status='submitted'"
	case "approved":
		clause = "i.annotator_id=$1 AND i.status IN ('approved','excluded')"
	default:
		httpx.Fail(c, httpx.BadRequest("status 不合法"))
		return
	}

	var total int
	if err := s.pool().QueryRow(ctx,
		`SELECT count(*) FROM images i WHERE `+clause, me.ID).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	order := "i.claimed_at ASC"
	if status != "active" {
		order = "i.submitted_at DESC NULLS LAST"
	}
	rows, err := s.pool().Query(ctx,
		imageSelect+` WHERE `+clause+` ORDER BY `+order+` LIMIT $2 OFFSET $3`,
		me.ID, size, (page-1)*size)
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

func (s *Server) handleReassign(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var in struct {
		AnnotatorID *int64 `json:"annotator_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	ctx := c.Request.Context()
	var tag pgconn.CommandTag
	if in.AnnotatorID == nil {
		tag, err = s.pool().Exec(ctx,
			`UPDATE images SET status='unassigned', annotator_id=NULL, claimed_at=NULL,
			        lock_expires_at=NULL, updated_at=now()
			  WHERE id=$1 AND status IN ('assigned','rejected')`, id)
	} else {
		var role, status string
		if err := s.pool().QueryRow(ctx,
			`SELECT role, status FROM users WHERE id=$1`, *in.AnnotatorID).Scan(&role, &status); err != nil {
			httpx.Fail(c, httpx.NotFound("标注员不存在"))
			return
		}
		if status != "active" {
			httpx.Fail(c, httpx.Conflict("该账号已停用，不能改派"))
			return
		}
		if role == "reviewer" {
			httpx.Fail(c, httpx.Conflict("审核员不能作为标注员接收任务"))
			return
		}
		tag, err = s.pool().Exec(ctx,
			`UPDATE images SET annotator_id=$2,
			        status = CASE WHEN status='unassigned' THEN 'assigned' ELSE status END,
			        claimed_at = COALESCE(claimed_at, now()),
			        lock_expires_at = now() + make_interval(hours => $3),
			        updated_at=now()
			  WHERE id=$1 AND status IN ('unassigned','assigned','rejected')`,
			id, *in.AnnotatorID, s.settingInt(ctx, "lock_ttl_hours", 24))
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.Conflict("该图片当前状态不允许改派"))
		return
	}
	s.audit(c, "image.reassign", "image", id, map[string]any{"annotator_id": in.AnnotatorID})
	httpx.OK(c, gin.H{"ok": true})
}
