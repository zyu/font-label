package score

import (
	"encoding/json"
	"fmt"
	"math"
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
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Dims             []Dim  `json:"dims"`
	Bands            []Band `json:"grade_bands"`
	CommentMin       int    `json:"comment_min"`
	CommentMax       int    `json:"comment_max"`
	AllowTotalOver   bool   `json:"allow_total_override"`
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

// Snapshot is stored on every annotation so later rubric edits never rewrite history.
func (r Rubric) Snapshot() map[string]any {
	return map[string]any{
		"rubric_id":      r.ID,
		"name":           r.Name,
		"dims":           r.Dims,
		"grade_bands":    r.Bands,
		"comment_min":    r.CommentMin,
		"comment_max":    r.CommentMax,
		"total_override": r.AllowTotalOver,
	}
}

// FromJSON builds a Rubric from the raw jsonb columns.
func FromJSON(id int64, name string, dimsRaw, bandsRaw []byte, commentMin, commentMax int, allowOverride bool) (Rubric, error) {
	r := Rubric{ID: id, Name: name, CommentMin: commentMin, CommentMax: commentMax, AllowTotalOver: allowOverride}
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
