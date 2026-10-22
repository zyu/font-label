package score

import "testing"

// 现行口径：五个维度，每维 20 分，总分直接相加（100 分制）。
func fiveDimRubric() Rubric {
	return Rubric{
		ID:   3,
		Name: "五维百分制（每维 20 分）",
		Dims: []Dim{
			{Key: "white", Label: "白平衡（匀）", Weight: 0.2, Max: 20, Step: 5},
			{Key: "black", Label: "黑平衡（稳）", Weight: 0.2, Max: 20, Step: 5},
			{Key: "variation", Label: "参差", Weight: 0.2, Max: 20, Step: 5},
			{Key: "structure", Label: "结构", Weight: 0.2, Max: 20, Step: 5},
			{Key: "control", Label: "控笔", Weight: 0.2, Max: 20, Step: 5},
		},
		Bands: []Band{
			{Label: "优秀", Min: 90},
			{Label: "良好", Min: 80},
			{Label: "中等", Min: 60},
			{Label: "较差", Min: 0},
		},
		CommentMin: 20,
		CommentMax: 50,
	}
}

func TestFiveDimTotalIsPlainSum(t *testing.T) {
	r := fiveDimRubric()
	cases := []struct {
		name   string
		scores map[string]int
		total  float64
		grade  string
	}{
		{
			"满分 100",
			map[string]int{"white": 20, "black": 20, "variation": 20, "structure": 20, "control": 20},
			100, "优秀",
		},
		{
			"20/20/15/15/15",
			map[string]int{"white": 20, "black": 20, "variation": 15, "structure": 15, "control": 15},
			85, "良好",
		},
		{
			"全 15 = 75 中等",
			map[string]int{"white": 15, "black": 15, "variation": 15, "structure": 15, "control": 15},
			75, "中等",
		},
		{
			"全 10 = 50 较差",
			map[string]int{"white": 10, "black": 10, "variation": 10, "structure": 10, "control": 10},
			50, "较差",
		},
		{
			"零分",
			map[string]int{"white": 0, "black": 0, "variation": 0, "structure": 0, "control": 0},
			0, "较差",
		},
	}
	for _, tc := range cases {
		got, grade := r.Total(tc.scores)
		if got != tc.total {
			t.Errorf("%s: total = %v, want %v", tc.name, got, tc.total)
		}
		if grade != tc.grade {
			t.Errorf("%s: grade = %v, want %v", tc.name, grade, tc.grade)
		}
	}
}

// 单维缺分不能算总分（避免"少打一维也显示满分"）。
func TestFiveDimMissingOneIsInvalid(t *testing.T) {
	r := fiveDimRubric()
	if err := r.ValidateScores(map[string]int{
		"white": 20, "black": 20, "variation": 20, "structure": 20,
	}); err == nil {
		t.Fatal("缺少「控笔」应被拒绝")
	}
}

func TestFiveDimRangeIsTwenty(t *testing.T) {
	r := fiveDimRubric()
	full := map[string]int{"white": 20, "black": 20, "variation": 20, "structure": 20, "control": 20}
	if err := r.ValidateScores(full); err != nil {
		t.Fatalf("满分行应通过：%v", err)
	}
	over := map[string]int{"white": 21, "black": 20, "variation": 20, "structure": 20, "control": 20}
	if err := r.ValidateScores(over); err == nil {
		t.Fatal("21 分应被拒绝（每维上限 20）")
	}
}

// 后台改权重后仍按权重归一化，不会悄悄变回简单相加。
func TestWeightedStillWorks(t *testing.T) {
	r := fiveDimRubric()
	r.Dims = []Dim{
		{Key: "white", Label: "白平衡", Weight: 0.4, Max: 20},
		{Key: "black", Label: "黑平衡", Weight: 0.3, Max: 20},
		{Key: "variation", Label: "参差", Weight: 0.2, Max: 20},
		{Key: "structure", Label: "结构", Weight: 0.1, Max: 20},
	}
	total, _ := r.Total(map[string]int{"white": 20, "black": 20, "variation": 20, "structure": 0})
	if total != 90 {
		t.Fatalf("weighted total = %v, want 90", total)
	}
}

// 打分只能落在 0/5/10/15/20 这些档位上。
func TestFiveDimStepEnforced(t *testing.T) {
	r := fiveDimRubric()
	ok := map[string]int{"white": 15, "black": 20, "variation": 5, "structure": 10, "control": 0}
	if err := r.ValidateScores(ok); err != nil {
		t.Fatalf("档位上的分数应通过：%v", err)
	}
	for _, bad := range []int{1, 7, 12, 17, 19} {
		s := map[string]int{"white": bad, "black": 20, "variation": 15, "structure": 15, "control": 15}
		if err := r.ValidateScores(s); err == nil {
			t.Errorf("white=%d 不是 5 的倍数，应被拒绝", bad)
		}
	}
}

func TestGradeBands(t *testing.T) {
	r := fiveDimRubric()
	for _, tc := range []struct {
		total float64
		want  string
	}{{100, "优秀"}, {90, "优秀"}, {89.5, "良好"}, {80, "良好"}, {79.5, "中等"}, {60, "中等"}, {59.5, "较差"}} {
		if got := r.Grade(tc.total); got != tc.want {
			t.Errorf("Grade(%v) = %v, want %v", tc.total, got, tc.want)
		}
	}
}
