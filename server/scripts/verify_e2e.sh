#!/bin/bash
# End-to-end smoke test for font-label API.
# Repeatable: each run uses a fresh folder + fresh users, and every assertion is
# scoped to that folder, so earlier runs never pollute the numbers.
set -uo pipefail
API="${API:-http://127.0.0.1:18090/api/v1}"
API_ORIGIN="${API%/api/v1}"
SAMPLES="/Users/zyu/code/shangyue/font-label/标注看图示例"
S=$(date +%H%M%S)
ANNU="wang$S"; REVU="li$S"; FNAME="e2e_$S"
# 五维点评（每维 >=10 字）：变量里放的是真实引号，展开后直接进 JSON
DIMC='"dim_comments":{"white":"空白停匀,笔画间距一致","black":"两半重量相当,重心稳定","variation":"四周有长有短,参差自然","structure":"左右占位合理,结构清楚","control":"起收笔见轻重,顿挫分明"}'
DIMC_SHORT='"dim_comments":{"white":"太短","black":"两半重量相当,重心稳定","variation":"四周有长有短,参差自然","structure":"左右占位合理,结构清楚","control":"起收笔见轻重,顿挫分明"}'
pass=0; fail=0
ok()   { echo "  PASS  $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $1 -> $2"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want=$2 got=$3"; fi; }

echo "== 1. 登录 =="
AT=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@12345"}' | jq -r '.data.access_token // empty')
check "admin 登录拿到 token" "yes" "$([ -n "$AT" ] && echo yes || echo no)"

echo "== 2. 建账号（${ANNU} / ${REVU}）=="
curl -s -X POST "$API/users" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ANNU\",\"display_name\":\"小王$S\",\"role\":\"annotator\",\"password\":\"Passw0rd!\"}" >/dev/null
curl -s -X POST "$API/users" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$REVU\",\"display_name\":\"老师$S\",\"role\":\"reviewer\",\"password\":\"Passw0rd!\"}" >/dev/null
ANN=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ANNU\",\"password\":\"Passw0rd!\"}" | jq -r '.data.access_token // empty')
REV=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$REVU\",\"password\":\"Passw0rd!\"}" | jq -r '.data.access_token // empty')
check "标注员登录" "yes" "$([ -n "$ANN" ] && echo yes || echo no)"
check "审核员登录" "yes" "$([ -n "$REV" ] && echo yes || echo no)"

echo "== 3. 上传到文件夹 $FNAME =="
UP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/2421a35a-fce2-4a31-8377-e87d24d40f1c.png" \
  -F "files=@$SAMPLES/39785807-6cf1-475d-ab74-4d66795803cc.png" \
  -F "files=@$SAMPLES/21605656-4622-4b42-ba29-242dbf54e620.png" \
  -F "files=@$SAMPLES/image.png" \
  -F "folder_name=$FNAME")
FID=$(echo "$UP" | jq -r '.data.folder.id')
check "上传 4 张全部成功" "4" "$(echo "$UP" | jq -r '.data.summary.ok')"
check "文件夹名回显" "$FNAME" "$(echo "$UP" | jq -r '.data.folder.name')"
FIRST=$(echo "$UP" | jq -r '.data.items[0].stored_name' | sed 's/\..*//')
LAST=$(echo "$UP" | jq -r '.data.items[3].stored_name' | sed 's/\..*//')
check "首张=当天日期_NNN(3位)" "yes" "$(echo "$FIRST" | grep -qE "^$(date +%Y%m%d)_[0-9]{3}$" && echo yes || echo no)"
check "四张连续编号" "$(( $(echo "$FIRST" | sed 's/.*_//' | sed 's/^0*//') + 3 ))" "$(echo "$LAST" | sed 's/.*_//' | sed 's/^0*//')"
IMG1=$(echo "$UP" | jq -r '.data.items[0].image_id')
IMG2=$(echo "$UP" | jq -r '.data.items[1].image_id')
IMG3=$(echo "$UP" | jq -r '.data.items[2].image_id')
IMG4=$(echo "$UP" | jq -r '.data.items[3].image_id')

echo "== 4. 重复上传检测 =="
DUP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/2421a35a-fce2-4a31-8377-e87d24d40f1c.png" -F "folder_name=$FNAME")
check "重复图被拦截" "1" "$(echo "$DUP" | jq -r '.data.summary.duplicate')"

