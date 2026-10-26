package score

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Dim is one scoring dimension (现行口径：白平衡/黑平衡/参差/结构/控笔，各 20 分，步长 5).
type Dim struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Weight float64 `json:"weight"`
	Max    int     `json:"max"`
	// Step 限制可取值（0/5/10/15/20 即 Step=5）；0 或 1 表示任意整数
	Step int `json:"step,omitempty"`
}

// Band maps a minimum total to a grade label.
type Band struct {
	Label string  `json:"label"`
	Min   float64 `json:"min"`
}

// Rubric is the configurable scoring scheme.
type Rubric struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Dims       []Dim  `json:"dims"`
	Bands      []Band `json:"grade_bands"`
	CommentMin int    `json:"comment_min"`
	CommentMax int    `json:"comment_max"`
	// 每维点评的字数范围（现行口径：每维 10~50 字）
	DimCommentMin  int  `json:"dim_comment_min"`
	DimCommentMax  int  `json:"dim_comment_max"`
	AllowTotalOver bool `json:"allow_total_override"`
}

// Total computes the weighted 0-100 score and its grade label.
func (r Rubric) Total(scores map[string]int) (float64, string) {
	var sum float64
	var weight float64
	for _, d := range r.Dims {
		v, ok := scores[d.Key]
		if !ok {
			continue
		}
		max := d.Max
		if max <= 0 {
			max = 10
		}
		sum += float64(v) / float64(max) * d.Weight
		weight += d.Weight
	}
	if weight == 0 {
		return 0, ""
	}
	total := math.Round(sum/weight*1000) / 10 // one decimal
	return total, r.Grade(total)
}

// Grade returns the label of the highest band whose Min <= total.
func (r Rubric) Grade(total float64) string {
	label := ""
	best := math.Inf(-1)
	for _, b := range r.Bands {
		if total >= b.Min && b.Min > best {
			best = b.Min
			label = b.Label
		}
	}
	return label
}

// ValidateScores checks presence, range and integer-ness of every dimension.
func (r Rubric) ValidateScores(scores map[string]int) error {
	for _, d := range r.Dims {
		v, ok := scores[d.Key]
		if !ok {
			return fmt.Errorf("缺少维度「%s」的分数", d.Label)
		}
		max := d.Max
		if max <= 0 {
			max = 10
		}
		if v < 0 || v > max {
			return fmt.Errorf("「%s」分数需在 0~%d 之间", d.Label, max)
		}
		if d.Step > 1 && v%d.Step != 0 {
			return fmt.Errorf("「%s」只能按 %d 分档打分（0/%d/.../%d）", d.Label, d.Step, d.Step, max)
		}
	}
	for k := range scores {
		found := false
		for _, d := range r.Dims {
			if d.Key == k {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("未知维度 %q", k)
		}
	}
	return nil
}

// ValidateDimComments 校验「每个维度各自的点评」：五个维度都要写，且每条在
// [min,max] 字之间（按 Unicode 字符数，不按字节）。
func (r Rubric) ValidateDimComments(comments map[string]string) error {
	min, max := r.DimCommentMin, r.DimCommentMax
	if min <= 0 {
		min = 10
	}
	if max <= 0 {
		max = 50
	}
	for _, d := range r.Dims {
		text := strings.TrimSpace(comments[d.Key])
		if text == "" {
			return fmt.Errorf("缺少「%s」的点评（至少 %d 字）", d.Label, min)
		}
		n := utf8.RuneCountInString(text)
		if n < min {
			return fmt.Errorf("「%s」点评至少 %d 字（当前 %d 字，还差 %d 字）", d.Label, min, n, min-n)
		}
		if n > max {
			return fmt.Errorf("「%s」点评最多 %d 字（当前 %d 字）", d.Label, max, n)
		}
	}
	for k := range comments {
		found := false
		for _, d := range r.Dims {
			if d.Key == k {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("未知维度的点评 %q", k)
		}
	}
	return nil
}

// JoinDimComments 把五维点评合成一条文本（写入 annotations.comment，列表/导出/训练集沿用）。
func (r Rubric) JoinDimComments(comments map[string]string) string {
	parts := make([]string, 0, len(r.Dims))
	for _, d := range r.Dims {
		if text := strings.TrimSpace(comments[d.Key]); text != "" {
			parts = append(parts, d.Label+"："+text)
		}
	}
	return strings.Join(parts, "；")
}

// DimByKey finds a dimension by its key.
func (r Rubric) DimByKey(key string) (Dim, bool) {
	for _, d := range r.Dims {
		if d.Key == key {
			return d, true
		}
	}
	return Dim{}, false
}

// ValidatePatch 只校验被修改的维度：历史标注可能带着旧方案的取值（例如 18 分），
// 未改动的维度不该被当前步长规则卡住，否则老数据永远审不过。
func (r Rubric) ValidatePatch(patch map[string]int) error {
	for k, v := range patch {
		d, ok := r.DimByKey(k)
		if !ok {
			return fmt.Errorf("未知维度 %q", k)
		}
		max := d.Max
		if max <= 0 {
			max = 10
		}
		if v < 0 || v > max {
			return fmt.Errorf("「%s」分数需在 0~%d 之间", d.Label, max)
		}
		if d.Step > 1 && v%d.Step != 0 {
			return fmt.Errorf("「%s」只能按 %d 分档打分（0/%d/.../%d）", d.Label, d.Step, d.Step, max)
		}
	}
	return nil
}

// Snapshot is stored on every annotation so later rubric edits never rewrite history.
func (r Rubric) Snapshot() map[string]any {
	return map[string]any{
		"rubric_id":       r.ID,
		"name":            r.Name,
		"dims":            r.Dims,
		"grade_bands":     r.Bands,
		"comment_min":     r.CommentMin,
		"comment_max":     r.CommentMax,
		"dim_comment_min": r.DimCommentMin,
		"dim_comment_max": r.DimCommentMax,
		"total_override":  r.AllowTotalOver,
	}
}

// FromJSON builds a Rubric from the raw jsonb columns.
func FromJSON(id int64, name string, dimsRaw, bandsRaw []byte, commentMin, commentMax int,
	dimCommentMin, dimCommentMax int, allowOverride bool) (Rubric, error) {
	r := Rubric{
		ID: id, Name: name,
		CommentMin: commentMin, CommentMax: commentMax,
		DimCommentMin: dimCommentMin, DimCommentMax: dimCommentMax,
		AllowTotalOver: allowOverride,
	}
	if err := json.Unmarshal(dimsRaw, &r.Dims); err != nil {
		return r, fmt.Errorf("decode dims: %w", err)
	}
	if err := json.Unmarshal(bandsRaw, &r.Bands); err != nil {
		return r, fmt.Errorf("decode bands: %w", err)
	}
	if len(r.Dims) == 0 {
		return r, fmt.Errorf("评分方案没有维度")
	}
	return r, nil
}
