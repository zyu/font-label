# font-label · 硬笔书法作业评分标注平台

学生硬笔书法作业照片的**上传 → 领取 → 标注评分 → 审核打回 → 统计 → 导出**全流程 Web 平台。

- 后端：Go 1.25 + gin + PostgreSQL 16
- 前端：React 19 + TypeScript + Vite + Ant Design 6
- 存储：服务器本地磁盘（可切 S3/MinIO）

## 文档（交给开发，按顺序读）

| 文档 | 内容 | 谁看 |
| --- | --- | --- |
| [`docs/01-产品需求文档.md`](docs/01-产品需求文档.md) | 角色权限、业务流程、状态机、功能规格、评分规则、页面清单、里程碑、待确认问题 | 产品 / 全员 |
| [`docs/02-技术方案与数据设计.md`](docs/02-技术方案与数据设计.md) | 选型、目录结构、完整 DDL、关键 SQL、REST 接口契约、图片存储与签名、配置项、部署、风险 | 后端 / 架构 |
| [`docs/03-验收标准与测试用例.md`](docs/03-验收标准与测试用例.md) | A-01~A-52 功能用例、N-01~N-08 非功能用例、对账 SQL、并发测试脚本、签字表 | 开发自测 / 甲方验收 |
| [`docs/04-开发任务拆解与排期.md`](docs/04-开发任务拆解与排期.md) | M0~M6 任务表、人日、依赖、接口先行清单、初始配置数据、不做清单 | 开发负责人 |

## 需求要点（一句话版）

1. 管理员上传图片，服务端按 **`当天日期_三位序号`**（如 `20261022_001.png`）重命名落盘，同日序号全局自增。
2. 标注员可**按张领取 N 张**，也可**按文件夹领取**该文件夹剩余全部；领取即锁定，**他人不可再领**。
3. 标注页 **左图右标注**：五维（白平衡/黑平衡/参差/结构/控笔，各 0~20）打分 + 总分自动相加 + 20~50 字点评；不合格图片走「上报异常」（6 类原因）。
4. 审核员逐条审核，**通过**或**批注打回**；打回任务留在原标注员名下，标注员在「已驳回」看到并重做。
5. 统计看板：每个标注员的领取数、提交数、通过数、驳回率、一次通过率、平均分、耗时，以及驳回原因分布。
6. 导出 xlsx/csv/json，可按**文件夹名称**、日期区间、标注员、状态筛选。

## 目录

```
font-label/
├── docs/                      # 需求 / 技术 / 验收 / 排期
├── 标注看图示例/               # 甲方提供的 4 张示例图（优秀/中等/较差/异常）
├── 标注看图示例 ....md         # 甲方原始示例文档（评分口径来源）
├── server/                    # Go 后端（待开发，结构见 02 文档 §2）
├── web/                       # React 前端（待开发）
└── storage/                   # 图片根目录（运行时生成）
```

> 前端实现「查看示例」抽屉时，把 `标注看图示例/` 里 4 张 png 复制到 `web/public/examples/`，示例文案提炼进 `web/src/content/scoring-guide.ts`。

## 已确认的关键决策

| 决策 | 结论 |
| --- | --- |
| 文件夹定义 | 上传时指定的批次名，默认当天日期 `YYYYMMDD`；图片路径 `storage/<文件夹>/<YYYYMMDD>_<NNN>.<ext>` |
| 领取并发控制 | PostgreSQL 原子 `SELECT ... FOR UPDATE SKIP LOCKED` + 文件夹 `pg_advisory_xact_lock`，不依赖 Redis |
| 领取模式 | `按张 N 张` / `按文件夹剩余全部`，二选一 |
| 评分总分 | 后端按可配置 rubric 计算并留快照，前端只做实时预览 |
| 审核自审 | 后端强制禁止审核自己标注的图片 |

## 待甲方确认（不阻塞开发，先按默认值做）

见 `docs/01-产品需求文档.md` §11，共 8 项：五维权重与等级线、是否要班级维度、多级审核、姓名打码策略、账号来源、部署环境、图片 zip 打包、持有上限与领取有效期。

## 环境（本机已具备）

| 项 | 版本 / 说明 |
| --- | --- |
| Go | 1.25.3 |
| Node | 22.13.0 |
| Docker | 27.4.0 |
| PostgreSQL | 16（Docker 运行中，`127.0.0.1:15432`，沿用现有实例；本项目建议独立 database `fontlabel`） |
| 端口 | API `18090`、Vite `5180`、生产 nginx `8088`（避开已占用的 18080/8100/9000） |

## 本地运行（已跑通）

### 1. 数据库
```bash
docker run -d --name fontlabel-pg -e POSTGRES_PASSWORD=fontlabel -e POSTGRES_DB=fontlabel \
  -p 127.0.0.1:15433:5432 postgres:16 -c timezone=Asia/Shanghai
```

### 2. 后端（首次启动自动迁移 + 建管理员）
```bash
cd server
export DATABASE_URL='postgres://postgres:fontlabel@127.0.0.1:15433/fontlabel_dev?sslmode=disable'
export STORAGE_ROOT="$(pwd)/../storage"
export JWT_SECRET='dev-jwt-secret-font-label-0123456789abcdef'
export MEDIA_SIGN_SECRET='dev-media-secret-font-label-0123456789'
export ADMIN_INIT_PASSWORD='Admin@12345'
go run ./cmd/api          # 监听 :18090，健康检查 GET /healthz
```
首次启动会 seed 管理员 `admin / Admin@12345`（首次登录强制改密）。

### 3. 前端
```bash
cd web
npm install
npm run dev               # http://127.0.0.1:5180（/api 代理到 :18090）
```

### 4. 验证
```bash
make test          # go vet + go test（含 250 张 / 50 并发的「领取不重复」压测）
make e2e           # 51 条端到端断言（可重复跑，结束自动归档测试文件夹）
cd web && npm test # 组件测试：总分算法 + 打分面板（8 条）
cd web && npm run build
```

## 生产部署

```bash
cd deploy && cp .env.example .env    # 改 4 个密码/密钥
docker compose up -d --build         # pg + api + nginx
curl -s http://127.0.0.1:8088/healthz
```
详见 `deploy/README.md`（备份、升级、扩容取舍、存储换 S3）。

## 实现进度

| 模块 | 状态 |
| --- | --- |
| 后端（Go/Gin + PG）：认证、用户、文件夹、上传、领取、标注、审核、统计、导出、审计 | ✅ 已完成，E2E 51/51，并发领取 50 并发零重复 |
| 前端（React19 + AntD6）：登录/工作台/标注页/审核页/统计/上传/文件夹/用户/导出/设置 | ✅ 已完成，浏览器实跑通过，路由懒加载 + 依赖分包 |
| Excel/CSV/JSON 导出 | ✅ 含表头样式、列宽、冻结首行、CSV BOM |
| 部署（Docker Compose + nginx + 备份脚本） | ✅ `deploy/`，`docker compose config` 校验通过 |
| 图上批注（圈注）| ⬜ P1（`reviews.drawings` 字段已预留） |
| 图片 zip 打包、多级审核、手机端 | ⬜ 首版不做 |

## 交给开发的第一句话

「先读 `docs/01`，再读 `docs/02` §3（DDL）与 §4（接口），按 `docs/04` 的 M0 开始；每完成一个任务用 `docs/03` 对应编号自测并回填用例号。」




  前端 http://127.0.0.1:5180  后端 http://127.0.0.1:18090  库 127.0.0.1:15433
  账号 admin/Admin@12345、demo_wang/Passw0rd!、demo_li/Passw0rd!