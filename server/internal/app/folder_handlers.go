package app

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

const folderSelect = `
SELECT f.id, f.name, f.note, f.created_at, COALESCE(u.display_name,''), (f.archived_at IS NOT NULL),
       count(i.id)::int,
       count(i.id) FILTER (WHERE i.status='unassigned')::int,
       count(i.id) FILTER (WHERE i.status='assigned')::int,
       count(i.id) FILTER (WHERE i.status='submitted')::int,
       count(i.id) FILTER (WHERE i.status='approved')::int,
       count(i.id) FILTER (WHERE i.status='rejected')::int,
       count(i.id) FILTER (WHERE i.status='excluded')::int
  FROM folders f
  LEFT JOIN users u ON u.id = f.created_by
  LEFT JOIN images i ON i.folder_id = f.id`

func (s *Server) handleListFolders(c *gin.Context) {
	includeArchived := c.Query("include_archived") == "1"
	clause := " GROUP BY f.id, u.display_name ORDER BY f.id DESC"
	if !includeArchived {
		clause = " WHERE f.archived_at IS NULL" + clause
	}
	rows, err := s.pool().Query(c.Request.Context(), folderSelect+clause)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items, err := scanFolders(rows)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// handleClaimableFolders lists folders with remaining unassigned images.
func (s *Server) handleClaimableFolders(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(),
		folderSelect+` WHERE f.archived_at IS NULL
		 GROUP BY f.id, u.display_name
		 HAVING count(i.id) FILTER (WHERE i.status='unassigned') > 0
		 ORDER BY f.id DESC`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items, err := scanFolders(rows)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

func scanFolders(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]FolderDTO, error) {
	items := []FolderDTO{}
	for rows.Next() {
		var f FolderDTO
		if err := rows.Scan(&f.ID, &f.Name, &f.Note, &f.CreatedAt, &f.Creator, &f.Archived,
			&f.Total, &f.Unassigned, &f.Assigned, &f.Submitted, &f.Approved, &f.Rejected, &f.Excluded); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}

var folderNameRe = regexp.MustCompile(`^[^/\\:*?"<>|]{1,64}$`)

func (s *Server) handleCreateFolder(c *gin.Context) {
	var in struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !folderNameRe.MatchString(in.Name) {
		httpx.Fail(c, httpx.Validation(map[string]string{"name": "1~64 字符，不能包含 / \\ : * ? \" < > |"}, "文件夹名称不合法"))
		return
	}
	me := auth.MustCurrent(c)
	var id int64
	err := s.pool().QueryRow(c.Request.Context(),
		`INSERT INTO folders (name, note, created_by) VALUES ($1,$2,$3)
		 ON CONFLICT (lower(name)) DO NOTHING RETURNING id`, in.Name, in.Note, me.ID).Scan(&id)
	if err != nil {
		if err.Error() == "no rows in result set" {
			httpx.Fail(c, httpx.Conflict("文件夹名称已存在"))
			return
		}
		httpx.Fail(c, err)
		return
	}
	if err := os.MkdirAll(filepath.Join(s.store.Root(), in.Name), 0o755); err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "folder.create", "folder", id, map[string]any{"name": in.Name})
	httpx.OK(c, gin.H{"id": id, "name": in.Name})
}

func (s *Server) handleUpdateFolder(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Note     *string `json:"note"`
		Archived *bool   `json:"archived"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	var oldName, newName string
	if in.Name != nil {
		newName = strings.TrimSpace(*in.Name)
		if !folderNameRe.MatchString(newName) {
			httpx.Fail(c, httpx.Validation(map[string]string{"name": "名称不合法"}, "文件夹名称不合法"))
			return
		}
	}
	ctx := c.Request.Context()
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Uploads take a shared lock on this row before inserting an image.
		// Lock first, then check completion in a fresh statement snapshot.
		if err := tx.QueryRow(ctx, `SELECT name FROM folders WHERE id=$1 FOR UPDATE`, id).Scan(&oldName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.NotFound("文件夹不存在")
			}
			return err
		}
		if in.Name == nil {
			newName = oldName
		}
		if in.Archived != nil && *in.Archived {
			var incomplete bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM images WHERE folder_id=$1 AND status NOT IN ('approved','excluded')
			)`, id).Scan(&incomplete); err != nil {
				return err
			}
			if incomplete {
				return httpx.Conflict("文件夹内还有未处理完成的图片，全部审核通过或确认无效后才可归档")
			}
		}
		_, err := tx.Exec(ctx, `UPDATE folders SET name=$2, note=COALESCE($3,note), updated_at=now(),
			archived_at=CASE WHEN $4::boolean IS NULL THEN archived_at WHEN $4 THEN now() ELSE NULL END
			WHERE id=$1`, id, newName, in.Note, in.Archived)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			httpx.Fail(c, httpx.Conflict("文件夹名称已存在"))
			return
		}
		httpx.Fail(c, err)
		return
	}
	if newName != oldName {
		src := filepath.Join(s.store.Root(), oldName)
		dst := filepath.Join(s.store.Root(), newName)
		if _, statErr := os.Stat(src); statErr == nil {
			if err := os.Rename(src, dst); err != nil {
				httpx.Fail(c, httpx.New(http.StatusInternalServerError, "RENAME_FAILED", "目录改名失败："+err.Error()))
				return
			}
		}
		_, _ = s.pool().Exec(c.Request.Context(),
			`UPDATE images SET rel_path = replace(rel_path, $1 || '/', $2 || '/'),
			        thumb_path = replace(thumb_path, $1 || '/', $2 || '/'),
			        preview_path = replace(preview_path, $1 || '/', $2 || '/')
			  WHERE folder_id=$3`, oldName, newName, id)
	}
	s.audit(c, "folder.update", "folder", id, map[string]any{"name": newName})
	httpx.OK(c, gin.H{"ok": true})
}

func (s *Server) handleDeleteFolder(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var n int
	if err := s.pool().QueryRow(c.Request.Context(),
		`SELECT count(*) FROM images WHERE folder_id=$1`, id).Scan(&n); err != nil {
		httpx.Fail(c, err)
		return
	}
	if n > 0 {
		httpx.Fail(c, httpx.Conflict("文件夹内还有图片，只能归档不能删除"))
		return
	}
	tag, err := s.pool().Exec(c.Request.Context(), `DELETE FROM folders WHERE id=$1`, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.NotFound("文件夹不存在"))
		return
	}
	s.audit(c, "folder.delete", "folder", id, nil)
	httpx.OK(c, gin.H{"ok": true})
}
