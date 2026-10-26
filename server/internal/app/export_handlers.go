package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/score"
	"github.com/shangyue/font-label/server/internal/platform/storage"
)

type exportFilters struct {
	Folders    []int64 `json:"folders"`
	Annotators []int64 `json:"annotators"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Status     string  `json:"status"`     // approved | reviewed | all
	DateField  string  `json:"date_field"` // created | submitted
}

type exportRow struct {
	ID         int64
	FolderID   int64
	Folder     string
	StoredName string
	OrigName   string
	Seq        int
	Annotator  string
	Submitted  *time.Time
	Scores     map[string]int
	// 五维各自的点评
	DimComments map[string]string
	Total       *float64
	Grade       string
	Comment     string
	Status      string
	Reviewer    string
	ReviewedAt  *time.Time
	RejectNum   int
	Exceptions  string
	RelPath     string
}

const exportSelect = `
SELECT f.name, i.stored_name, i.orig_name, i.seq, COALESCE(u.display_name,''),
       i.submitted_at, COALESCE(a.scores::text,'{}'), COALESCE(a.dim_comments::text,'{}'),
       a.total, COALESCE(a.grade,''),
       COALESCE(a.comment,''), i.status, COALESCE(rv.display_name,''), i.reviewed_at,
       i.reject_count, COALESCE(array_to_string(a.exception_reasons, '、'),''), i.rel_path,
       i.folder_id, i.id
  FROM images i
  JOIN folders f ON f.id = i.folder_id
  LEFT JOIN annotations a ON a.image_id = i.id
  LEFT JOIN users u ON u.id = i.annotator_id
  LEFT JOIN LATERAL (
        SELECT u2.display_name
          FROM reviews r JOIN users u2 ON u2.id = r.reviewer_id
         WHERE r.image_id = i.id ORDER BY r.created_at DESC LIMIT 1) rv ON true`

func scanExportRow(rows pgx.Rows) (exportRow, error) {
	var r exportRow
	var scores, dimComments string
	var total *float64
	var submitted, reviewed *time.Time
	if err := rows.Scan(&r.Folder, &r.StoredName, &r.OrigName, &r.Seq, &r.Annotator,
		&submitted, &scores, &dimComments, &total, &r.Grade, &r.Comment, &r.Status, &r.Reviewer,
		&reviewed, &r.RejectNum, &r.Exceptions, &r.RelPath, &r.FolderID, &r.ID); err != nil {
		return r, err
	}
	r.Submitted, r.ReviewedAt, r.Total = submitted, reviewed, total
	r.Scores = map[string]int{}
	if scores != "" {
		_ = json.Unmarshal([]byte(scores), &r.Scores)
	}
	r.DimComments = map[string]string{}
	if dimComments != "" && dimComments != "null" {
		_ = json.Unmarshal([]byte(dimComments), &r.DimComments)
	}
	return r, nil
}

func (f exportFilters) where() (string, []any) {
	clause := " i.id > 0"
	args := []any{}
	switch f.Status {
	case "all":
	case "reviewed":
		clause += " AND i.status IN ('approved','excluded','rejected')"
	default:
		clause += " AND i.status IN ('approved','excluded')"
	}
	if len(f.Folders) > 0 {
		args = append(args, f.Folders)
		clause += fmt.Sprintf(" AND i.folder_id = ANY($%d)", len(args))
	}
	if len(f.Annotators) > 0 {
		args = append(args, f.Annotators)
		clause += fmt.Sprintf(" AND i.annotator_id = ANY($%d)", len(args))
	}
	field := "i.created_at"
	if f.DateField == "submitted" {
		field = "i.submitted_at"
	}
	if f.From != "" {
		args = append(args, f.From)
		clause += fmt.Sprintf(" AND %s >= $%d::date", field, len(args))
	}
	if f.To != "" {
		args = append(args, f.To)
		clause += fmt.Sprintf(" AND %s < $%d::date + 1", field, len(args))
	}
	return clause, args
}

func (s *Server) exportRows(c *gin.Context, f exportFilters) ([]exportRow, error) {
	clause, args := f.where()
	rows, err := s.pool().Query(c.Request.Context(),
		exportSelect+" WHERE "+clause+" ORDER BY f.name, i.seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []exportRow{}
	for rows.Next() {
		r, err := scanExportRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Server) exportDir() string {
	return filepath.Join(s.store.Root(), ".exports")
}

func (s *Server) handleCreateExport(c *gin.Context) {
	var in struct {
		Format  string        `json:"format"`
		Filters exportFilters `json:"filters"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	switch in.Format {
	case "xlsx", "csv", "json", "jsonl":
	default:
		httpx.Fail(c, httpx.Validation(map[string]string{"format": "xlsx / csv / json / jsonl"}, "格式不合法"))
		return
	}
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()

	rows, err := s.exportRows(c, in.Filters)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if len(rows) == 0 {
		httpx.Fail(c, httpx.NotFound("没有符合条件的数据"))
		return
	}

	rubric, err := s.activeRubric(ctx)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	labels := s.exceptionLabels(ctx)
	rows = eligibleExportRows(in.Format, rows, rubric.Dims, labels)
	if len(rows) == 0 {
		httpx.Fail(c, httpx.NotFound("没有符合条件的训练样本（仅包含审核通过的评分和无效图）"))
		return
	}

	// Every export has a persistent record, including immediate downloads.
	filtersJSON, _ := json.Marshal(in.Filters)
	status := "running"
	if len(rows) > 5000 {
		status = "pending"
	}
	var jobID int64
	if err := s.pool().QueryRow(ctx,
		`INSERT INTO export_jobs (user_id, format, filters, status, row_count) VALUES ($1,$2,$3::jsonb,$4,$5) RETURNING id`,
		me.ID, in.Format, filtersJSON, status, len(rows)).Scan(&jobID); err != nil {
		httpx.Fail(c, err)
		return
	}
	if status == "pending" {
		s.audit(c, "export.enqueue", "export", jobID, map[string]any{"format": in.Format, "rows": len(rows)})
		httpx.OK(c, gin.H{"async": true, "job_id": jobID, "row_count": len(rows)})
		return
	}
	if err := s.writeExportFile(ctx, jobID, in.Format, rows, rubric.Dims, labels); err != nil {
		s.failExportJob(ctx, jobID, err)
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "export.download", "export", jobID, map[string]any{"format": in.Format, "rows": len(rows)})
	c.Header("Content-Type", exportContentType(in.Format))
	c.FileAttachment(s.exportPath(jobID, in.Format), fmt.Sprintf("font-label_%d.%s", jobID, exportExtension(in.Format)))
}

