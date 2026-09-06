---
description: How to build and deploy to test and production environments
---

# Deploy Workflow

> [!CAUTION]
> **NEVER modify `docker-compose.yml` or `docker-compose.test.yml` without explicit user approval.**
> **NEVER run `docker compose down -v` on production — this DELETES all data volumes.**
> **ALWAYS confirm environment details with the user before deploying.**

## Environment Overview

| Environment | Container | Port | Database Container | Database Name | Password |
|---|---|---|---|---|---|
| **Production** | `new-api` | `3000` | `postgres` | `new-api` | `admin123..` |
| **Test** | `new-api-2` | `3002` | `postgres-2` | `new-api-test` | `admin123..` |
| **Legacy (do not use)** | `new-api-3` | `3003` | `postgres-3` | `new-api-3` | `admin123..` |

- **Nginx**: `/etc/nginx/sites-enabled/new-api` proxies domain traffic → `http://127.0.0.1:3000`
- **Docker images**: Production uses `new-api-custom:latest`, Test uses `new-api-test:latest`
- **Compose files**: Production = `docker-compose.yml`, Test = `docker-compose.test.yml`
- **Database backups**: `/root/new-api/backups/` (auto-created by `deploy.sh` before each deployment)

## Deploy to Test

> [!IMPORTANT]
> **deploy.sh 会在构建前执行 `git stash`，未提交的代码不会被打进镜像！**
> 部署前必须先提交代码：`git add -A && git commit -m "描述"`

// turbo-all

1. Make sure you're on the feature branch and **all changes are committed**:
```bash
cd /root/new-api && git branch --show-current && git status
```
If there are uncommitted changes, commit them first:
```bash
cd /root/new-api && git add -A && git commit -m "your message"
```

2. Run deploy:
```bash
cd /root/new-api && bash deploy.sh test
```

3. Verify:
```bash
curl -s http://127.0.0.1:3002/api/status | grep success
```

## Deploy to Production

> [!IMPORTANT]
> **deploy.sh 会在构建前执行 `git stash`，未提交的代码不会被打进镜像！**
> 部署前必须先提交代码。生产环境建议用 `deploy.sh promote` 从 dev 合并到 main。

> [!WARNING]
> `deploy.sh prod` requires interactive confirmation (type `y`).
> Use `echo y | bash deploy.sh prod` to bypass, or run it manually.

1. Make sure you're on `main` branch:
```bash
cd /root/new-api && git checkout main && git branch --show-current
```

2. Run deploy (auto-backs up database first):
```bash
cd /root/new-api && echo y | bash deploy.sh prod
```

3. Verify:
```bash
curl -s http://127.0.0.1:3000/api/status | grep success
docker logs new-api --tail 5
```

## Rollback Production

If production deployment fails:

1. Check the latest backup:
```bash
ls -lt /root/new-api/backups/ | head -5
```

2. Restore from backup:
```bash
# Stop app, keep postgres running
docker stop new-api

# Restore (replace filename with actual backup)
docker exec postgres psql -U root -d postgres -c "DROP DATABASE IF EXISTS \"new-api\";"
docker exec postgres psql -U root -d postgres -c "CREATE DATABASE \"new-api\";"
gunzip -c /root/new-api/backups/BACKUP_FILE.sql.gz | docker exec -i postgres psql -U root -d new-api

# Restart
docker start new-api
```

## Key Files

- `deploy.sh` — Build & deploy script  
- `docker-compose.yml` — Production compose (image: `new-api-custom:latest`)
- `docker-compose.test.yml` — Test compose (image: `new-api-test:latest`)
- `scripts/db_backup.sh` — Database backup script
- `Dockerfile` — Multi-stage build (frontend + backend)
