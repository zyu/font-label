package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
	Group          string
	SampleNo       int64
	QualityPending bool
	ID             int64
	FolderID       int64
	Folder         string
	StoredName     string
	OrigName       string
	Seq            int
	Annotator      string
	Submitted      *time.Time
	Grades         map[string]string
	// 五维各自的点评
	DimComments map[string]string
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
       i.submitted_at, COALESCE(a.grades::text,'{}'), COALESCE(a.dim_comments::text,'{}'),
       COALESCE(a.comment,''), i.status, COALESCE(rv.display_name,''), i.reviewed_at,
       i.reject_count, COALESCE(array_to_string(a.exception_reasons, '、'),''), i.rel_path,
       i.folder_id, i.id, COALESCE(g.name,''), i.sample_no, i.quality_pending
  FROM images i
  JOIN folders f ON f.id = i.folder_id
  LEFT JOIN annotation_groups g ON g.id=i.group_id
  LEFT JOIN annotations a ON a.image_id=i.id AND a.version>i.voided_through_version
  LEFT JOIN users u ON u.id = i.annotator_id
  LEFT JOIN LATERAL (
        SELECT u2.display_name
          FROM reviews r JOIN users u2 ON u2.id = r.reviewer_id
         WHERE r.image_id=i.id AND r.annotation_version>i.voided_through_version ORDER BY r.created_at DESC LIMIT 1) rv ON true`

func scanExportRow(rows pgx.Rows) (exportRow, error) {
	var r exportRow
	var grades, dimComments string
	var submitted, reviewed *time.Time
	if err := rows.Scan(&r.Folder, &r.StoredName, &r.OrigName, &r.Seq, &r.Annotator,
		&submitted, &grades, &dimComments, &r.Comment, &r.Status, &r.Reviewer,
		&reviewed, &r.RejectNum, &r.Exceptions, &r.RelPath, &r.FolderID, &r.ID, &r.Group, &r.SampleNo, &r.QualityPending); err != nil {
		return r, err
	}
	r.Submitted, r.ReviewedAt = submitted, reviewed
	r.Grades = map[string]string{}
	if grades != "" {
		_ = json.Unmarshal([]byte(grades), &r.Grades)
	}
	r.DimComments = map[string]string{}
	if dimComments != "" && dimComments != "null" {
		_ = json.Unmarshal([]byte(dimComments), &r.DimComments)
	}
	return r, nil
}

func (f exportFilters) where() (string, []any) {
	clause := " i.id > 0 AND " + workflowImageSQL
	args := []any{}
	switch f.Status {
	case "all":
		clause += " AND i.status<>'exception_locked' AND (i.voided_through_version=0 OR a.id IS NOT NULL)"
	case "reviewed":
		clause += " AND i.status IN ('approved','excluded','rejected') AND NOT i.quality_pending"
	default:
		clause += " AND i.status = 'approved' AND NOT i.quality_pending AND a.kind = 'score' AND a.grades <> '{}'::jsonb"
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

func (s *Server) exportDir() string {
	return filepath.Join(s.store.Root(), ".exports")
}

func eligibleExportRows(format string, rows []exportRow, dims []score.Dim) []exportRow {
	if format != "jsonl" {
		return rows
	}
	out := make([]exportRow, 0, len(rows))
	for _, row := range rows {
		if _, ok := assistantContent(row, dims); ok {
			out = append(out, row)
		}
	}
	return out
}

const trainUserPrompt = "<image>请评价这位学员的硬笔楷书单字，并给出五维 S/A/B/C/D 等级和各维评语。"

func assistantContent(r exportRow, dims []score.Dim) (string, bool) {
	if r.Status != "approved" || r.QualityPending || len(dims) != 5 {
		return "", false
	}
	lines := make([]string, 0, len(dims))
	for _, dim := range dims {
		level := r.Grades[dim.Key]
		if score.GradeRank(level) == 0 {
			return "", false
		}
		line := dim.Label + "：" + level
		if comment := strings.TrimSpace(r.DimComments[dim.Key]); comment != "" {
			line += "；点评：" + comment
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), true
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

func trainingRecord(content, path string) trainRecord {
	return trainRecord{
		Messages: []trainMessage{
			{Role: "user", Content: trainUserPrompt},
			{Role: "assistant", Content: content},
		},
		Images: []string{path},
	}
}

func renderJSONL(rows []exportRow, dims []score.Dim) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 中文不转义，训练集里可读
	for _, r := range rows {
		content, ok := assistantContent(r, dims)
		if !ok {
			continue // 未审核通过的图不进训练集，避免噪声
		}

		if err := enc.Encode(trainingRecord(content, trainingImagePath(r))); err != nil {
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

func renderTrainingZIP(w io.Writer, rows []exportRow, dims []score.Dim, store *storage.Store) (int, error) {
	rows = eligibleExportRows("jsonl", rows, dims)
	if len(rows) == 0 {
		return 0, fmt.Errorf("没有符合条件的训练样本")
	}
	archive := zip.NewWriter(w)
	defer archive.Close()
	data, err := renderJSONL(rows, dims)
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
		if err := appendArchiveImage(archive, store, row.RelPath, trainingImagePath(row), zip.Deflate); err != nil {
			return 0, err
		}
	}
	if err := archive.Close(); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func appendArchiveImage(archive *zip.Writer, store *storage.Store, source, name string, method uint16) error {
	file, err := store.Open(source)
	if err != nil {
		return fmt.Errorf("读取图片 %s: %w", path.Base(name), err)
	}
	defer file.Close()
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		return err
	}
	if _, err := io.Copy(entry, file); err != nil {
		return fmt.Errorf("打包图片 %s: %w", path.Base(name), err)
	}
	return nil
}

var baseHeaders = []string{
	"文件夹", "服务器文件名", "原始文件名", "序号", "标注员", "提交时间",
}

var tailHeaders = []string{
	"状态", "审核员", "审核时间", "驳回次数", "异常原因", "图片相对路径", "样本编号", "分组",
}

// exportHeaders 为每个维度提供独立的等级和评语列。
func exportHeaders(dims []score.Dim) []string {
	out := make([]string, 0, len(baseHeaders)+2*len(dims)+len(tailHeaders))
	out = append(out, baseHeaders...)
	for _, d := range dims {
		out = append(out, d.Label+"等级", d.Label+"点评")
	}
	return append(out, tailHeaders...)
}

func rowValues(r exportRow, dims []score.Dim) []string {
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
	if r.QualityPending {
		statusLabel = "待质检或仲裁"
	}

	out := []string{
		r.Folder, r.StoredName, r.OrigName, strconv.Itoa(r.Seq), r.Annotator, tm(r.Submitted),
	}
	for _, d := range dims {
		out = append(out, r.Grades[d.Key], r.DimComments[d.Key])
	}
	out = append(out,
		statusLabel, r.Reviewer, tm(r.ReviewedAt),
		strconv.Itoa(r.RejectNum), r.Exceptions, r.RelPath, fmt.Sprintf("P%06d", r.SampleNo), r.Group,
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

func exportJSONRecord(r exportRow) gin.H {
	return gin.H{
		"folder": r.Folder, "stored_name": r.StoredName, "orig_name": r.OrigName,
		"seq": r.Seq, "annotator": r.Annotator, "submitted_at": r.Submitted,
		"grades": r.Grades, "dim_comments": r.DimComments,
		"exception_note": r.Comment,
		"status":         r.Status, "reviewer": r.Reviewer, "reviewed_at": r.ReviewedAt,
		"reject_count": r.RejectNum, "exception_reasons": r.Exceptions,
		"rel_path": r.RelPath, "sample_id": fmt.Sprintf("P%06d", r.SampleNo), "group": r.Group,
		"quality_pending": r.QualityPending,
	}
}

func renderJSON(rows []exportRow) ([]byte, error) {
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, exportJSONRecord(r))
	}
	return json.MarshalIndent(items, "", "  ")
}

func (s *Server) handleListExports(c *gin.Context) {
	me := auth.MustCurrent(c)
	page, size := listPaging(c)
	ctx := c.Request.Context()
	clause := "destination='oss' AND ($1 OR user_id=$2)"
	args := []any{me.Role == "admin", me.ID}
	var total int
	if err := s.pool().QueryRow(ctx, "SELECT count(*) FROM export_jobs WHERE "+clause, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool().Query(c.Request.Context(),
		`SELECT id, format, status, COALESCE(row_count,0), COALESCE(error,''), created_at, finished_at, expires_at,
		        destination, COALESCE(object_key,''), COALESCE(object_url,'')
		   FROM export_jobs WHERE `+clause+` ORDER BY id DESC LIMIT $3 OFFSET $4`, args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id, rowsN                         int64
			format                            string
			status                            string
			errText                           string
			destination, objectKey, objectURL string
			createdAt                         time.Time
			finishedAt                        *time.Time
			expiresAt                         *time.Time
		)
		if err := rows.Scan(&id, &format, &status, &rowsN, &errText, &createdAt, &finishedAt, &expiresAt, &destination, &objectKey, &objectURL); err != nil {
			httpx.Fail(c, err)
			return
		}
		item := gin.H{
			"id": id, "format": format, "status": status, "row_count": rowsN,
			"error": errText, "created_at": createdAt, "finished_at": finishedAt, "expires_at": expiresAt,
			"destination": destination,
		}
		if destination == "oss" && status == "done" {
			item["object_key"] = objectKey
			item["object_url"] = objectURL
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size, "oss_enabled": me.Role == "admin" && s.ossExportEnabled()})
}
