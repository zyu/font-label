#!/usr/bin/env bash
# 硬笔书法作业评分标注平台 · 部署脚本
#
# 用法：
#   ./deploy/deploy.sh                 # 首次部署（缺 .env 会自动生成随机密钥）
#   ./deploy/deploy.sh deploy          # 同上，可重复执行（= 升级）
#   ./deploy/deploy.sh upgrade         # 重建镜像并滚动重启（不重新生成密钥）
#   ./deploy/deploy.sh status          # 容器状态 + 健康检查 + 数据卷占用
#   ./deploy/deploy.sh logs [服务]      # 跟踪日志（默认全部）
#   ./deploy/deploy.sh backup          # 备份数据库 + 图片到 backups/
#   ./deploy/deploy.sh restore <文件>   # 从备份恢复（会二次确认并停 API）
#   ./deploy/deploy.sh down            # 停止容器（保留数据卷）
#   ./deploy/deploy.sh purge           # 停止并删除数据卷（危险，需输入 yes 两次）
#
# 环境变量（可选）：
#   WEB_PORT=8088  HTTP_PORT  BACKUP_DIR=./backups  BACKUP_KEEP=0(不清理)
#   GOPROXY=https://goproxy.cn,https://goproxy.io,direct
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="$ROOT/deploy"
COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
ENV_FILE="$DEPLOY_DIR/.env"
PROJECT="font-label"
BACKUP_DIR="${BACKUP_DIR:-$ROOT/backups}"
BACKUP_KEEP="${BACKUP_KEEP:-0}"

if [[ -t 1 ]]; then
  C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'; C_DIM=$'\033[2m'; C_END=$'\033[0m'
else
  C_OK=""; C_WARN=""; C_ERR=""; C_DIM=""; C_END=""
fi

log()  { printf '%s[deploy]%s %s\n' "$C_OK" "$C_END" "$*"; }
warn() { printf '%s[warn]%s %s\n' "$C_WARN" "$C_END" "$*" >&2; }
die()  { printf '%s[error]%s %s\n' "$C_ERR" "$C_END" "$*" >&2; exit 1; }
dim()  { printf '%s%s%s\n' "$C_DIM" "$*" "$C_END"; }

on_error() {
  local code=$?
  printf '\n%s部署中断（退出码 %s，第 %s 行）%s\n' "$C_ERR" "$code" "${BASH_LINENO[0]:-?}" "$C_END" >&2
  printf '排查：%s logs --tail=80\n' "$0" >&2
  exit "$code"
}
trap on_error ERR

# ---------- 前置检查 ----------
need() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }

check_prereq() {
  need docker
  docker info >/dev/null 2>&1 || die "Docker 未运行或当前用户无权限（试试 sudo 或把用户加入 docker 组）"
  docker compose version >/dev/null 2>&1 || die "需要 Docker Compose v2（docker compose，而不是 docker-compose）"
  [[ -f "$COMPOSE_FILE" ]] || die "找不到 $COMPOSE_FILE"
}

dc() { docker compose --project-name "$PROJECT" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"; }

# ---------- 镜像准备：能复用本地镜像就不拉，拉不动要给能照做的指引 ----------
image_exists() { docker image inspect "$1" >/dev/null 2>&1; }

pull_with_retry() {
  local img="$1" i
  for i in 1 2 3; do
    docker pull "$img" >/dev/null 2>&1 && return 0
    sleep $((i * 3))
  done
  return 1
}

detect_pg_image() {
  if [[ -n "${POSTGRES_IMAGE:-}" ]]; then
    echo "$POSTGRES_IMAGE"
    return
  fi
  local cand
  for cand in postgres:16-alpine postgres:16 postgres:17-alpine postgres:15-alpine; do
    if image_exists "$cand"; then
      echo "$cand"
      return
    fi
  done
  echo "postgres:16-alpine"
}

mirror_help() {
  local img="$1"
  cat >&2 <<EOF

${C_ERR}拉取镜像失败：${img}${C_END}
这通常是 Docker Hub 在当前网络不可达。三种办法，任选其一后重跑本脚本：

  1) 配镜像加速（推荐）
     sudo tee /etc/docker/daemon.json >/dev/null <<'JSON'
     { "registry-mirrors": ["https://<你的加速地址>"] }
     JSON
     sudo systemctl restart docker       # macOS Docker Desktop 在设置里改 Docker Engine

  2) 离线导入（内网服务器常用）
     docker load -i base-images.tar      # 事先在能联网的机器上 save 好

  3) 复用本地已有镜像
     docker images | grep -E 'golang|node|postgres|alpine'
     然后按需改 server/Dockerfile、web/Dockerfile 的 FROM，或
     export POSTGRES_IMAGE=postgres:16

