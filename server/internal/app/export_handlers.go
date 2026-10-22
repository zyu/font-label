package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/score"
)

type exportFilters struct {
	Folders    []int64 `json:"folders"`
	Annotators []int64 `json:"annotators"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Status     string  `json:"status"`      // approved | reviewed | all
	DateField  string  `json:"date_field"`  // created | submitted
}

type exportRow struct {
	FolderID   int64
	Folder     string
	StoredName string
	OrigName   string
	Seq        int
	Annotator  string
	Submitted  *time.Time
	Scores     map[string]int
	Total      *float64
	Grade      string
	Comment    string
	Status     string
	Reviewer   string
	ReviewedAt *time.Time
	RejectNum  int
	Exceptions string
	RelPath    string
}

const exportSelect = `
SELECT f.name, i.stored_name, i.orig_name, i.seq, COALESCE(u.display_name,''),
       i.submitted_at, COALESCE(a.scores::text,'{}'), a.total, COALESCE(a.grade,''),
       COALESCE(a.comment,''), i.status, COALESCE(rv.display_name,''), i.reviewed_at,
       i.reject_count, COALESCE(array_to_string(a.exception_reasons, '、'),''), i.rel_path,
       i.folder_id
  FROM images i
  JOIN folders f ON f.id = i.folder_id
  LEFT JOIN annotations a ON a.image_id = i.id
  LEFT JOIN users u ON u.id = i.annotator_id
  LEFT JOIN LATERAL (
        SELECT u2.display_name
          FROM reviews r JOIN users u2 ON u2.id = r.reviewer_id
         WHERE r.image_id = i.id ORDER BY r.created_at DESC LIMIT 1) rv ON true`

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
		var r exportRow
		var scores string
		var total *float64
		var submitted, reviewed *time.Time
		if err := rows.Scan(&r.Folder, &r.StoredName, &r.OrigName, &r.Seq, &r.Annotator,
			&submitted, &scores, &total, &r.Grade, &r.Comment, &r.Status, &r.Reviewer,
			&reviewed, &r.RejectNum, &r.Exceptions, &r.RelPath, &r.FolderID); err != nil {
			return nil, err
		}
		r.Submitted, r.ReviewedAt, r.Total = submitted, reviewed, total
		r.Scores = map[string]int{}
		if scores != "" {
			_ = json.Unmarshal([]byte(scores), &r.Scores)
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

	if len(rows) > 5000 {
		filtersJSON, _ := json.Marshal(in.Filters)
		var jobID int64
		err := s.pool().QueryRow(ctx,
			`INSERT INTO export_jobs (user_id, format, filters, status) VALUES ($1,$2,$3::jsonb,'pending')
			 RETURNING id`, me.ID, in.Format, filtersJSON).Scan(&jobID)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		s.audit(c, "export.enqueue", "export", jobID, map[string]any{"format": in.Format, "rows": len(rows)})
		httpx.OK(c, gin.H{"async": true, "job_id": jobID, "row_count": len(rows)})
		return
	}

	rubric, err := s.activeRubric(ctx)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	data, contentType, err := renderExport(in.Format, rows, rubric.Dims, s.exceptionLabels(ctx))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "export.download", "export", 0, map[string]any{"format": in.Format, "rows": len(rows)})
	name := "font-label_" + time.Now().Format("20060102_1504") + "." + in.Format
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Data(http.StatusOK, contentType, data)
}

func renderExport(format string, rows []exportRow, dims []score.Dim, labels map[string]string) ([]byte, string, error) {
	switch format {
	case "xlsx":
		b, err := renderXLSX(rows, dims)
		return b, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", err
	case "csv":
		b, err := renderCSV(rows, dims)
		return b, "text/csv; charset=utf-8", err
	case "jsonl":
		b, err := renderJSONL(rows, dims, labels)
		return b, "application/x-ndjson; charset=utf-8", err
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
//	 "images":["images/<folder_id>/<stored_name>"]}
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
		fmt.Fprintf(&b, "%s：%d/%d\n", d.Label, v, max)
	}
	fmt.Fprintf(&b, "总分：%s/100\n", strconv.FormatFloat(*r.Total, 'f', -1, 64))
	fmt.Fprintf(&b, "档位：%s\n", r.Grade)
	fmt.Fprintf(&b, "总评：%s", strings.TrimSpace(r.Comment))
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
			Images: []string{fmt.Sprintf("images/%d/%s", r.FolderID, r.StoredName)},
		}
		if err := enc.Encode(rec); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

var baseHeaders = []string{
	"文件夹", "服务器文件名", "原始文件名", "序号", "标注员", "提交时间",
}

var tailHeaders = []string{
	"总分", "等级", "点评",
	"状态", "审核员", "审核时间", "驳回次数", "异常原因", "图片相对路径",
}

// exportHeaders 把评分维度插在中间：维度由当前启用的方案决定（现行为五维各 20 分）
func exportHeaders(dims []score.Dim) []string {
	out := make([]string, 0, len(baseHeaders)+len(dims)+len(tailHeaders))
	out = append(out, baseHeaders...)
	for _, d := range dims {
		out = append(out, d.Label)
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
	}
	out = append(out,
		num(r.Total), r.Grade, r.Comment, statusLabel, r.Reviewer, tm(r.ReviewedAt),
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
	// 点评列：基础列 6 + 维度数 + 3（总分/等级/点评）
	commentCol, _ := excelize.ColumnNumberToName(6 + len(dims) + 3)
	_ = f.SetColWidth(sheet, commentCol, commentCol, 48)
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
			"scores": r.Scores, "total": r.Total, "grade": r.Grade, "comment": r.Comment,
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
		`SELECT id, format, status, row_count, COALESCE(error,''), created_at, finished_at, expires_at
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
	st, _ := f.Stat()
	name := "font-label_" + strconv.FormatInt(id, 10) + "." + format
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Header("Content-Type", "application/octet-stream")
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
		var r exportRow
		var scores string
		var total *float64
		var submitted, reviewed *time.Time
		if err := rows.Scan(&r.Folder, &r.StoredName, &r.OrigName, &r.Seq, &r.Annotator,
			&submitted, &scores, &total, &r.Grade, &r.Comment, &r.Status, &r.Reviewer,
			&reviewed, &r.RejectNum, &r.Exceptions, &r.RelPath, &r.FolderID); err != nil {
			return err
		}
		r.Submitted, r.ReviewedAt, r.Total = submitted, reviewed, total
		r.Scores = map[string]int{}
		if scores != "" {
			_ = json.Unmarshal([]byte(scores), &r.Scores)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	data, _, err := renderExport(format, out, rubricForExport.Dims, s.exceptionLabels(contextBackground()))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.exportDir(), 0o755); err != nil {
		return err
	}
	target := filepath.Join(s.exportDir(), fmt.Sprintf("export_%d.%s", jobID, format))
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return err
	}
	_, err = s.pool().Exec(contextBackground(),
		`UPDATE export_jobs SET status='done', file_path=$2, row_count=$3,
		        finished_at=now(), expires_at=now() + interval '24 hours' WHERE id=$1`,
		jobID, target, len(out))
	return err
}