echo "== 5. 按文件夹领取 =="
CLAIM=$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"mode\":\"folder\",\"folder_id\":$FID}")
check "整包领取 4 张" "4" "$(echo "$CLAIM" | jq -r '.data.granted')"
check "池内剩余 0" "0" "$(echo "$CLAIM" | jq -r '.data.remaining_in_pool')"
check "持有 4/100" "4/100" "$(echo "$CLAIM" | jq -r '"\(.data.hold.current)/\(.data.hold.limit)"')"

echo "== 6. 领取后不可再领 =="
AGAIN=$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"mode\":\"folder\",\"folder_id\":$FID}")
check "文件夹已领完 -> granted=0" "0" "$(echo "$AGAIN" | jq -r '.data.granted')"
check "审核员不能领取(FORBIDDEN)" "FORBIDDEN" \
  "$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"mode":"count","count":1}' | jq -r '.error.code')"

echo "== 7. 提交标注 =="
SUB=$(curl -s -X POST "$API/images/$IMG1/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"11111111-1111-4111-8111-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":20,\"black\":20,\"variation\":15,\"structure\":15,\"control\":15},$DIMC,\"duration_ms\":42000}")
check "总分 85 良好" "85 良好" "$(echo "$SUB" | jq -r '"\(.data.total) \(.data.grade)"')"
check "状态 submitted" "submitted" "$(echo "$SUB" | jq -r '.data.status')"
DIM_BACK=$(curl -s "$API/images/$IMG1" -H "Authorization: Bearer $ANN" | jq -r '.data.image.dim_comments.white // empty')
check "五维点评已存库（白平衡）" "空白停匀,笔画间距一致" "$DIM_BACK"
MERGED=$(curl -s "$API/images/$IMG1" -H "Authorization: Bearer $ANN" | jq -r '.data.image.comment')
check "合并点评含维度名" "yes" "$(echo "$MERGED" | grep -q '白平衡（匀）：' && echo yes || echo no)"
IDEM=$(curl -s -X POST "$API/images/$IMG1/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"11111111-1111-4111-8111-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":20,\"black\":20,\"variation\":15,\"structure\":15,\"control\":15},$DIMC}")
check "重复提交幂等" "true" "$(echo "$IDEM" | jq -r '.data.duplicate')"

echo "== 8. 校验分支 =="
BAD=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"22222222-2222-4222-8222-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":20,\"black\":20,\"variation\":15,\"structure\":15,\"control\":15},$DIMC_SHORT}")
check "点评过短 422" "VALIDATION_FAILED" "$(echo "$BAD" | jq -r '.error.code')"
MISS=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"33333333-3333-4333-8333-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":5},$DIMC}")
check "缺维度 422" "VALIDATION_FAILED" "$(echo "$MISS" | jq -r '.error.code')"

