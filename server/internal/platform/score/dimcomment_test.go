package score

import (
	"strings"
	"testing"
)

func dimCommentRubric() Rubric {
	return Rubric{
		Dims: []Dim{
			{Key: "white", Label: "白平衡（匀）"},
			{Key: "black", Label: "黑平衡（稳）"},
			{Key: "variation", Label: "参差"},
			{Key: "structure", Label: "结构"},
			{Key: "control", Label: "控笔"},
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