EOF
}

# 构建阶段需要的基础镜像；缺了先试着拉，拉不动就停止并给指引（不要等到 compose 半路失败）
preflight_images() {
  local pg_img
  pg_img="$(detect_pg_image)"
  if ! image_exists "$pg_img"; then
    log "拉取数据库镜像 $pg_img"
    pull_with_retry "$pg_img" || { mirror_help "$pg_img"; exit 18; }
  fi
  export POSTGRES_IMAGE="$pg_img"

  local need=(golang:1.25-alpine node:22-alpine alpine:3.20)
  local img
  for img in "${need[@]}"; do
    if image_exists "$img"; then
      continue
    fi
    log "拉取构建基础镜像 $img"
    pull_with_retry "$img" || { mirror_help "$img"; exit 18; }
  done
  log "镜像就绪（postgres 使用 ${pg_img}）"
}

port() {
  local p
  p="$(grep -E '^WEB_PORT=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- || true)"
  echo "${p:-8088}"
}

# ---------- .env ----------
gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 36 | tr -d '\n=+/' | cut -c1-40
  else
    LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 40
  fi
}

ensure_env() {
  if [[ -f "$ENV_FILE" ]]; then
    log "使用已有配置 $ENV_FILE"
  else
    [[ -f "$DEPLOY_DIR/.env.example" ]] || die "缺少 .env.example"
    umask 077
    local db jwt media admin
    db="$(gen_secret)"; jwt="$(gen_secret)"; media="$(gen_secret)"; admin="Adm$(gen_secret | cut -c1-12)!"
    sed -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${db}|" \
        -e "s|^JWT_SECRET=.*|JWT_SECRET=${jwt}|" \
        -e "s|^MEDIA_SIGN_SECRET=.*|MEDIA_SIGN_SECRET=${media}|" \
        -e "s|^ADMIN_INIT_PASSWORD=.*|ADMIN_INIT_PASSWORD=${admin}|" \
        "$DEPLOY_DIR/.env.example" > "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    log "已生成 ${ENV_FILE}（随机密钥）"
  fi
  chmod 600 "$ENV_FILE" 2>/dev/null || true

  # 占位符未替换就拒绝启动：宁可当场失败，也不要带着 "change-me" 上生产
  if grep -qE 'change-me|CHANGE_ME' "$ENV_FILE"; then
    die "$ENV_FILE 里还有 change-me 占位符没替换，请填入真实密码后重试"
  fi
  local jwt media
  jwt="$(grep -E '^JWT_SECRET=' "$ENV_FILE" | cut -d= -f2-)"
  media="$(grep -E '^MEDIA_SIGN_SECRET=' "$ENV_FILE" | cut -d= -f2-)"
  [[ "$jwt" != "$media" ]] || die "JWT_SECRET 与 MEDIA_SIGN_SECRET 不能相同"
  [[ ${#jwt} -ge 16 ]] || die "JWT_SECRET 太短（至少 16 字符）"
}

# ---------- 健康检查 ----------
wait_healthy() {
  local p; p="$(port)"
  local url="http://127.0.0.1:${p}/healthz"
  log "等待服务就绪：$url"
  for i in $(seq 1 60); do
    if curl -fsS --max-time 3 "$url" 2>/dev/null | grep -q '"status":"ok"'; then
      log "${C_OK}服务已就绪（${i} 次探测）${C_END}"
      return 0
    fi
    sleep 2
    if (( i % 10 == 0 )); then dim "  仍在等待…（${i}/60）"; fi
  done
  printf '\n%s健康检查超时%s\n' "$C_ERR" "$C_END" >&2
  dc ps || true
  dc logs --tail=60 api || true
  die "服务未能就绪，请按上面的日志排查"
}

smoke_test() {
  local p; p="$(port)"
  local admin_pw base
  admin_pw="$(grep -E '^ADMIN_INIT_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
  base="http://127.0.0.1:${p}"
  log "冒烟：登录接口 + 静态页面"
  local body
  body="$(curl -fsS --max-time 10 -X POST "$base/api/v1/auth/login" \
      -H 'Content-Type: application/json' \
      -d "{\"username\":\"admin\",\"password\":\"${admin_pw}\"}" || true)"
  if grep -q 'access_token' <<<"$body"; then
    log "${C_OK}管理员登录成功${C_END}"
  else
    warn "管理员登录失败（首次部署以外的正常现象：密码已被改过）。响应：${body:0:160}"
  fi
  curl -fsS --max-time 10 "$base/" | grep -q '<div id="root">' \
    && log "${C_OK}前端页面可访问${C_END}" \
    || warn "前端页面没有返回 SPA 入口，检查 nginx 容器日志"
}

# ---------- 子命令 ----------
cmd_deploy() {
  check_prereq
  ensure_env
  preflight_images
  log "构建并启动（首次会编译前后端，约 3~8 分钟）"
  dc up -d --build --remove-orphans
  wait_healthy
  smoke_test
  summary
}

cmd_upgrade() {
  check_prereq
  [[ -f "$ENV_FILE" ]] || die "还没部署过（缺 .env），请先执行 $0 deploy"
  ensure_env
  preflight_images
  log "重建镜像并重启（数据库数据卷保留，迁移随启动自动执行）"
  dc up -d --build --remove-orphans
  wait_healthy
  summary
}

cmd_status() {
  check_prereq
  [[ -f "$ENV_FILE" ]] || die "还没部署过（缺 .env）"
  dc ps
  echo
  local p; p="$(port)"
  if curl -fsS --max-time 3 "http://127.0.0.1:${p}/healthz" >/dev/null 2>&1; then
    log "${C_OK}健康检查通过 http://127.0.0.1:${p}/healthz${C_END}"
  else
    warn "健康检查失败：http://127.0.0.1:${p}/healthz"
  fi
  echo
  dim "数据卷占用："
  docker system df -v 2>/dev/null | grep -E "font-label_(pgdata|storage)" || dim "  （未创建）"
}

cmd_logs() {
  check_prereq
  [[ -f "$ENV_FILE" ]] || die "还没部署过（缺 .env）"
  dc logs -f --tail=200 "${1:-}"
}

cmd_backup() {
  check_prereq
  [[ -f "$ENV_FILE" ]] || die "还没部署过（缺 .env）"
  mkdir -p "$BACKUP_DIR"
  local ts; ts="$(date +%Y%m%d_%H%M%S)"
  local sql="$BACKUP_DIR/fontlabel_${ts}.sql.gz"
  local tar="$BACKUP_DIR/storage_${ts}.tar.gz"

  log "备份数据库 → $sql"
  dc exec -T pg pg_dump -U fontlabel -d fontlabel | gzip > "$sql"
  [[ -s "$sql" ]] || die "pg_dump 输出为空，备份失败"

  log "备份图片 → $tar"
  docker run --rm \
    -v "${PROJECT}_storage:/data:ro" \
    -v "$BACKUP_DIR:/backup" \
    alpine:3.20 tar czf "/backup/storage_${ts}.tar.gz" -C /data . \
    || die "图片备份失败（数据卷名应为 ${PROJECT}_storage）"

  local sz1 sz2
  sz1="$(du -h "$sql" | cut -f1)"; sz2="$(du -h "$tar" | cut -f1)"
  log "${C_OK}备份完成：${sql} (${sz1})  ${tar} (${sz2})${C_END}"

  if [[ "$BACKUP_KEEP" =~ ^[1-9][0-9]*$ ]]; then
    log "清理旧备份，只保留最近 ${BACKUP_KEEP} 份"
    ls -1t "$BACKUP_DIR"/fontlabel_*.sql.gz 2>/dev/null | tail -n +"$((BACKUP_KEEP+1))" | while read -r f; do
      dim "  删除 $f"; rm -f -- "$f"
    done
    ls -1t "$BACKUP_DIR"/storage_*.tar.gz 2>/dev/null | tail -n +"$((BACKUP_KEEP+1))" | while read -r f; do
      dim "  删除 $f"; rm -f -- "$f"
    done
  fi
}

cmd_restore() {
  check_prereq
  local file="${1:-}"
  [[ -n "$file" ]] || die "用法：$0 restore <备份文件.sql.gz | storage_*.tar.gz>"
  [[ -f "$file" ]] || die "文件不存在：$file"
  warn "恢复会覆盖现有数据（数据库或图片），且不可撤销。"
  read -r -p "确认继续？输入 yes 回车：" ans
  [[ "$ans" == "yes" ]] || die "已取消"

  case "$file" in
    *.sql.gz)
      log "停止 API（避免恢复期间写入）"
      dc stop api || true
      log "恢复数据库 ← $file"
      gunzip -c "$file" | dc exec -T pg psql -U fontlabel -d fontlabel -v ON_ERROR_STOP=1 >/dev/null
      log "重启服务"
      dc start api || dc up -d api
      wait_healthy
      log "${C_OK}数据库恢复完成${C_END}"
      ;;
    *.tar.gz)
      log "恢复图片 ← $file"
      docker run --rm \
        -v "${PROJECT}_storage:/data" \
        -v "$(cd "$(dirname "$file")" && pwd):/backup:ro" \
        alpine:3.20 sh -c "tar xzf /backup/$(basename "$file") -C /data" \
        || die "解包失败"
      log "${C_OK}图片恢复完成（建议重启 api：$0 upgrade 或 docker restart）${C_END}"
      ;;
    *)
      die "无法识别的备份文件类型（需要 .sql.gz 或 .tar.gz）"
      ;;
  esac
}

