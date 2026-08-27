# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

OpenList 是 AList 的 AGPL-3.0 社区分支。它把约 88 个云存储后端（称为 driver）挂载成一棵统一的虚拟文件系统，再通过 HTTP API、WebDAV、S3、FTP、SFTP 和 MCP 对外暴露。模块路径是 `github.com/OpenListTeam/OpenList/v4`，导入时**必须带 `/v4`**。Web UI 在独立仓库 OpenList-Frontend，本仓库只消费其预构建产物 `dist`。

## 常用命令

构建与运行 —— 不需要 CGO，不需要外部工具，前端 `dist` **也不是必需的**（`public/dist` 只有一个占位 README，`//go:embed all:dist` 仍能匹配成功）：

```shell
go build -tags=jsoniter -o openlist .   # 已在干净 checkout 上实测通过
go run main.go server                   # server 才是启动服务的子命令
air                                     # 热重载；.air.toml 已配置好传 server
bash build.sh dev web                   # 仅当需要 Web UI 时：下载 dist 到 public/dist
```

检查：

```shell
go test ./...
go test ./internal/fs/... -run TestList -v   # 跑单个测试
go vet -printf=false ./...                   # 不加 -printf=false 会报 13 处上游遗留的 non-constant format string
gofmt -l internal server cmd drivers pkg     # 当前整棵树是 gofmt-clean 的
```

CI 门禁（`.github/workflows/ci.yml`）跑的就是 gofmt、`go vet -printf=false` 和
linux/amd64 + linux/arm64 交叉编译；Docker 镜像发布见 `.github/workflows/docker.yml`。

运维类子命令直接操作 data 目录：

```shell
go run main.go admin            # 查看 admin 用户名；admin set PWD / admin random 重置密码
go run main.go storage list     # 另有 storage disable <mount path>、storage delete <id>
go run main.go cancel2fa <user>
```

正式发布走 `build.sh`（`BuildDev`、`BuildRelease` 等）。CI（`.github/workflows/build.yml`）用 `OpenListTeam/cgo-actions` 交叉编译，并通过 `-ldflags -X github.com/OpenListTeam/OpenList/v4/internal/conf.*` 注入版本信息；直接 `go build` 会让这些字段停留在 `dev`/`unknown`，属正常现象。sqlite driver 由构建 tag 决定——默认走纯 Go 的 glebarez，带 `sqlite_cgo_compat` 时切到 gorm 的 CGO 版（build.sh 里的 `GetBuildTagsForTarget` 会为 mips/loong64/windows-386 目标自动加上）。见 `internal/bootstrap/sqlite_driver_*.go`。

## 架构

调用严格自上而下：

```
cmd/ (cobra) → internal/bootstrap → server/ (gin + 各协议服务端)
    → internal/fs → internal/op → internal/driver ← drivers/*
```

### 挂载路径 / 实际路径的分界线

这是理解 fs 与 op 为什么要分两层的关键不变量。`internal/fs` 接收的是**挂载路径**（用户看到的虚拟树），它存在的全部意义就是解析路径然后转交下游——见 `internal/fs/fs.go:17` 的包注释。解析发生在 `op.GetStorageAndActualPath`（`internal/op/path.go:16`）：对已挂载的 storage 做最长前缀匹配，再剥掉挂载前缀。`internal/op` 只会见到**实际路径**（相对于 storage 根）。

**不要**从 `server/` 直接调 driver，也**不要**把挂载路径传给 `op`。挂载路径中不属于任何 storage 的顶层片段，会在解析之前由 `internal/fs/get.go` 合成为"虚拟文件"。

### internal/op —— 包在 driver 外面的缓存、singleflight 与 hook

`op` 不是薄转发层，它给每次 driver 调用都套了一层：

- `op.Cache`（`internal/op/cache.go:14`）管五套缓存：目录列表（5 分钟）、link（30 分钟）、user（1 小时）、setting（1 小时）、storage 详情（30 分钟）。storage 自身的 `CacheExpiration` 与 `CustomCachePolicies`（glob:ttl 规则）会覆盖目录列表的 TTL；`Config().NoCache` 直接跳过缓存。
- 同一路径的并发 list 由 `singleflight` 合并（`internal/op/fs.go:23`）。
- 写操作会失效受影响的子树（`Cache.DeleteDirectoryTree`）——**新增任何写路径都必须同步做失效**。
- `storagesMap`（`internal/op/storage.go`）是内存里的挂载表。storage 是启动后**异步**加载的，这正是每个请求都要过 `middlewares.StoragesLoaded` 的原因。

### drivers

`driver.Driver` 被刻意做得极小——只有 `Meta` + `Reader`（`List`/`Link`），别无其他（`internal/driver/driver.go:9`）。其余能力全是可选接口，由 `op` 在调用时 type-assert：`Mkdir`/`Move`/`Rename`/`Copy`/`Remove`/`Put`/`PutURL`、`Archive*` 家族，以及 `WithDetails`、`Reference`、`DirectUploader`、`GetRooter`/`Getter`、`LinkCacheModeResolver`。

