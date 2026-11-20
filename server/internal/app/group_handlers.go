package app

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

const groupModeSQL = `EXISTS (SELECT 1 FROM annotation_groups)`
const undispatchedSQL = `NOT EXISTS (SELECT 1 FROM images task WHERE task.source_image_id=i.id)`
const workflowImageSQL = `(i.status <> 'unassigned' OR ` + undispatchedSQL + `)`

// Group tasks can only be reviewed by a reviewer who currently belongs to
// their group. Admins can finish the pre-group workflow.
const reviewScopeSQL = `(
	(i.group_id IS NULL AND ($ROLE='admin' OR NOT ` + groupModeSQL + `))
	OR (i.group_id IS NOT NULL AND $ROLE='reviewer' AND EXISTS (
		SELECT 1 FROM annotation_group_members gm WHERE gm.group_id=i.group_id AND gm.user_id=$USER
	)))`

func reviewScope(roleParam, userParam string) string {
	return strings.NewReplacer("$ROLE", roleParam+"::text", "$USER", userParam+"::bigint").Replace(reviewScopeSQL)
}

// Serialize group membership edits, dispatch, and changes to users' eligibility.
func lockGroupManagement(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(732140, 1)`)
	return err
}

func (s *Server) handleListGroups(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(), `
		SELECT g.id,g.name,g.archived,
		       COALESCE(array_agg(gm.user_id ORDER BY gm.user_id) FILTER (WHERE gm.user_id IS NOT NULL),'{}')
		FROM annotation_groups g LEFT JOIN annotation_group_members gm ON gm.group_id=g.id
		GROUP BY g.id ORDER BY g.archived,g.name,g.id`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	groups := []gin.H{}
	for rows.Next() {
		var id int64
		var name string
		var archived bool
		var members []int64
		if err := rows.Scan(&id, &name, &archived, &members); err != nil {
			httpx.Fail(c, err)
			return
		}
		groups = append(groups, gin.H{"id": id, "name": name, "archived": archived, "member_ids": members})
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": groups})
}

func (s *Server) handleSaveGroup(c *gin.Context) {
	var id int64
	if c.Param("id") != "" {
		var err error
		id, err = httpx.ParamInt64(c, "id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	var input struct {
		Name      string  `json:"name"`
		MemberIDs []int64 `json:"member_ids"`
		Archived  bool    `json:"archived"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if n := utf8.RuneCountInString(input.Name); n == 0 || n > 64 {
		httpx.Fail(c, httpx.BadRequest("分组名称须为 1 至 64 个字符"))
		return
	}
	if input.MemberIDs == nil {
		input.MemberIDs = []int64{}
	}
	ctx := c.Request.Context()
	creating := id == 0
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockGroupManagement(ctx, tx); err != nil {
			return err
		}
		var invalid, unfinished, orphaned bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM unnest($2::bigint[]) member(id)
			     LEFT JOIN users u ON u.id=member.id
			     WHERE u.id IS NULL OR u.role NOT IN ('annotator','reviewer')),
			       EXISTS (SELECT 1 FROM images WHERE group_id=$1
			           AND (quality_pending OR status NOT IN ('approved','excluded'))),
			       EXISTS (SELECT 1 FROM images WHERE group_id=$1
			           AND (quality_pending OR status NOT IN ('approved','excluded'))
			           AND NOT (annotator_id=ANY($2)))`, id, input.MemberIDs).
			Scan(&invalid, &unfinished, &orphaned); err != nil {
			return err
		}
		if invalid {
			return httpx.BadRequest("组内只能添加标注员和审核员")
		}
		if unfinished {
			if input.Archived || orphaned {
				return httpx.Conflict("请先完成未结任务，再归档或移除负责的标注员")
			}
			var hasReviewer bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=ANY($1)
				AND role='reviewer' AND status='active')`, input.MemberIDs).Scan(&hasReviewer); err != nil {
				return err
			}
			if !hasReviewer {
				return httpx.Conflict("有未结任务的组须保留至少一名启用的审核员")
			}
		}
		if creating {
			if err := tx.QueryRow(ctx, `INSERT INTO annotation_groups(name,archived) VALUES ($1,$2) RETURNING id`,
				input.Name, input.Archived).Scan(&id); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE annotation_groups SET name=$2,archived=$3 WHERE id=$1`,
				id, input.Name, input.Archived)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.NotFound("分组不存在")
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM annotation_group_members WHERE group_id=$1 AND NOT (user_id=ANY($2))`,
			id, input.MemberIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO annotation_group_members(group_id,user_id)
			SELECT $1,member FROM unnest($2::bigint[]) member ON CONFLICT DO NOTHING`, id, input.MemberIDs); err != nil {
			return err
		}
		action := "group.update"
		if creating {
			action = "group.create"
		}
		return s.insertAudit(c, tx, action, "group", id, map[string]any{
			"name": input.Name, "member_ids": input.MemberIDs, "archived": input.Archived,
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "uq_annotation_group_name" {
			err = httpx.Conflict("分组名称已存在")
		}
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

func (s *Server) handleDispatchGroups(c *gin.Context) {
	var input struct {
		GroupIDs []int64 `json:"group_ids"`
		ImageIDs []int64 `json:"image_ids"`
		FolderID int64   `json:"folder_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.GroupIDs) == 0 ||
		(len(input.ImageIDs) == 0) == (input.FolderID == 0) || input.FolderID < 0 {
		httpx.Fail(c, httpx.BadRequest("请选择目标分组，以及图片或一个完整文件夹"))
		return
	}
	if input.ImageIDs == nil {
		input.ImageIDs = []int64{}
	}
	ctx := c.Request.Context()
	var total int64
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockGroupManagement(ctx, tx); err != nil {
			return err
		}
		// Hold shared folder locks so that a concurrent archive cannot hide
		// newly dispatched work, including when images were picked one by one.
		folders, err := tx.Query(ctx, `SELECT f.id FROM folders f WHERE f.id=$1 OR f.id IN (
			SELECT folder_id FROM images WHERE id=ANY($2)
		) ORDER BY f.id FOR SHARE`, input.FolderID, input.ImageIDs)
		if err != nil {
			return err
		}
		if _, err := pgx.CollectRows(folders, pgx.RowTo[int64]); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT i.id FROM images i JOIN folders f ON f.id=i.folder_id
			WHERE i.group_id IS NULL AND f.archived_at IS NULL AND
			    (($1::bigint > 0 AND i.folder_id=$1) OR i.id=ANY($2))
			ORDER BY i.sample_no FOR UPDATE OF i`, input.FolderID, input.ImageIDs)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(ids) == 0 || (input.FolderID == 0 && len(ids) != distinctIDs(input.ImageIDs)) {
			return httpx.BadRequest("所选图片不存在或所在文件夹已归档")
		}
		rows, err = tx.Query(ctx, `SELECT id FROM annotation_groups WHERE id=ANY($1) AND NOT archived ORDER BY id`, input.GroupIDs)
		if err != nil {
			return err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(groups) != distinctIDs(input.GroupIDs) {
			return httpx.BadRequest("请选择有效且未归档的分组")
		}
		for _, groupID := range groups {
			count, err := dispatchGroup(ctx, tx, groupID, ids)
			if err != nil {
				return err
			}
			total += count
			detail := map[string]any{"folder_id": input.FolderID, "created": count}
			if input.FolderID == 0 {
				detail["image_ids"] = ids
			}
			if err := s.insertAudit(c, tx, "group.dispatch", "group", groupID, detail); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"created": total})
}

func distinctIDs(ids []int64) int {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}

func dispatchGroup(ctx context.Context, tx pgx.Tx, groupID int64, imageIDs []int64) (int64, error) {
	var annotators []int64
	var reviewers int
	var cursor int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(array_agg(u.id ORDER BY u.id) FILTER (WHERE u.role='annotator'),'{}'),
		       count(*) FILTER (WHERE u.role='reviewer'),g.dispatch_count
		FROM annotation_groups g JOIN annotation_group_members m ON m.group_id=g.id
		JOIN users u ON u.id=m.user_id AND u.status='active'
		WHERE g.id=$1 GROUP BY g.id`, groupID).Scan(&annotators, &reviewers, &cursor); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if len(annotators) == 0 || reviewers == 0 {
		return 0, httpx.Conflict("下发前每个组须有至少一名启用的标注员和审核员")
	}
	tag, err := tx.Exec(ctx, `
		WITH pending AS (
			SELECT src.*, row_number() OVER (ORDER BY src.sample_no) AS position
			FROM images src WHERE src.id=ANY($2) AND NOT EXISTS (
				SELECT 1 FROM images task WHERE task.source_image_id=src.id AND task.group_id=$1
			)
		)
		INSERT INTO images (folder_id,stored_name,day,seq,orig_name,ext,mime,bytes,width,height,sha256,
			rel_path,thumb_path,preview_path,uploaded_by,created_at, status, annotator_id, claimed_at,
			group_id, source_image_id, sample_no)
		SELECT p.folder_id,p.stored_name,p.day,p.seq,p.orig_name,p.ext,p.mime,p.bytes,p.width,p.height,p.sha256,
			p.rel_path,p.thumb_path,p.preview_path,p.uploaded_by,p.created_at,'assigned',
			($3::bigint[])[1+(($4::bigint+p.position-1) % cardinality($3))],now(),$1,p.id,p.sample_no
		FROM pending p ORDER BY p.position`, groupID, imageIDs, annotators, cursor)
	if err != nil {
		return 0, err
	}
	count := tag.RowsAffected()
	_, err = tx.Exec(ctx, `UPDATE annotation_groups SET dispatch_count=dispatch_count+$2 WHERE id=$1`, groupID, count)
	return count, err
}

func (s *Server) groupedWorkflow(c *gin.Context) (bool, error) {
	var enabled bool
	err := s.pool().QueryRow(c.Request.Context(), `SELECT `+groupModeSQL).Scan(&enabled)
	return enabled, err
}

func (s *Server) handleWorkMode(c *gin.Context) {
	grouped, err := s.groupedWorkflow(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"grouped": grouped})
}
