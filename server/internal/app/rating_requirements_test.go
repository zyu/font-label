package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRatingsAPIUsesLetters(t *testing.T) {
	s, router, admin := regressionServer(t)
	_, id := regressionImage(t, s, "assigned")
	path := fmt.Sprintf("/images/%d/submit", id)
	grades := map[string]string{"white": "S", "black": "A", "variation": "B", "structure": "C"}
	comments := map[string]string{}
	for _, key := range []string{"white", "black", "variation", "structure", "control"} {
		comments[key] = "这里是一条足够长的维度评语"
	}
	body := map[string]any{"kind": "score", "grades": grades, "dim_comments": comments}
	for _, tc := range []struct{ grade, message string }{{"", "的等级"}, {"X", "S/A/B/C/D"}} {
		if tc.grade != "" {
			grades["control"] = tc.grade
		}
		res := regressionRequest(t, router, admin, "POST", path, body)
		if res.Code != 422 || !strings.Contains(res.Body.String(), tc.message) {
			t.Fatalf("invalid grade: %d %s", res.Code, res.Body)
		}
	}
	grades["control"] = "D"
	if res := regressionRequest(t, router, admin, "POST", path, body); res.Code != 200 {
		t.Fatalf("valid grades: %d %s", res.Code, res.Body)
	}
	res := regressionRequest(t, router, admin, "GET", fmt.Sprintf("/images/%d", id), nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"white":"S"`) ||
		strings.Contains(res.Body.String(), `"scores"`) || strings.Contains(res.Body.String(), `"total"`) {
		t.Fatalf("grade detail: %d %s", res.Code, res.Body)
	}
}

func TestRequiredExceptionTypes(t *testing.T) {
	s, router, admin := regressionServer(t)
	_, id := regressionImage(t, s, "assigned")
	submit := func(reason string) int {
		return regressionRequest(t, router, admin, "POST", fmt.Sprintf("/images/%d/submit", id),
			map[string]any{"kind": "exception", "exception_reasons": []string{reason}}).Code
	}
	for _, obsolete := range []string{"tilt", "privacy", "empty", "occluded", "exposure"} {
		if got := submit(obsolete); got != 422 {
			t.Fatalf("obsolete reason %s produced HTTP %d", obsolete, got)
		}
	}
	if got := submit("wrong_char"); got != 200 {
		t.Fatalf("wrong_char produced HTTP %d", got)
	}
}

func TestFolderListsAreServerPaginated(t *testing.T) {
	s, router, admin := regressionServer(t)
	if _, err := s.pool().Exec(context.Background(), `INSERT INTO folders (name)
		SELECT '分页文件夹 '||n FROM generate_series(1,22) n`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/folders", "/stats/folders"} {
		for page, want := range map[int]int{1: 20, 2: 2} {
			res := regressionRequest(t, router, admin, "GET", fmt.Sprintf("%s?page=%d&page_size=100", path, page), nil)
			var result struct {
				Data struct {
					Items []FolderDTO
					Total int
				}
			}
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != 200 ||
				result.Data.Total != 22 || len(result.Data.Items) != want {
				t.Fatalf("page %d of %s: %d %s (%v)", page, path, res.Code, res.Body, err)
			}
		}
	}
}
