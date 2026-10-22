#!/bin/bash
# End-to-end smoke test for font-label API.
set -uo pipefail
API="http://127.0.0.1:18090/api/v1"
SAMPLES="/Users/zyu/code/shangyue/font-label/标注看图示例"
pass=0; fail=0
ok()   { echo "  PASS  $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $1 -> $2"; fail=$((fail+1)); }
check(){ # desc expected actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want=$2 got=$3"; fi
}

echo "== 1. 登录 =="
ADMIN=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@12345"}')
AT=$(echo "$ADMIN" | jq -r '.data.access_token // empty')
check "admin 登录拿到 token" "yes" "$([ -n "$AT" ] && echo yes || echo no)"

echo "== 2. 建账号 =="
for u in xiaowang:annotator laoshi:reviewer; do
  name=${u%%:*}; role=${u##*:}
  curl -s -X POST "$API/users" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$name\",\"display_name\":\"$name\",\"role\":\"$role\",\"password\":\"Passw0rd!\"}" > /dev/null
done
ANN=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"xiaowang","password":"Passw0rd!"}' | jq -r '.data.access_token // empty')
REV=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"laoshi","password":"Passw0rd!"}' | jq -r '.data.access_token // empty')
check "标注员登录" "yes" "$([ -n "$ANN" ] && echo yes || echo no)"
check "审核员登录" "yes" "$([ -n "$REV" ] && echo yes || echo no)"

echo "== 3. 上传（文件夹名留空 -> 当天日期）=="
UP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/2421a35a-fce2-4a31-8377-e87d24d40f1c.png" \
  -F "files=@$SAMPLES/39785807-6cf1-475d-ab74-4d66795803cc.png" \
  -F "files=@$SAMPLES/21605656-4622-4b42-ba29-242dbf54e620.png" \
  -F "files=@$SAMPLES/image.png")
echo "$UP" | jq -c '.data.summary'
FOLDER=$(echo "$UP" | jq -r '.data.folder.name')
check "上传 4 张全部成功" "4" "$(echo "$UP" | jq -r '.data.summary.ok')"
check "文件夹名=当天日期" "$(date +%Y%m%d)" "$FOLDER"
check "首张文件名 日期_001" "$(date +%Y%m%d)_001.jpg" "$(echo "$UP" | jq -r '.data.items[0].stored_name')"
check "最后一张序号 004" "4" "$(echo "$UP" | jq -r '.data.items[3].stored_name' | sed 's/.*_0*//;s/\..*//')"

echo "== 4. 重复上传检测 =="
DUP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/2421a35a-fce2-4a31-8377-e87d24d40f1c.png")
check "重复图被拦截" "1" "$(echo "$DUP" | jq -r '.data.summary.duplicate')"

echo "== 5. 按张领取 =="
CLAIM=$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"mode":"count","count":2}')
check "领取 2 张" "2" "$(echo "$CLAIM" | jq -r '.data.granted')"
IMG1=$(echo "$CLAIM" | jq -r '.data.images[0].id')
IMG2=$(echo "$CLAIM" | jq -r '.data.images[1].id')

echo "== 6. 别人领不到已领的图 =="
POOL=$(curl -s "$API/claims/my?status=active" -H "Authorization: Bearer $ANN" | jq -r '.data.total')
check "我的待完成=2" "2" "$POOL"
CLAIM2=$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"mode":"count","count":5}')
check "审核员不能领取(403)" "403" "$(echo "$CLAIM2" | jq -r '.error.code' )"

echo "== 7. 提交标注 =="
SUB=$(curl -s -X POST "$API/images/$IMG1/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"client_submit_id":"11111111-1111-4111-8111-111111111111","kind":"score","scores":{"fill":9,"space":9,"balance":10,"variation":9},"comment":"字撑满梯形格，四周留白均匀；三笔间距相等，重心稳。","duration_ms":42000}')
check "提交成功 92.5 优秀" "92.5 优秀" "$(echo "$SUB" | jq -r '"\(.data.total) \(.data.grade)"')"
check "状态 submitted" "submitted" "$(echo "$SUB" | jq -r '.data.status')"
IDEM=$(curl -s -X POST "$API/images/$IMG1/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"client_submit_id":"11111111-1111-4111-8111-111111111111","kind":"score","scores":{"fill":9,"space":9,"balance":10,"variation":9},"comment":"字撑满梯形格，四周留白均匀；三笔间距相等，重心稳。"}')
check "重复提交幂等" "true" "$(echo "$IDEM" | jq -r '.data.duplicate')"

echo "== 8. 点评字数校验 =="
BAD=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"client_submit_id":"22222222-2222-4222-8222-222222222222","kind":"score","scores":{"fill":8,"space":7,"balance":8,"variation":6},"comment":"太短了"}')
check "点评过短被拒(422)" "VALIDATION_FAILED" "$(echo "$BAD" | jq -r '.error.code')"

echo "== 9. 异常上报 =="
EXC=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"client_submit_id":"33333333-3333-4333-8333-333333333333","kind":"exception","exception_reasons":["blur","privacy"],"exception_note":"整张模糊且右上角有姓名"}')
check "异常提交成功" "exception" "$(echo "$EXC" | jq -r '.data.kind')"

