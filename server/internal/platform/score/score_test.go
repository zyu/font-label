package score

import "testing"

func TestGradeRanks(t *testing.T) {
	for i, grade := range []string{"S", "A", "B", "C", "D", "x"} {
		if got := GradeRank(grade); got != 5-i {
			t.Errorf("GradeRank(%q) = %d, want %d", grade, got, 5-i)
		}
	}
}

func TestValidateGrades(t *testing.T) {
	r := dimCommentRubric()
	for _, tc := range []struct {
		grades  map[string]string
		partial bool
		ok      bool
	}{
		{map[string]string{"white": "C"}, true, true},
		{map[string]string{"white": "C"}, false, false},
		{map[string]string{"white": "17"}, true, false},
		{map[string]string{"other": "B"}, true, false},
		{map[string]string{"white": "S", "black": "A", "variation": "B", "structure": "C", "control": "D"}, false, true},
	} {
		if err := r.ValidateGrades(tc.grades, tc.partial); (err == nil) != tc.ok {
			t.Errorf("ValidateGrades(%v, %v) = %v, want success %v", tc.grades, tc.partial, err, tc.ok)
		}
	}
}
