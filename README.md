# CodeDep

[中文文档](README_zh.md)

Lightweight automated build & deploy system with multi-environment support, version rollback, and real-time logs. Single Go binary, zero dependencies.

## Features

- **Project Management**: Multi-project support, auto-numbering, group management
- **Git Integration**: Auto-fetch branches/tags, SSH and HTTPS support
- **Multi-Environment Deploy**: SIT/UAT/PROD configs with independent build and deploy scripts
- **Real-time Logs**: SSE live log streaming with task cancellation
- **Version Rollback**: One-click redeploy from history, skip build and deploy directly
- **Worker Pool**: Weighted worker selection with busy-state detection for build tasks
- **Cross-Platform Build**: GOOS/GOARCH support for Linux, macOS, and Windows targets
- **Multi-Node Deploy**: Deploy to multiple servers simultaneously via SSH or local paths
- **Secret Management**: Git/SSH/API secrets with AES-256-GCM encryption
- **Notifications**: WeCom and Feishu Webhook notifications
- **Node Console**: WebSocket interactive terminal for remote command execution
- **User Permissions**: Admin/regular users with project-level run/edit authorization
- **i18n**: Simplified Chinese, English, and Traditional Chinese UI with a universal `🌐` language selector

## Quick Start

### Download Prebuilt Binaries

Download from [Releases](https://github.com/tim-today/Code-Dep/releases):

- `code-dep-*-linux-amd64.tar.gz` — Linux x86_64
- `code-dep-*-linux-arm64.tar.gz` — Linux ARM64
- `code-dep-*-darwin-amd64.tar.gz` — macOS Intel
- `code-dep-*-darwin-arm64.tar.gz` — macOS Apple Silicon
- `code-dep-*-windows-amd64.tar.gz` — Windows x86_64

```bash
# Linux / macOS
tar -xzf code-dep-*.tar.gz && cd code-dep-*
./code-dep

# Windows: extract and run code-dep.exe
```

### Build from Source

```bash
go build -o server ./cmd/server
./server

# Custom port
PORT=9000 ./server
```

Default admin: `admin` / `123456`. Change password after first login.

Open `http://localhost:8080` by default. The login page defaults to Simplified Chinese; use the `🌐` selector to switch languages.

### Docker

```bash
docker-compose up -d

# Or manually
docker build -t code-dep .
docker run -d -p 8080:8080 -v code-dep-data:/app/data --name code-dep code-dep
```

## Tech Stack

- **Backend**: Go stdlib + `golang.org/x/crypto/ssh` + `github.com/gorilla/websocket`
- **Frontend**: Vanilla HTML/CSS/JS, zero framework dependencies
- **Storage**: Filesystem with AES-256-GCM encryption for sensitive data
- **Realtime**: SSE (log streaming) + WebSocket (node console)

## Project Structure

```
code-dep/
├── cmd/server/          # Backend entry
│   ├── main.go          # Routes, business logic
│   └── storage.go       # File storage, encryption
├── web/static/          # Frontend assets
│   ├── index.html
│   ├── app.js
│   ├── i18n.js          # Internationalization
│   └── style.css
├── data/                # Runtime data (auto-created, not committed)
├── .github/workflows/   # CI/CD
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## Security

- Secrets (passwords, tokens, private keys) are encrypted with AES-256-GCM
- User passwords use bcrypt one-way hashing
- Encryption key auto-generated at `data/.key` with 0600 permissions

## License

[MIT License](LICENSE)

## Links

- [GitHub](https://github.com/tim-today/Code-Dep)
- [Issues](https://github.com/tim-today/Code-Dep/issues)
- [中文文档](README_zh.md)
