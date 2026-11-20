package score

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Dim struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type Rubric struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Dims          []Dim    `json:"dims"`
	Levels        []string `json:"levels"`
	DimCommentMin int      `json:"dim_comment_min"`
	DimCommentMax int      `json:"dim_comment_max"`
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

// DimByKey finds a dimension by its key.
func (r Rubric) DimByKey(key string) (Dim, bool) {
	for _, d := range r.Dims {
		if d.Key == key {
			return d, true
		}
	}
	return Dim{}, false
}

var Levels = []string{"S", "A", "B", "C", "D"}

func GradeRank(level string) int {
	for i, allowed := range Levels {
		if level == allowed {
			return len(Levels) - i
		}
	}
	return 0
}

func (r Rubric) ValidateGrades(grades map[string]string, partial bool) error {
	if !partial {
		for _, dim := range r.Dims {
			if _, ok := grades[dim.Key]; !ok {
				return fmt.Errorf("缺少维度「%s」的等级", dim.Label)
			}
		}
	}
	for key, level := range grades {
		dim, ok := r.DimByKey(key)
		if !ok {
			return fmt.Errorf("未知维度 %q", key)
		}
		if GradeRank(level) == 0 {
			return fmt.Errorf("「%s」等级只能是 S/A/B/C/D", dim.Label)
		}
	}
	return nil
}