cmd_down() {
  check_prereq
  dc down
  log "已停止容器（数据卷保留，重新 $0 deploy 即可恢复）"
}

cmd_purge() {
  check_prereq
  warn "这会删除容器 + 数据库数据卷 + 全部图片，且不可恢复。"
  read -r -p "确认？输入 yes 回车：" a1
  [[ "$a1" == "yes" ]] || die "已取消"
  read -r -p "再确认一次，输入 DELETE 回车：" a2
  [[ "$a2" == "DELETE" ]] || die "已取消"
  dc down -v --remove-orphans
  log "已清空（含数据卷）"
}

summary() {
  local p admin_pw ip
  p="$(port)"
  admin_pw="$(grep -E '^ADMIN_INIT_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
  ip="$(ipconfig getifaddr en0 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}' || echo '')"
  echo
  log "──────────────────────────────────────────────"
  log "访问地址：   http://127.0.0.1:${p}/"
  [[ -n "$ip" ]] && log "局域网访问： http://${ip}:${p}/"
  # 交互式终端才打印初始密码；重定向/CI 里只给位置，避免密码进日志
  if [[ -t 1 ]]; then
    log "管理员账号： admin / ${admin_pw}（首次登录强制改密）"
  else
    log "管理员账号： admin（初始密码见 ${ENV_FILE} 的 ADMIN_INIT_PASSWORD，首次登录强制改密）"
  fi
  log "配置文件：   ${ENV_FILE}"
  log "常用命令：   $0 status | logs | backup | upgrade | down"
  log "──────────────────────────────────────────────"
  dim "下一步：登录 → 改密 → 用户管理建标注员/审核员 → 上传图片 → 领取标注 → 审核 → 导出"
}

usage() {
  awk 'NR>1 {
         if ($0 ~ /^#/) { sub(/^# ?/, ""); print; next }
         if ($0 ~ /^[[:space:]]*$/) { print ""; next }
         exit
       }' "$0"
}

main() {
  local cmd="${1:-deploy}"
  [[ $# -gt 0 ]] && shift || true
  case "$cmd" in
    deploy|install|up) cmd_deploy ;;
    upgrade)           cmd_upgrade ;;
    status|ps)         cmd_status ;;
    logs)              cmd_logs "${1:-}" ;;
    backup)            cmd_backup ;;
    restore)           cmd_restore "${1:-}" ;;
    down|stop)         cmd_down ;;
    purge)             cmd_purge ;;
    -h|--help|help)    usage ;;
    *) die "未知子命令：${cmd}（用 --help 看用法）" ;;
  esac
}

main "$@"
