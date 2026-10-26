package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/imgproc"
)

var allowedExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "webp": true, "gif": true, "heic": true, "heif": true,
}

type uploadItem struct {
	File        string `json:"file"`
	OK          bool   `json:"ok"`
	ImageID     int64  `json:"image_id,omitempty"`
	StoredName  string `json:"stored_name,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Message     string `json:"message,omitempty"`
	DuplicateOf int64  `json:"duplicate_of,omitempty"`
}

func todaySH() (string, time.Time) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	now := time.Now().In(loc)
	return now.Format("20060102"), now
}

func (s *Server) handleUpload(c *gin.Context) {
	ctx := c.Request.Context()
	me := auth.MustCurrent(c)
	maxFiles := s.settingInt(ctx, "max_upload_files", 200)
	maxMB := s.settingInt(ctx, "max_file_mb", int(s.cfg.MaxFileMB))
	maxBytes := int64(maxMB) << 20

	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		httpx.Fail(c, httpx.BadRequest("上传表单解析失败："+err.Error()))
		return
	}
	form := c.Request.MultipartForm
	if form == nil {
		httpx.Fail(c, httpx.BadRequest("没有收到文件"))
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"]
	}
	if len(files) == 0 {
		httpx.Fail(c, httpx.BadRequest("请选择要上传的图片"))
		return
	}
	if len(files) > maxFiles {
		httpx.Fail(c, httpx.Validation(map[string]string{"files": fmt.Sprintf("一次最多 %d 张", maxFiles)},
			"文件数量超出上限"))
		return
	}

	var folderID int64
	if v := c.Request.FormValue("folder_id"); v != "" {
		fmt.Sscan(v, &folderID)
	}
	folderName := strings.TrimSpace(c.Request.FormValue("folder_name"))
	fid, fname, err := s.resolveFolder(ctx, folderID, folderName, me.ID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}

	dayStr, _ := todaySH()
	items := make([]uploadItem, 0, len(files))
	okCount, dupCount, failCount := 0, 0, 0

	for _, fh := range files {
		item := uploadItem{File: fh.Filename}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fh.Filename), "."))
		if !allowedExt[ext] {
			item.Reason, item.Message = "UNSUPPORTED_TYPE", "仅支持 jpg/png/webp/gif"
			items = append(items, item)
			failCount++
			continue
		}
		if ext == "heic" || ext == "heif" {
			item.Reason, item.Message = "UNSUPPORTED_TYPE", "HEIC 需先转成 JPG（服务器未启用 HEIC 转码）"
			items = append(items, item)
			failCount++
			continue
		}
		if fh.Size > maxBytes {
			item.Reason, item.Message = "FILE_TOO_LARGE", fmt.Sprintf("单张不能超过 %dMB", maxMB)
			items = append(items, item)
			failCount++
			continue
		}
		raw, err := readLimited(fh, maxBytes)
		if err != nil {
			item.Reason, item.Message = "READ_FAILED", err.Error()
			items = append(items, item)
			failCount++
			continue
		}
		sum := sha256.Sum256(raw)
		sha := hex.EncodeToString(sum[:])

		var dupID int64
		var dupName string
		if err := s.pool().QueryRow(ctx,
			`SELECT id, stored_name FROM images WHERE folder_id=$1 AND sha256=$2`, fid, sha).
			Scan(&dupID, &dupName); err == nil {
			item.Reason, item.DuplicateOf = "DUPLICATE", dupID
			item.Message = "已存在同图 " + dupName
			items = append(items, item)
			dupCount++
			continue
		}

		proc, err := imgproc.Process(raw, ext)
		if err != nil {
			item.Reason, item.Message = "UNSUPPORTED_TYPE", err.Error()
			items = append(items, item)
			failCount++
			continue
		}
		seq, err := s.nextSeq(ctx, dayStr)
		if err != nil {
			item.Reason, item.Message = "INTERNAL", "序号分配失败"
			items = append(items, item)
			failCount++
			continue
		}
		storedName := fmt.Sprintf("%s_%03d.%s", dayStr, seq, proc.Ext)
		relPath := path.Join(fname, storedName)
		base := strings.TrimSuffix(storedName, "."+proc.Ext)
		thumbRel := path.Join(fname, ".derived", base+".thumb.jpg")
		previewRel := path.Join(fname, ".derived", base+".preview.jpg")

		if err := s.store.Write(relPath, proc.Original); err != nil {
			item.Reason, item.Message = "STORAGE_FAILED", err.Error()
			items = append(items, item)
			failCount++
			continue
		}
		if err := s.store.Write(previewRel, proc.Preview); err != nil {
			_ = s.store.Remove(relPath)
			item.Reason, item.Message = "STORAGE_FAILED", err.Error()
			items = append(items, item)
			failCount++
			continue
		}
		if err := s.store.Write(thumbRel, proc.Thumb); err != nil {
			_ = s.store.Remove(relPath)
			_ = s.store.Remove(previewRel)
			item.Reason, item.Message = "STORAGE_FAILED", err.Error()
			items = append(items, item)
			failCount++
			continue
		}

		var imageID int64
		err = s.pool().QueryRow(ctx,
			`WITH target_folder AS (
			    SELECT id FROM folders WHERE id=$1 AND archived_at IS NULL FOR SHARE
			 )
			 INSERT INTO images (folder_id, stored_name, day, seq, orig_name, ext, mime, bytes, width, height,
			                    sha256, rel_path, thumb_path, preview_path, uploaded_by)
			 SELECT id,$2,$3::date,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15 FROM target_folder
			 RETURNING id`,
			fid, storedName, dayStr, seq, fh.Filename, proc.Ext, mimeOf(proc.Ext),
			int64(len(proc.Original)), proc.Width, proc.Height, sha, relPath, thumbRel, previewRel, me.ID).
			Scan(&imageID)
		if err != nil {
			_ = s.store.Remove(relPath)
			_ = s.store.Remove(previewRel)
			_ = s.store.Remove(thumbRel)
			if errors.Is(err, pgx.ErrNoRows) {
				item.Reason, item.Message = "FOLDER_ARCHIVED", "文件夹已归档，请取消归档后再上传"
				failCount++
			} else if isUnique(err) {
				item.Reason, item.Message = "DUPLICATE", "同图已存在"
				dupCount++
			} else {
				slog.Error("insert image", "err", err)
				item.Reason, item.Message = "INTERNAL", "入库失败"
				failCount++
			}
			items = append(items, item)
			continue
		}

		item.OK, item.ImageID, item.StoredName = true, imageID, storedName
		item.Width, item.Height = proc.Width, proc.Height
		items = append(items, item)
		okCount++
	}

	s.audit(c, "image.upload", "folder", fid, map[string]any{
		"folder": fname, "ok": okCount, "duplicate": dupCount, "failed": failCount,
	})
	httpx.OK(c, gin.H{
		"folder":  gin.H{"id": fid, "name": fname},
		"summary": gin.H{"ok": okCount, "duplicate": dupCount, "failed": failCount},
		"items":   items,
	})
}

func (s *Server) resolveFolder(ctx context.Context, folderID int64, name string, actor int64) (int64, string, error) {
	if folderID > 0 {
		var n string
		var archived bool
		if err := s.pool().QueryRow(ctx, `SELECT name, archived_at IS NOT NULL FROM folders WHERE id=$1`, folderID).Scan(&n, &archived); err != nil {
			return 0, "", httpx.NotFound("文件夹不存在")
		}
		if archived {
			return 0, "", httpx.Conflict("文件夹已归档，请取消归档后再上传")
		}
		return folderID, n, nil
	}
	if name == "" {
		name, _ = todaySH()
	}
	if !folderNameRe.MatchString(name) {
		return 0, "", httpx.Validation(map[string]string{"folder_name": "1~64 字符，不能含 / \\ : * ? \" < > |"}, "文件夹名称不合法")
	}
	var id int64
	err := s.pool().QueryRow(ctx,
		`INSERT INTO folders (name, created_by) VALUES ($1,$2)
		 ON CONFLICT (lower(name)) DO NOTHING RETURNING id`, name, actor).Scan(&id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, "", err
		}
		var archived bool
		if err := s.pool().QueryRow(ctx,
			`SELECT id, name, archived_at IS NOT NULL FROM folders WHERE lower(name)=lower($1)`, name).Scan(&id, &name, &archived); err != nil {
			return 0, "", err
		}
		if archived {
			return 0, "", httpx.Conflict("文件夹已归档，请取消归档后再上传")
		}
	}
	_ = os.MkdirAll(filepath.Join(s.store.Root(), name), 0o755)
	return id, name, nil
}

func (s *Server) nextSeq(ctx context.Context, day string) (int, error) {
	var seq int
	err := s.pool().QueryRow(ctx,
		`INSERT INTO daily_counters (day, last_seq) VALUES ($1::date, 1)
		 ON CONFLICT (day) DO UPDATE SET last_seq = daily_counters.last_seq + 1
		 RETURNING last_seq`, day).Scan(&seq)
	return seq, err
}

func readLimited(fh *multipart.FileHeader, max int64) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > max {
		return nil, errors.New("文件过大")
	}
	return buf, nil
}

func mimeOf(ext string) string {
	if ext == "png" {
		return "image/png"
	}
	return "image/jpeg"
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
