# 部署说明（单机 Docker Compose）

## 前置

- Docker 24+ / Docker Compose v2
- 一个可用端口（默认 8088）
- 建议域名 + HTTPS（在外层再加一层反代或直接给 nginx 配证书）

## 步骤

```bash
cd deploy
cp .env.example .env
# 必改三项：POSTGRES_PASSWORD / JWT_SECRET / MEDIA_SIGN_SECRET / ADMIN_INIT_PASSWORD
# 生成随机值：openssl rand -base64 32
chmod 600 .env
docker compose up -d --build
curl -s http://127.0.0.1:8088/healthz     # {"status":"ok"}
```

首次启动会自动：

1. 建库建表（goose 迁移）
2. 写入默认评分方案（五维百分制：白平衡/黑平衡/参差/结构/控笔，各 20 分）与 10 项系统配置
3. 创建管理员 `admin`（密码取 `ADMIN_INIT_PASSWORD`，首次登录强制改密）

访问 `http://<服务器>:8088`，用 admin 登录 → 改密 → 建账号 → 上传图片。

## 数据结构

| 位置 | 内容 | 备份 |
| --- | --- | --- |
| `pgdata` 卷 | PostgreSQL 数据 | `docker compose exec pg pg_dump -U fontlabel fontlabel > backup.sql` |
| `storage` 卷 | 图片原图 + `.derived/` 预览缩略图 + `.exports/` 导出文件 | `docker run --rm -v font-label_storage:/d -v $PWD:/b alpine tar czf /b/storage.tgz -C /d .` |

每日备份建议（crontab）：

```cron
0 3 * * * cd /opt/font-label/deploy && docker compose exec -T pg pg_dump -U fontlabel fontlabel | gzip > /backup/fontlabel-$(date +\%F).sql.gz
30 3 * * * cd /opt/font-label/deploy && docker run --rm -v font-label_storage:/d -v /backup:/b alpine tar czf /b/storage-$(date +\%F).tgz -C /d .
```

## 扩容与取舍

- 单机足够支撑 50 人并发标注（领取 51ms / 250 张，见 `server/internal/app/claim_concurrency_test.go`）。
- 图片走 nginx → api 的签名 URL，不暴露目录；如需 CDN，把 `/api/v1/media/` 缓存进 CDN 即可（URL 自带 5~60 分钟过期签名）。
- 多实例部署时把 worker 从 API 进程拆出去（`WORKER_IN_API=false` + 独立 `worker` 服务，需给导出任务加 `locked_until` 抢占，见 docs/02 §6.4）。
- 存储换 S3/MinIO：实现 `internal/platform/storage` 的同一接口即可，业务代码不改。

## 升级

```bash
git pull
cd deploy && docker compose up -d --build
```
迁移在启动时自动执行（goose 幂等），回滚用 `goose down` 需人工确认。

## 上传标注结果和图片到阿里云 OSS

在部署机上创建仅部署账号可读的 `deploy/.env.oss`（权限 `600`）。下方是配置示例，凭据填写新签发的 RAM AccessKey。

```dotenv
OSS_BUCKET=biaozhushuju
OSS_REGION=cn-chengdu
OSS_ENDPOINT=https://oss-cn-chengdu.aliyuncs.com
OSS_EXPORT_PREFIX=font-label/exports
OSS_ACCESS_KEY_ID=
OSS_ACCESS_KEY_SECRET=
# 使用 STS 临时凭据时同时设置 OSS_SESSION_TOKEN。
```

成都地域的阿里云 ECS 可将 `OSS_ENDPOINT` 改为 `https://oss-cn-chengdu-internal.aliyuncs.com`。RAM 权限按对象前缀授予 `oss:PutObject`。SDK 清理上传失败的分片需要 `oss:AbortMultipartUpload`。

`./deploy/deploy.sh upgrade` 会加载此文件。直接用 Compose 部署时，在 `deploy` 目录运行 `docker compose --env-file .env --env-file .env.oss up -d --build`。

管理员在「导出结果」选好筛选条件后，点击「上传结果和图片到 OSS」。任务在后台打包一个 ZIP，包内的 `annotations.json` 通过 `image_path` 引用 `images/` 中的原图。同一原图的多组标注共享包内的图片条目。上传完成后可从导出记录获取对象键和对象地址。私有桶可通过 OSS 控制台或有权限的客户端访问。服务端打包时需要可容纳 ZIP 的临时磁盘空间，上传后会清理临时文件。