func exportExtension(format string) string {
	if format == "jsonl" {
		return "zip"
	}
	return format
}

func exportContentType(format string) string {
	switch format {
	case "jsonl":
		return "application/zip"
	case "csv":
		return "text/csv; charset=utf-8"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return "application/json; charset=utf-8"
	}
}

func (s *Server) exportPath(jobID int64, format string) string {
	return filepath.Join(s.exportDir(), fmt.Sprintf("export_%d.%s", jobID, exportExtension(format)))
}

func (s *Server) failExportJob(ctx context.Context, jobID int64, cause error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.pool().Exec(finishCtx, `UPDATE export_jobs SET status='failed', error=$2, finished_at=now() WHERE id=$1`, jobID, cause.Error()); err != nil {
		slog.Error("export failure status update failed", "id", jobID, "err", err)
	}
}

func eligibleExportRows(format string, rows []exportRow, dims []score.Dim, labels map[string]string) []exportRow {
	if format != "jsonl" {
		return rows
	}
	out := make([]exportRow, 0, len(rows))
	for _, row := range rows {
		if _, ok := assistantContent(row, dims, labels); ok {
			out = append(out, row)
		}
	}
	return out
}

// writeExportFile streams ZIP images to disk and publishes only a complete artifact.
func (s *Server) writeExportFile(ctx context.Context, jobID int64, format string, rows []exportRow, dims []score.Dim, labels map[string]string) error {
	if err := os.MkdirAll(s.exportDir(), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.exportDir(), ".export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	count := len(rows)
	if format == "jsonl" {
		count, err = renderTrainingZIP(file, rows, dims, labels, s.store)
	} else {
		var data []byte
		data, _, err = renderExport(format, rows, dims, labels)
		if err == nil {
			_, err = file.Write(data)
		}
	}
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("没有符合条件的导出数据")
	}
	if err := file.Close(); err != nil {
		return err
	}
	target := s.exportPath(jobID, format)
	if err := os.Rename(file.Name(), target); err != nil {
		return err
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = s.pool().Exec(finishCtx,
		`UPDATE export_jobs SET status='done', file_path=$2, row_count=$3,
		 finished_at=now(), expires_at=now() + interval '24 hours' WHERE id=$1`, jobID, target, count)
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}

func renderExport(format string, rows []exportRow, dims []score.Dim, labels map[string]string) ([]byte, string, error) {
	switch format {
	case "xlsx":
		b, err := renderXLSX(rows, dims)
		return b, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", err
	case "csv":
		b, err := renderCSV(rows, dims)
		return b, "text/csv; charset=utf-8", err
	default:
		b, err := renderJSON(rows)
		return b, "application/json; charset=utf-8", err
	}
}

// exceptionLabels 把异常原因的 key 映射成中文标签（训练集里要给中文）
func (s *Server) exceptionLabels(ctx context.Context) map[string]string {
	out := map[string]string{}
	raw, ok := s.settingValue(ctx, "exception_reasons").([]any)
	if !ok {
		return out
	}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k, _ := m["key"].(string)
		l, _ := m["label"].(string)
		if k != "" && l != "" {
			out[k] = l
		}
	}
	return out
}