echo "== 9. 异常上报 =="
EXC=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"44444444-4444-4444-8444-$(printf '%012d' "$((10#$S))")\",\"kind\":\"exception\",\"exception_reasons\":[\"blur\",\"privacy\"],\"exception_note\":\"整张模糊且右上角有姓名\"}")
check "异常提交 kind=exception" "exception" "$(echo "$EXC" | jq -r '.data.kind')"

echo "== 10. 第三张正常提交（用于打回重做）=="
S3=$(curl -s -X POST "$API/images/$IMG3/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"55555555-5555-4555-8555-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":10,\"black\":10,\"variation\":10,\"structure\":10,\"control\":10},$DIMC}")
check "第三张 50 分 较差" "50 较差" "$(echo "$S3" | jq -r '"\(.data.total) \(.data.grade)"')"

echo "== 11. 审核队列（限本文件夹）=="
Q=$(curl -s "$API/reviews/queue?folder_id=$FID" -H "Authorization: Bearer $REV")
check "待审核 3 条" "3" "$(echo "$Q" | jq -r '.data.total')"
check "标注员访问审核队列 403" "FORBIDDEN" "$(curl -s "$API/reviews/queue?folder_id=$FID" -H "Authorization: Bearer $ANN" | jq -r '.error.code')"

echo "== 12. 通过 / 打回 / 重复审核 =="
AP=$(curl -s -X POST "$API/images/$IMG1/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"approve"}')
check "通过 -> approved" "approved" "$(echo "$AP" | jq -r '.data.status')"
AGAINAP=$(curl -s -X POST "$API/images/$IMG1/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"action":"approve"}')
check "重复审核 409" "CONFLICT" "$(echo "$AGAINAP" | jq -r '.error.code')"
RJ=$(curl -s -X POST "$API/images/$IMG2/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"reject","comment":"异常判定不成立，请按四维正常打分","reason_tags":["异常判定不成立"]}')
check "异常图打回 -> rejected" "rejected" "$(echo "$RJ" | jq -r '.data.status')"
RJ3=$(curl -s -X POST "$API/images/$IMG3/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"reject","comment":"撇画太短没送到左下角，参差分应更低，请复核","reason_tags":["某维度分数明显偏高/偏低"]}')
check "普通图打回 -> rejected" "rejected" "$(echo "$RJ3" | jq -r '.data.status')"
SHORT=$(curl -s -X POST "$API/images/$IMG4/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"action":"reject","comment":"短"}')
check "批注过短 422" "VALIDATION_FAILED" "$(echo "$SHORT" | jq -r '.error.code')"

echo "== 13. 标注员看到驳回并重做 =="
MINE=$(curl -s "$API/claims/my?status=rejected" -H "Authorization: Bearer $ANN")
check "已驳回 2 条" "2" "$(echo "$MINE" | jq -r '.data.total')"
DET=$(curl -s "$API/images/$IMG2" -H "Authorization: Bearer $ANN")
check "批注可见" "异常判定不成立，请按四维正常打分" "$(echo "$DET" | jq -r '.data.reviews[0].comment')"
RE2=$(curl -s -X POST "$API/images/$IMG2/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"66666666-6666-4666-8666-$(printf '%012d' "$((10#$S))")\",\"kind\":\"score\",\"scores\":{\"white\":15,\"black\":20,\"variation\":15,\"structure\":15,\"control\":15},$DIMC}")
check "重做版本=2" "2" "$(echo "$RE2" | jq -r '.data.version')"
HIST=$(curl -s "$API/images/$IMG2/history" -H "Authorization: Bearer $ANN")
check "历史有 2 个版本" "2" "$(echo "$HIST" | jq -r '.data.versions | length')"

echo "== 14. 统计 =="
FS=$(curl -s "$API/stats/folders" -H "Authorization: Bearer $AT" | jq -c ".data.items[] | select(.id==$FID)")
check "文件夹总数 4" "4" "$(echo "$FS" | jq -r '.total')"
check "已通过 1" "1" "$(echo "$FS" | jq -r '.approved')"
check "待审核 1" "1" "$(echo "$FS" | jq -r '.submitted')"
check "已打回 1" "1" "$(echo "$FS" | jq -r '.rejected')"
AN=$(curl -s "$API/stats/annotators" -H "Authorization: Bearer $AT" | jq -c ".data.items[] | select(.name==\"小王$S\")")
check "标注员提交 3" "3" "$(echo "$AN" | jq -r '.submitted')"
check "驳回事件 2" "2" "$(echo "$AN" | jq -r '.reject_events')"
check "驳回原因有数据" "yes" "$([ "$(curl -s "$API/stats/rejections" -H "Authorization: Bearer $AT" | jq -r '.data.reasons | length')" -ge 1 ] && echo yes || echo no)"
check "趋势 7 个点" "7" "$(curl -s "$API/stats/timeseries?days=7" -H "Authorization: Bearer $AT" | jq -r '.data.points | length')"
check "我的统计 holding=1（第4张未提交）" "1" "$(curl -s "$API/stats/me" -H "Authorization: Bearer $ANN" | jq -r '.data.holding')"

echo "== 15. 导出 =="
curl -s -X POST "$API/exports" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d "{\"format\":\"xlsx\",\"filters\":{\"folders\":[$FID],\"status\":\"all\"}}" -o /tmp/fl_export.xlsx
check "xlsx 文件头 PK" "PK" "$(head -c 2 /tmp/fl_export.xlsx)"
ROWS=$(unzip -p /tmp/fl_export.xlsx "$(unzip -l /tmp/fl_export.xlsx | awk '/xl\/worksheets\/sheet/{print $4}' | head -1)" | grep -o '<row ' | wc -l | tr -d ' ')
check "xlsx 表头+4 行" "5" "$ROWS"
curl -s -X POST "$API/exports" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d "{\"format\":\"csv\",\"filters\":{\"folders\":[$FID],\"status\":\"all\"}}" -o /tmp/fl_export.csv
check "csv 带 BOM" "yes" "$(head -c 3 /tmp/fl_export.csv | grep -q $'\xef\xbb\xbf' && echo yes || echo no)"
check "csv 首列是文件夹名" "$FNAME" "$(sed -n '2p' /tmp/fl_export.csv | cut -d, -f1)"

echo "== 15b. 训练集 JSONL =="
curl -s -X POST "$API/exports" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
  -d "{\"format\":\"jsonl\",\"filters\":{\"folders\":[$FID],\"status\":\"all\"}}" -o /tmp/fl_train.jsonl
J_LINES=$(wc -l < /tmp/fl_train.jsonl | tr -d ' ')
check "jsonl 有样本" "yes" "$([ "${J_LINES:-0}" -ge 1 ] && echo yes || echo no)"
check "每行字段恰好 messages+images" "$J_LINES" \
  "$(jq -c 'select((keys|sort)==["images","messages"] and (.messages|length)==2)' /tmp/fl_train.jsonl | wc -l | tr -d ' ')"
check "assistant 是五维分数或无效结论" "$J_LINES" \
  "$(jq -r '.messages[1].content' /tmp/fl_train.jsonl | grep -cE '总分：|^无效：')"
check "user 带 <image> 占位" "yes" \
  "$(jq -r '.messages[0].content' /tmp/fl_train.jsonl | head -1 | grep -q '<image>' && echo yes || echo no)"
check "images 路径为 images/<folder>/<file>" "yes" \
  "$(jq -r '.images[0]' /tmp/fl_train.jsonl | head -1 | grep -q "^images/$FID/" && echo yes || echo no)"

echo "== 16. 权限 =="
LOCKED=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/image.png" -F "folder_name=locked_$S")
LOCKED_ID=$(echo "$LOCKED" | jq -r '.data.items[0].image_id')
check "标注员访问用户管理 403" "FORBIDDEN" "$(curl -s "$API/users" -H "Authorization: Bearer $ANN" | jq -r '.error.code')"
check "标注员访问未领取图片 403" "FORBIDDEN" "$(curl -s "$API/images/$LOCKED_ID" -H "Authorization: Bearer $ANN" | jq -r '.error.code')"
check "标注员访问统计表 403" "FORBIDDEN" "$(curl -s "$API/stats/annotators" -H "Authorization: Bearer $ANN" | jq -r '.error.code')"
check "未登录 401" "401" "$(curl -s -o /dev/null -w '%{http_code}' "$API/images")"
check "伪造签名 403" "403" "$(curl -s -o /dev/null -w '%{http_code}' "$API/media/$IMG1/preview?exp=1&sig=forged")"
PREVIEW=$(curl -s "$API/images/$IMG1" -H "Authorization: Bearer $ANN" | jq -r '.data.image.preview_url')
check "正常签名 200" "200" "$(curl -s -o /dev/null -w '%{http_code}' "$API_ORIGIN$PREVIEW")"

echo "== 16b. 本轮修复回归 =="
# 1) GIF：此前 allowedExt 声明支持，但解码器未注册，上传必失败
printf 'R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7' | base64 -d > /tmp/fl_probe.gif
GIFUP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" -F "files=@/tmp/fl_probe.gif" -F "folder_name=gif_$S")
GIF_FID=$(echo "$GIFUP" | jq -r '.data.folder.id')
check "gif 上传成功" "1" "$(echo "$GIFUP" | jq -r '.data.summary.ok')"
check "gif 转存为 jpg" "jpg" "$(echo "$GIFUP" | jq -r '.data.items[0].stored_name' | sed 's/.*\.//')"

# 2) 非 UUID 的 client_submit_id：此前落库报 22P02 变 500，现在应 422
BADUUID=$(curl -s -X POST "$API/images/$IMG4/submit" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"client_submit_id\":\"not-a-uuid\",\"kind\":\"score\",\"scores\":{\"white\":10,\"black\":10,\"variation\":10,\"structure\":10,\"control\":10},$DIMC}")
check "非 UUID 提交 422" "VALIDATION_FAILED" "$(echo "$BADUUID" | jq -r '.error.code')"

# 3) 审核改分：总分重算 + 五维同步写回 + 审核留痕
PATCHED=$(curl -s -X POST "$API/images/$IMG2/review" -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' \
  -d '{"action":"approve","comment":"白平衡一维偏高，按 5 分复核","score_patch":{"white":5}}')
check "改分后总分 70" "70" "$(echo "$PATCHED" | jq -r '.data.total')"
AFTER=$(curl -s "$API/images/$IMG2" -H "Authorization: Bearer $AT")
check "改分已写回五维" "5" "$(echo "$AFTER" | jq -r '.data.image.scores.white')"
check "改分在审核记录里留痕" "5" "$(echo "$AFTER" | jq -r '.data.reviews[0].score_patch.white')"

# 4) 整包领取也要受持有上限约束（已持 1 张、上限 3 张、文件夹 4 张 → 只能再领 2 张）
curl -s -X PUT "$API/settings/max_hold_per_user" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' -d '3' >/dev/null
HOLDUP=$(curl -s -X POST "$API/upload" -H "Authorization: Bearer $AT" \
  -F "files=@$SAMPLES/2421a35a-fce2-4a31-8377-e87d24d40f1c.png" \
  -F "files=@$SAMPLES/39785807-6cf1-475d-ab74-4d66795803cc.png" \
  -F "files=@$SAMPLES/21605656-4622-4b42-ba29-242dbf54e620.png" \
  -F "files=@$SAMPLES/image.png" -F "folder_name=hold_$S")
HOLD_FID=$(echo "$HOLDUP" | jq -r '.data.folder.id')
HOLDCLAIM=$(curl -s -X POST "$API/claims" -H "Authorization: Bearer $ANN" -H 'Content-Type: application/json' \
  -d "{\"mode\":\"folder\",\"folder_id\":$HOLD_FID}")
check "整包领取被上限截断（已持1,上限3 → 领2）" "2" "$(echo "$HOLDCLAIM" | jq -r '.data.granted')"
check "文件夹里还剩 2 张" "2" "$(echo "$HOLDCLAIM" | jq -r '.data.remaining_in_pool')"
curl -s -X PUT "$API/settings/max_hold_per_user" -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' -d '100' >/dev/null
check "持有上限已恢复 100" "100" \
  "$(curl -s "$API/settings" -H "Authorization: Bearer $AT" | jq -r '.data.settings.max_hold_per_user')"

echo
echo "== 17. 清理（归档本次测试用到的文件夹，避免污染领取池）=="
LOCKED_FID=$(echo "$LOCKED" | jq -r '.data.folder.id')
ADMIN_ID=$(curl -s "$API/auth/me" -H "Authorization: Bearer $AT" | jq -r '.data.user.id')
for f in "$FID" "$LOCKED_FID" "$GIF_FID" "$HOLD_FID"; do
  [ -n "$f" ] && [ "$f" != "null" ] || continue
  check "未完成文件夹 $f 不允许归档" "CONFLICT" \
    "$(curl -s -X PATCH "$API/folders/$f" -H "Authorization: Bearer $AT" \
      -H 'Content-Type: application/json' -d '{"archived":true}' | jq -r '.error.code')"
  # Only this run's fixtures are touched: remove unclaimed images and finish
  # claimed/rejected test images through the real submission/review endpoints.
  FIXTURES=$(curl -s "$API/images?folder_id=$f&page_size=100" -H "Authorization: Bearer $AT")
  while read -r image_id status; do
    case "$status" in
      unassigned)
        check "删除未领取测试图 $image_id" "200" \
          "$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/images/$image_id" -H "Authorization: Bearer $AT")"
        continue ;;
      assigned|rejected)
        curl -s -X POST "$API/images/$image_id/reassign" -H "Authorization: Bearer $AT" \
          -H 'Content-Type: application/json' -d "{\"annotator_id\":$ADMIN_ID}" >/dev/null
        check "完成测试图 $image_id" "200" \
          "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/images/$image_id/submit" \
            -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' \
            -d '{"kind":"exception","exception_reasons":["empty"],"exception_note":"自动化测试样本清理"}')" ;;
      submitted) ;;
      *) continue ;;
    esac
    check "审核测试图 $image_id" "200" \
      "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/images/$image_id/review" \
        -H "Authorization: Bearer $REV" -H 'Content-Type: application/json' -d '{"action":"approve"}')"
  done < <(echo "$FIXTURES" | jq -r '.data.items[] | "\(.id) \(.status)"')
  check "处理完成文件夹 $f 可归档" "200" \
    "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH "$API/folders/$f" \
      -H "Authorization: Bearer $AT" -H 'Content-Type: application/json' -d '{"archived":true}')"
done
check "测试文件夹已归档" "true" \
  "$(curl -s "$API/folders?include_archived=1" -H "Authorization: Bearer $AT" | jq -r ".data.items[] | select(.id==$FID) | .archived")"

echo
echo "==== 结果: pass=$pass fail=$fail (folder=$FNAME) ===="
test "$fail" -eq 0