多数写操作有两种形态——`Move` 对 `MoveResult`、`Put` 对 `PutResult` 等等。带 `Result` 的变体会返回操作结果的 `model.Obj`，让 `op` 能**更新**缓存而不是丢弃它。**优先实现 `*Result` 形态**。

`errs.NotImplement` 是控制流信号，不只是一个错误：从 archive 相关钩子返回它，是在告诉 `op` 回退到内置的 archive 工具。`errs.NotSupport` 语义不同——见 `internal/driver/driver.go:163` 的接口注释。

新增一个 driver：

1. 复制 `drivers/template/`（里面的注释就是真正的契约），重命名 package。
2. 配置项声明为 `Addition` 的字段。结构体 tag（`json`、`type`、`required`、`options`、`default`、`help`）会被 `op.registerDriverItems`（`internal/op/driver.go:46`）反射成管理后台的表单项——没有另一份 schema 需要维护。
3. 在 `drivers/<name>/meta.go` 的 `init()` 里调 `op.RegisterDriver`。
4. **往 `drivers/all.go` 加空导入。** 除此之外没有任何地方导入 driver 包，所以漏了这一行的 driver 能正常编译，但运行时根本不存在。

写上传逻辑之前，先读 `driver.Put` 上那段很长的契约注释（`internal/driver/driver.go:79`）：响应 `ctx` 取消、通过 `up` 实时上报进度、以及为限速包装读流（`driver.RateLimitReader` / `NewLimitedUploadStream`），这三件都是**必需**而非可选。

同样的"空导入即注册"模式也管着 `internal/archive/all.go`、`internal/offline_download/all.go` 和 `internal/search/import.go`。

### server/ —— 一套文件系统，多种协议

`server/router.go:19` 把所有东西挂在 `conf.URL.Path` 之下：`/api/**`（handler 在 `server/handles/`，按业务域拆分——fsread/fsmanage/fsup/archive/storage/user/task/sharing 等），加上 `/dav`、`/s3`、`/mcp`，以及免鉴权的签名直链 `/d` `/p`（下载、代理）、`/ad` `/ap` `/ae`（压缩包）和 `/sd` `/sad`（分享）。

WebDAV、S3、FTP、SFTP、MCP 是各自独立的服务端（`server/webdav`、`server/s3`、`server/ftp`、`server/sftp`、`server/mcp`），全部经由 `internal/fs` 访问存储；FTP 与 SFTP 共用同一个 afero 适配层（`server/ftp/afero.go`）。**协议特有的行为应留在这些适配层里，不要下沉到 `fs`/`op`。**

三套鉴权机制并存（`server/middlewares/auth.go`）：

- 来自 setting 的 admin token，与 `Authorization` 头做常量时间比较；
- 用户级 JWT（`common.ParseToken`），靠递增用户的 `PwdTS` 使其失效；
- 直链用的 HMAC `sign` 查询参数（`internal/sign`），由 `middlewares.Down` 校验。

### 进程生命周期

由 `internal/bootstrap/run.go` 掌管。`Init()` 顺序为：config → log → DB（`gorm` + 对 `internal/db/db.go:13` 那八个 model 做 `AutoMigrate`）→ 种子数据 → 流限速 → 搜索索引 → 升级补丁。随后 `Start()` 把 HTTP/HTTPS/HTTP3/unix socket/S3/FTP/SFTP 各自放进独立 goroutine，`Shutdown()` 在同一个超时下并发关停。

### 配置与 data 目录

`--data`（默认 `./data`）存放 `config.json`、`data.db`、日志、临时文件和 bleve 索引。`conf.Config` 的每个字段都可用 `OPENLIST_` 前缀的环境变量覆盖（`internal/bootstrap/config.go:173`），嵌套结构通过 `envPrefix` tag 展开——例如 `OPENLIST_DB_TYPE`、`OPENLIST_FTP_ENABLE`。数据库支持 sqlite3（默认）、MySQL 和 Postgres。

## 约定

- `go.mod` 声明 Go 1.25，toolchain 1.26.4；CI 用 1.26.4 构建。
- 优先使用 `internal/errs` 里已有的哨兵错误，而不是新写错误字符串——其中若干个会驱动 `op` 的回退行为。
- commit 与 PR 规范见 `AGENTS.md`：Conventional Commits（`type(scope): subject`）、正文用 Markdown 列表、最多一个 `Co-authored-by` trailer 且必须对应真实使用的 AI 助手。`CONTRIBUTING.md` 另要求在 PR 描述中披露实质性的 AI 辅助，`.github/PULL_REQUEST_TEMPLATE.md` 规定了 PR 的必需结构。