// 训练集用：每行是一条多模态样本，格式与训练侧对齐
//
//	{"messages":[{"role":"user","content":"<image>请评价…"},{"role":"assistant","content":"白平衡（匀）：20/20\n…"}],
//	 "images":["images/<image_id>_<stored_name>"]}
const trainUserPrompt = "<image>请评价这位学员的硬笔楷书单字，并按五维给出评分与评语。"

// assistantContent 生成训练侧的答案文本；ok=false 表示这条没有可用于训练的标注（未审核/未提交）
func assistantContent(r exportRow, dims []score.Dim, labels map[string]string) (string, bool) {
	// 无效图：异常判定成立（excluded）
	if r.Status == "excluded" {
		reason := strings.TrimSpace(r.Exceptions)
		if reason == "" {
			reason = "图片不可用"
		} else {
			parts := strings.Split(reason, "、")
			for i, p := range parts {
				if l, ok := labels[strings.TrimSpace(p)]; ok && l != "" {
					parts[i] = l
				}
			}
			reason = strings.Join(parts, "、")
		}
		return "无效：" + reason + "，建议重拍", true
	}
	// 有效图：必须审核通过且五维齐全、有总分
	if r.Status != "approved" || r.Total == nil {
		return "", false
	}
	var b strings.Builder
	for _, d := range dims {
		v, ok := r.Scores[d.Key]
		if !ok {
			return "", false
		}
		max := d.Max
		if max <= 0 {
			max = 20
		}
		fmt.Fprintf(&b, "%s：%d/%d", d.Label, v, max)
		if text := strings.TrimSpace(r.DimComments[d.Key]); text != "" {
			fmt.Fprintf(&b, "；点评：%s", text)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "总分：%s/100\n", strconv.FormatFloat(*r.Total, 'f', -1, 64))
	fmt.Fprintf(&b, "档位：%s", r.Grade)
	return b.String(), true
}

// 训练侧的记录结构：字段顺序固定为 messages → images（与训练脚本对齐）
type trainMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type trainRecord struct {
	Messages []trainMessage `json:"messages"`
	Images   []string       `json:"images"`
}

func renderJSONL(rows []exportRow, dims []score.Dim, labels map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 中文不转义，训练集里可读
	for _, r := range rows {
		content, ok := assistantContent(r, dims, labels)
		if !ok {
			continue // 未审核通过的图不进训练集，避免噪声
		}
		rec := trainRecord{
			Messages: []trainMessage{
				{Role: "user", Content: trainUserPrompt},
				{Role: "assistant", Content: content},
			},
			Images: []string{trainingImagePath(r)},
		}
		if err := enc.Encode(rec); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// IDs are globally unique and stable across datasets; basename keeps the ZIP flat.
func trainingImagePath(row exportRow) string {
	name := path.Base(strings.ReplaceAll(row.StoredName, "\\", "/"))
	return fmt.Sprintf("images/%d_%s", row.ID, name)
}

func renderTrainingZIP(w io.Writer, rows []exportRow, dims []score.Dim, labels map[string]string, store *storage.Store) (int, error) {
	rows = eligibleExportRows("jsonl", rows, dims, labels)
	if len(rows) == 0 {
		return 0, fmt.Errorf("没有符合条件的训练样本")
	}
	archive := zip.NewWriter(w)
	defer archive.Close()
	data, err := renderJSONL(rows, dims, labels)
	if err != nil {
		return 0, err
	}
	manifest, err := archive.Create("data.jsonl")
	if err != nil {
		return 0, err
	}
	if _, err := manifest.Write(data); err != nil {
		return 0, err
	}
	for _, row := range rows {
		if err := appendTrainingImage(archive, row, store); err != nil {
			return 0, err
		}
	}
	if err := archive.Close(); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func appendTrainingImage(archive *zip.Writer, row exportRow, store *storage.Store) error {
	file, err := store.Open(row.RelPath)
	if err != nil {
		return fmt.Errorf("读取图片 %s: %w", row.StoredName, err)
	}
	defer file.Close()
	entry, err := archive.Create(trainingImagePath(row))
	if err != nil {
		return err
	}
	if _, err := io.Copy(entry, file); err != nil {
		return fmt.Errorf("打包图片 %s: %w", row.StoredName, err)
	}
	return nil
}

var baseHeaders = []string{
	"文件夹", "服务器文件名", "原始文件名", "序号", "标注员", "提交时间",
}

var tailHeaders = []string{
	"总分", "等级",
	"状态", "审核员", "审核时间", "驳回次数", "异常原因", "图片相对路径",
}

// exportHeaders 每个维度占两列：分数 + 该维度的点评（现行为五维各 20 分、各 10~50 字点评）
func exportHeaders(dims []score.Dim) []string {
	out := make([]string, 0, len(baseHeaders)+2*len(dims)+len(tailHeaders))
	out = append(out, baseHeaders...)
	for _, d := range dims {
		out = append(out, d.Label, d.Label+"点评")
	}
	return append(out, tailHeaders...)
}

func rowValues(r exportRow, dims []score.Dim) []string {
	num := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', 1, 64)
	}
	tm := func(v *time.Time) string {
		if v == nil {
			return ""
		}
		return v.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
	}
	statusLabel := map[string]string{
		"approved": "已通过", "excluded": "无效图", "rejected": "已打回",
		"submitted": "待审核", "assigned": "已领取", "unassigned": "待领取",
	}[r.Status]

	out := []string{
		r.Folder, r.StoredName, r.OrigName, strconv.Itoa(r.Seq), r.Annotator, tm(r.Submitted),
	}
	for _, d := range dims {
		if v, ok := r.Scores[d.Key]; ok {
			out = append(out, strconv.Itoa(v))
		} else {
			out = append(out, "")
		}
		out = append(out, r.DimComments[d.Key])
	}
	out = append(out,
		num(r.Total), r.Grade, statusLabel, r.Reviewer, tm(r.ReviewedAt),
		strconv.Itoa(r.RejectNum), r.Exceptions, r.RelPath,
	)
	return out
}

func renderXLSX(rows []exportRow, dims []score.Dim) ([]byte, error) {
	f := excelize.NewFile()
	sheet := "标注结果"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	headers := exportHeaders(dims)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return nil, err
		}
	}
	lastCol, _ := excelize.ColumnNumberToName(len(headers))
	headStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#EEF3FA"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err == nil {
		_ = f.SetCellStyle(sheet, "A1", lastCol+"1", headStyle)
	}
	for ri, r := range rows {
		for ci, v := range rowValues(r, dims) {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return nil, err
			}
		}
	}
	_ = f.SetColWidth(sheet, "A", "A", 16)
	_ = f.SetColWidth(sheet, "B", "C", 22)
	// 每个维度占两列（分数、点评）：分数列窄，点评列宽
	for i := range dims {
		if name, err := excelize.ColumnNumberToName(7 + 2*i); err == nil {
			_ = f.SetColWidth(sheet, name, name, 9)
		}
		if name, err := excelize.ColumnNumberToName(8 + 2*i); err == nil {
			_ = f.SetColWidth(sheet, name, name, 40)
		}
	}
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderCSV(rows []exportRow, dims []score.Dim) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	if err := w.Write(exportHeaders(dims)); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write(rowValues(r, dims)); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func renderJSON(rows []exportRow) ([]byte, error) {
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{
			"folder": r.Folder, "stored_name": r.StoredName, "orig_name": r.OrigName,
			"seq": r.Seq, "annotator": r.Annotator, "submitted_at": r.Submitted,
			"scores": r.Scores, "dim_comments": r.DimComments,
			"total": r.Total, "grade": r.Grade, "comment": r.Comment,
			"status": r.Status, "reviewer": r.Reviewer, "reviewed_at": r.ReviewedAt,
			"reject_count": r.RejectNum, "exception_reasons": r.Exceptions,
			"rel_path": r.RelPath,
		})
	}
	return json.MarshalIndent(items, "", "  ")
}

