package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

type settingDefinition struct {
	kind string
	min  int
	max  int
}

// Bounds keep task counts, upload batches and lock durations operationally bounded.
var settingDefinitions = map[string]settingDefinition{
	"lock_ttl_hours":         {kind: "integer", min: 1, max: 8760},
	"max_hold_per_user":      {kind: "integer", min: 1, max: 10000},
	"claim_default_count":    {kind: "integer", min: 1, max: 10000},
	"max_claim_count":        {kind: "integer", min: 1, max: 10000},
	"max_upload_files":       {kind: "integer", min: 1, max: 1000},
	"max_file_mb":            {kind: "integer", min: 0, max: 1024},
	"auto_next_after_submit": {kind: "boolean"},
	"exception_reasons":      {kind: "reasons"},
	"reject_reason_tags":     {kind: "strings"},
	"comment_templates":      {kind: "strings"},
}

func validateSetting(key string, body []byte) error {
	def, ok := settingDefinitions[key]
	fail := func(message string) error {
		return httpx.Validation(map[string]string{key: message}, "配置值校验失败："+message)
	}
	if !ok {
		return httpx.Validation(map[string]string{"key": "不可修改的配置项"}, "配置项不可写")
	}
	switch def.kind {
	case "integer":
		var value *int
		if json.Unmarshal(body, &value) != nil || value == nil || *value < def.min || *value > def.max {
			return fail(fmt.Sprintf("请输入 %d~%d 的整数", def.min, def.max))
		}
	case "boolean":
		var value *bool
		if json.Unmarshal(body, &value) != nil || value == nil {
			return fail("必须为布尔值 true 或 false")
		}
	case "strings", "reasons":
		var items []json.RawMessage
		if json.Unmarshal(body, &items) != nil || items == nil {
			return fail("必须为 JSON 数组")
		}
		seen := map[string]bool{}
		for _, item := range items {
			if def.kind == "strings" {
				var value *string
				if json.Unmarshal(item, &value) != nil || value == nil || strings.TrimSpace(*value) == "" {
					return fail("数组中的每项必须为非空字符串")
				}
				continue
			}
			var reason map[string]json.RawMessage
			if json.Unmarshal(item, &reason) != nil || len(reason) != 2 {
				return fail("异常原因每项必须仅包含非空字符串 key 和 label")
			}
			var key, label string
			if json.Unmarshal(reason["key"], &key) != nil || json.Unmarshal(reason["label"], &label) != nil || strings.TrimSpace(key) == "" || strings.TrimSpace(label) == "" {
				return fail("异常原因每项必须仅包含非空字符串 key 和 label")
			}
			if strings.TrimSpace(key) != key || seen[key] {
				return fail("异常原因 key 不能重复或含首尾空格")
			}
			seen[key] = true
		}
	}
	return nil
}
