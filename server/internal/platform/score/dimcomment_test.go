package score

import (
	"strings"
	"testing"
)

func dimCommentRubric() Rubric {
	return Rubric{
		Dims: []Dim{
			{Key: "white", Label: "白平衡（匀）", Weight: 0.2, Max: 20},
			{Key: "black", Label: "黑平衡（稳）", Weight: 0.2, Max: 20},
			{Key: "variation", Label: "参差", Weight: 0.2, Max: 20},
			{Key: "structure", Label: "结构", Weight: 0.2, Max: 20},
			{Key: "control", Label: "控笔", Weight: 0.2, Max: 20},
		},
		DimCommentMin: 10,
		DimCommentMax: 50,
	}
}

func fullComments(n int) map[string]string {
	text := strings.Repeat("字", n)
	return map[string]string{
		"white": text, "black": text, "variation": text, "structure": text, "control": text,
	}
}

func TestValidateDimCommentsAcceptsTenCharsEach(t *testing.T) {
	r := dimCommentRubric()
	if err := r.ValidateDimComments(fullComments(10)); err != nil {
		t.Fatalf("每维正好 10 字应通过，实际: %v", err)
	}
}

func TestValidateDimCommentsRejectsShort(t *testing.T) {
	r := dimCommentRubric()
	c := fullComments(10)
	c["white"] = strings.Repeat("字", 9)
	err := r.ValidateDimComments(c)
	if err == nil {
		t.Fatal("白平衡只有 9 字应被拒绝")
	}
	if !strings.Contains(err.Error(), "白平衡") || !strings.Contains(err.Error(), "还差 1 字") {
		t.Fatalf("报错信息应指出维度与还差字数，实际: %v", err)
	}
}

func TestValidateDimCommentsRejectsMissing(t *testing.T) {
	r := dimCommentRubric()
	c := fullComments(10)
	delete(c, "black")
	err := r.ValidateDimComments(c)
	if err == nil || !strings.Contains(err.Error(), "黑平衡") {
		t.Fatalf("缺少黑平衡点评应被拒绝，实际: %v", err)
	}
}

func TestValidateDimCommentsRejectsTooLong(t *testing.T) {
	r := dimCommentRubric()
	c := fullComments(10)
	c["control"] = strings.Repeat("字", 51)
	err := r.ValidateDimComments(c)
	if err == nil || !strings.Contains(err.Error(), "最多 50 字") {
		t.Fatalf("超过 50 字应被拒绝，实际: %v", err)
	}
}

func TestValidateDimCommentsRejectsBlankOnly(t *testing.T) {
	r := dimCommentRubric()
	c := fullComments(10)
	c["structure"] = "   \n  "
	if err := r.ValidateDimComments(c); err == nil {
		t.Fatal("只有空白字符应视为未填写")
	}
}

func TestJoinDimCommentsKeepsDimensionLabels(t *testing.T) {
	r := dimCommentRubric()
	joined := r.JoinDimComments(fullComments(10))
	for _, d := range r.Dims {
		if !strings.Contains(joined, d.Label+"：") {
			t.Fatalf("合并文本应包含「%s：」，实际: %s", d.Label, joined)
		}
	}
}

// 老标注可能带着旧方案的非档位取值（18 分）；审核只改一维时，不能被未改维度的步长规则卡住。
func TestValidatePatchIgnoresUntouchedLegacyValues(t *testing.T) {
	r := fiveDimRubric()
	r.Dims[0].Step = 5
	// 只把 white 改成 5，其余维度不在 patch 里（历史值 18/13/16/16 都不是 5 的倍数）
	if err := r.ValidatePatch(map[string]int{"white": 5}); err != nil {
		t.Fatalf("只校验改动维度，不该因为历史值报错，实际: %v", err)
	}
}

func TestValidatePatchRejectsOffStepAndRange(t *testing.T) {
	r := fiveDimRubric()
	r.Dims[0].Step = 5
	if err := r.ValidatePatch(map[string]int{"white": 17}); err == nil {
		t.Fatal("17 不是 5 的档位，应被拒绝")
	}
	if err := r.ValidatePatch(map[string]int{"white": 25}); err == nil {
		t.Fatal("25 超出 0~20，应被拒绝")
	}
	if err := r.ValidatePatch(map[string]int{"nope": 5}); err == nil {
		t.Fatal("未知维度应被拒绝")
	}
}
