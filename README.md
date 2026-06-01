# 轻发布 (Code Dep)

轻量级项目编译发布系统，支持多环境部署、版本回退、实时日志。Go 单文件部署，开箱即用。

## 功能特性

- **项目管理**：多项目支持，自动编号，分组管理
- **Git 集成**：自动拉取分支/Tag，支持 SSH 和 HTTPS
- **多环境发布**：SIT/UAT/PROD 等环境独立配置，编译命令、发布命令可分别定义
- **实时日志**：SSE 实时推送编译发布日志，支持终止发布任务
- **版本回退**：历史版本一键重新部署，跳过编译直接发布
- **多节点发布**：支持同时发布到多台服务器，SSH 远程和本地目录
- **秘钥管理**：Git/SSH/API 多种类型，AES-256-GCM 加密存储
- **通知推送**：企业微信、飞书 Webhook 通知
- **节点控制台**：WebSocket 交互式终端，远程执行命令
- **用户权限**：管理员/普通用户，项目级授权（可运行/可修改）

## 快速开始

### 下载预编译版本

从 [Releases](https://github.com/tim-today/Code-Dep/releases) 页面下载对应平台的压缩包：

- `code-dep-*-linux-amd64.tar.gz` — Linux x86_64
- `code-dep-*-linux-arm64.tar.gz` — Linux ARM64
- `code-dep-*-darwin-amd64.tar.gz` — macOS Intel
- `code-dep-*-darwin-arm64.tar.gz` — macOS Apple Silicon
- `code-dep-*-windows-amd64.tar.gz` — Windows x86_64

解压后运行：

```bash
# Linux / macOS
tar -xzf code-dep-*.tar.gz && cd code-dep-*
./code-dep

# Windows
# 解压后双击 code-dep.exe 或在终端运行
```

### 从源码编译

```bash
go build -o server ./cmd/server
./server
```

首次启动自动创建默认管理员：`admin` / `123456`，请登录后立即修改密码。

### Docker 部署

```bash
# 使用 docker-compose
docker-compose up -d

# 或手动构建运行
docker build -t code-dep .
docker run -d -p 8080:8080 -v code-dep-data:/app/data --name code-dep code-dep
```

## 技术栈

- **后端**：Go 标准库 + `golang.org/x/crypto/ssh` + `github.com/gorilla/websocket`
- **前端**：原生 HTML/CSS/JS，零框架依赖
- **存储**：文件系统，AES-256-GCM 加密敏感数据
- **实时通信**：SSE（日志推送）+ WebSocket（节点控制台）

## 目录结构

```
code-dep/
├── cmd/server/          # 后端入口
│   ├── main.go          # 路由、业务逻辑
│   └── storage.go       # 文件存储、加密
├── web/static/          # 前端资源
│   ├── index.html
│   ├── app.js
│   └── style.css
├── data/                # 运行时数据（自动生成，不提交）
│   ├── .key             # 加密密钥
│   ├── system/          # 系统配置（秘钥、节点、用户等）
│   └── projects/        # 项目配置和发布记录
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## 数据安全

- 秘钥密码、Token、私钥等敏感字段使用 AES-256-GCM 加密存储
- 用户密码使用 bcrypt 单向哈希
- 加密密钥自动生成在 `data/.key`，权限 0600

## 开源协议

[MIT License](LICENSE)

## 社区

- [GitHub](https://github.com/tim-today/Code-Dep)
- [Issues](https://github.com/tim-today/Code-Dep/issues)