func (s *Server) handleListExports(c *gin.Context) {
	me := auth.MustCurrent(c)
	clause := "user_id=$1"
	args := []any{me.ID}
	if me.Role == "admin" {
		clause = "1=1"
		args = []any{}
	}
	rows, err := s.pool().Query(c.Request.Context(),
		`SELECT id, format, status, COALESCE(row_count,0), COALESCE(error,''), created_at, finished_at, expires_at
		   FROM export_jobs WHERE `+clause+` ORDER BY id DESC LIMIT 50`, args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id, rowsN  int64
			format     string
			status     string
			errText    string
			createdAt  time.Time
			finishedAt *time.Time
			expiresAt  *time.Time
		)
		if err := rows.Scan(&id, &format, &status, &rowsN, &errText, &createdAt, &finishedAt, &expiresAt); err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "format": format, "status": status, "row_count": rowsN,
			"error": errText, "created_at": createdAt, "finished_at": finishedAt, "expires_at": expiresAt,
		})
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

func (s *Server) handleDownloadExport(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	me := auth.MustCurrent(c)
	var (
		ownerID  int64
		format   string
		status   string
		filePath string
		expires  *time.Time
	)
	err = s.pool().QueryRow(c.Request.Context(),
		`SELECT user_id, format, status, COALESCE(file_path,''), expires_at FROM export_jobs WHERE id=$1`, id).
		Scan(&ownerID, &format, &status, &filePath, &expires)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("导出任务不存在"))
		return
	}
	if ownerID != me.ID && me.Role != "admin" {
		httpx.Fail(c, httpx.Forbidden("不能下载他人的导出文件"))
		return
	}
	if status != "done" || filePath == "" {
		httpx.Fail(c, httpx.Conflict("导出尚未完成"))
		return
	}
	if expires != nil && expires.Before(time.Now()) {
		httpx.Fail(c, httpx.Conflict("下载链接已过期，请重新导出"))
		return
	}
	f, err := os.Open(filePath)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("文件已清理，请重新导出"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	name := "font-label_" + strconv.FormatInt(id, 10) + "." + exportExtension(format)
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Header("Content-Type", exportContentType(format))
	http.ServeContent(c.Writer, c.Request, name, st.ModTime(), f)
}

// runExportJob renders a queued job to disk (called by the worker).
func (s *Server) runExportJob(jobID int64, userID int64, format string, filtersJSON []byte) error {
	var f exportFilters
	if err := json.Unmarshal(filtersJSON, &f); err != nil {
		return err
	}
	rubricForExport, err := s.activeRubric(contextBackground())
	if err != nil {
		return err
	}
	clause, args := f.where()
	rows, err := s.pool().Query(contextBackground(), exportSelect+" WHERE "+clause+" ORDER BY f.name, i.seq", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []exportRow{}
	for rows.Next() {
		r, err := scanExportRow(rows)
		if err != nil {
			return err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	out = eligibleExportRows(format, out, rubricForExport.Dims, s.exceptionLabels(contextBackground()))
	return s.writeExportFile(contextBackground(), jobID, format, out, rubricForExport.Dims, s.exceptionLabels(contextBackground()))
}