echo "== 10. 审核队列 =="
Q=$(curl -s "$API/reviews/queue" -H "Authorization: Bearer $REV")
check "队列有 2 条" "2" "$(echo "$Q" | jq -r '.data.total')"

echo "== 11. 审核：通过 + 打回 =="
AP=$(curl -s -X POST "$API/images/$IMG1/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"approve"}')
check "通过后 approved" "approved" "$(echo "$AP" | jq -r '.data.status')"
RJ=$(curl -s -X POST "$API/images/$IMG2/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"reject","comment":"这张是异常图但原因选错，请重新判定","reason_tags":["异常判定不成立"]}')
check "异常被打回 -> rejected" "rejected" "$(echo "$RJ" | jq -r '.data.status')"
NOC=$(curl -s -X POST "$API/images/$IMG1/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"action":"reject","comment":"短"}')
check "打回批注过短被拒(422)" "VALIDATION_FAILED" "$(echo "$NOC" | jq -r '.error.code')"

echo "== 12. 标注员看到驳回 =="
MINE=$(curl -s "$API/claims/my?status=rejected" -H "Authorization: Bearer $ANN")
check "已驳回 1 条" "1" "$(echo "$MINE" | jq -r '.data.total')"
DET=$(curl -s "$API/images/$IMG2" -H "Authorization: Bearer $ANN")
check "驳回批注可见" "这张是异常图但原因选错，请重新判定" "$(echo "$DET" | jq -r '.data.reviews[0].comment')"

echo "== 13. 驳回后重做重提 =="
RE2=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d '{"client_submit_id":"44444444-4444-4444-8444-444444444444","kind":"score","scores":{"fill":4,"space":5,"balance":5,"variation":6},"comment":"字缩在格子左上角只占三分之一，三竖间距不均，左竖不到底，纸面有涂改。"}')
check "版本 2 提交 50 分" "2 50" "$(echo "$RE2" | jq -r '"\(.data.version) \(.data.total)"')"

echo "== 14. 统计 =="
OV=$(curl -s "$API/stats/overview" -H "Authorization: Bearer $AT")
check "已通过=1" "1" "$(echo "$OV" | jq -r '.data.approved')"
check "待审核=1" "1" "$(echo "$OV" | jq -r '.data.submitted')"
AN=$(curl -s "$API/stats/annotators" -H "Authorization: Bearer $AT")
check "标注员表含 xiaowang" "xiaowang" "$(echo "$AN" | jq -r '.data.items[0].name')"
check "驳回事件=1" "1" "$(echo "$AN" | jq -r '.data.items[0].reject_events')"

echo "== 15. 导出 =="
curl -s -X POST "$API/exports" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d '{"format":"xlsx","filters":{"status":"all"}}' -o /tmp/fl_export.xlsx
check "xlsx 导出文件头 PK" "PK" "$(head -c 2 /tmp/fl_export.xlsx)"
curl -s -X POST "$API/exports" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d '{"format":"csv","filters":{"status":"all"}}' -o /tmp/fl_export.csv
check "csv 有 BOM" "yes" "$(head -c 3 /tmp/fl_export.csv | grep -q $'\xef\xbb\xbf' && echo yes || echo no)"

echo "== 16. 权限 =="
P1=$(curl -s "$API/users" -H "Authorization: Bearer $ANN" | jq -r '.error.code')
check "标注员访问用户管理 403" "FORBIDDEN" "$P1"
P2=$(curl -s "$API/images/1" -H "Authorization: Bearer $ANN" | jq -r '.error.code')
check "标注员访问他人图片 403" "FORBIDDEN" "$P2"
P3=$(curl -s -o /dev/null -w '%{http_code}' "$API/images")
check "未登录 401" "401" "$P3"

echo
echo "==== 结果: pass=$pass fail=$fail ===="
