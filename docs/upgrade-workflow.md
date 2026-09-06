# 升级工作流（实际验证版）

本文档基于实际多次成功升级的流程总结。**会话丢失也能照做**。

---

## 环境关系

| 环境 | 端口 | 镜像 | 数据库 | Redis | 网络 |
|---|---|---|---|---|---|
| 生产 `new-api` | 3000 | `new-api-custom:latest` | postgres / new-api | redis | new-api_default |
| 测试 `new-api-2` | 3002 | `new-api-test:latest` | postgres-2 / new-api-test | redis-2 | test_default |
| 独立站 `new-api-3` | 127.0.0.1:3003 | `calciumion/new-api:latest` | postgres-3 / new-api-3 | redis-3 | new-api-2_default |

代码分支：`/root/new-api`，主干 `release-slim`（基于上游 v0.13.1 + 二开）。

数据库**完全独���**，可以放心在测试环境随便折腾。

---

## 日常迭代（90% 用这个）

适用：fix bug、加小功能、调样式、port 二开。

### 1. 改代码 + commit

```bash
cd /root/new-api
git checkout release-slim
# ...编辑代码...
git add <files>
git commit -m "fix(slim): xxx"
```

### 2. 构建测试镜像（后台跑，~2 分钟）

```bash
docker build -t new-api-test . > /tmp/build.log 2>&1 &
# 等命令完成（前端 50s + Go 50s）
tail -f /tmp/build.log | grep -E "DONE [0-9]|naming to"
```

### 3. 部署到测试环境

```bash
docker rm -f new-api-2
docker compose -f docker-compose.test.yml up -d
sleep 5
curl -s -o /dev/null -w "HTTP %{http_code}\n" http://localhost:3002/api/status
# 期望: HTTP 200
```

### 4. 浏览器验证

`http://172.93.102.161:3002/`（注意：访问要是 80/443 端口走 nginx，3002 直连看你网络是否通）。

**Ctrl+Shift+R 硬刷新**（避免缓存）。

### 5. 同步生产（确认无误后）

```bash
STAMP=$(date +%Y%m%d-%H%M)

# 5.1 备份当前生产镜像（保险，秒级回滚用）
docker tag new-api-custom:latest new-api-custom:before-$STAMP

# 5.2 把测试镜像打成生产 latest
docker tag new-api-test new-api-custom:latest

# 5.3 重启生产容器
docker compose -f docker-compose.yml up -d --force-recreate

# 5.4 验证
sleep 8
curl -s -o /dev/null -w "HTTP %{http_code}\n" http://localhost:3000/api/status
docker logs --tail 20 new-api 2>&1 | grep -iE "error|panic|fatal" || echo "无错"
```

⚠️ **关键点**：测试和生产用**同一个镜像**（只是 tag 不同），所以 docker 不会重 build，秒级切换。

### 6. 出问题立即回滚

```bash
docker tag new-api-custom:before-$STAMP new-api-custom:latest
docker compose -f docker-compose.yml up -d --force-recreate
```

数据库 schema 加字段是兼容的，不需要回滚 DB。

---

## new-api-3 升级（官方 latest 镜像，不参与 release-slim 二开）

适用：把独立站 new-api-3 升级到 `calciumion/new-api:latest` 的新版本。

> 跟生产 / 测试不一样：new-api-3 **不走我们的 release-slim 分支**，直接用官方镜像。
> 所以"升级"= `docker pull` + 换容器，没有 build。
> **生产 (3000) 和测试 (3002) 完全不会受影响**，库、redis、网络都独立。

### 当前实际部署事实（与表格细节有出入，校准）

- 跑着的容器名是 `new-api-3-old`（不是表格里写的 `new-api-3`），`new-api-3` 这个名字是上次升级失败后退出留下的占位。**升级时要先 `docker rm new-api-3` 腾名字。**
- 数据挂载是 `/root/new-api-2/data` 和 `/root/new-api-2/logs`（共用 new-api-2 的目录，不是 `/root/new-api-3/`）。
- 网络 `new-api-2_default`，端口绑 `127.0.0.1:3003->3000`。
- DB 容器 `postgres-3`，库名 `new-api-3`，volume `new-api-2_pg_data_3`。

### 升级步骤

```bash
STAMP=$(date +%Y%m%d-%H%M)
BAK=/root/new-api/backups
mkdir -p $BAK

# 1. 备份三件套（前缀 new-api-3- 区分生产备份）
docker exec postgres-3 pg_dumpall -U root | gzip > $BAK/new-api-3-pgdump-$STAMP.sql.gz
docker run --rm -v new-api-2_pg_data_3:/src:ro -v $BAK:/backup alpine \
  tar czf /backup/new-api-3-pgvol-$STAMP.tgz -C /src .
docker save $(docker inspect new-api-3-old --format '{{.Config.Image}}') | gzip \
  > $BAK/new-api-3-image-$STAMP.tgz

# 2. tag 当前镜像作为回滚锚点（与生产风格一致）
OLD_IMG=$(docker inspect new-api-3-old --format '{{.Config.Image}}')
docker tag $OLD_IMG ${OLD_IMG}:before-$STAMP

# 3. 拉新镜像
docker pull calciumion/new-api:latest

# 4. 停旧容器（保留！不要 rm，作为最快回滚路径）
docker stop new-api-3-old

# 5. 删除占位的退出容器，腾出 new-api-3 名字
docker rm new-api-3 2>/dev/null || true

# 6. 用 latest 镜像起新容器（参数与 new-api-3-old 一致）
docker run -d \
  --name new-api-3 \
  --restart always \
  --network new-api-2_default \
  -p 127.0.0.1:3003:3000 \
  -v /root/new-api-2/data:/data \
  -v /root/new-api-2/logs:/app/logs \
  -e OSS_UPLOAD_SERVICE_URL=http://172.93.102.161:3001 \
  -e SQL_DSN='postgresql://root:admin123..@postgres-3:5432/new-api-3' \
  -e REDIS_CONN_STRING=redis://redis-3 \
  -e TZ=Asia/Shanghai \
  -e ERROR_LOG_ENABLED=true \
  -e BATCH_UPDATE_ENABLED=true \
  calciumion/new-api:latest \
  --log-dir /app/logs

# 7. 验证
sleep 8
curl -s -o /dev/null -w "HTTP %{http_code}\n" http://127.0.0.1:3003/api/status
docker logs --tail 30 new-api-3 2>&1 | grep -iE "started|error|panic|fatal|migrat"
```

期望日志：`database migration started` → `Successfully migrated ...` → `New API vX.Y.Z started`。

### 出问题立即回滚（秒级）

```bash
docker rm -f new-api-3
docker start new-api-3-old
```

如果连数据库 schema 都需要回滚（很少见，迁移加字段一般兼容）：

```bash
gunzip -c $BAK/new-api-3-pgdump-$STAMP.sql.gz | docker exec -i postgres-3 psql -U root
```

### 升级记录

| 日期 | 从 | 到 | 备注 |
|---|---|---|---|
| 2026-05-18 | `new-api-custom-3:latest` (旧本地构建) | `calciumion/new-api:latest` v1.0.0-rc.6 | 迁移 `tokens.model_limits → text`，无错误 |

---

## 大升级（上游新 release）

适用：上游发了 v0.13.2、v0.14.0 这种大版本。

```bash
cd /root/new-api
git checkout release-slim
git fetch origin

# 1. 看 hotspot
scripts/upgrade.sh --dry-run v0.13.2

# 2. 真升级（会问你确认）
scripts/upgrade.sh v0.13.2

# 3. 解决冲突（重点对照 CUSTOM.md 里的检查点）
# 一般冲突在: model/option.go, model/user.go, model/topup.go, controller/user.go, router/api-router.go, web/src/i18n/locales/*.json

# 4. 编译验证
go build ./...
cd web && bun install && bun run build && cd ..

# 5. 之后走"日常迭代"步骤 2-5
```

---

## 关键备份（升级到新 release 前必做）

```bash
STAMP=$(date +%Y%m%d-%H%M)
BAK=/root/new-api/backups
mkdir -p $BAK

# 1. PG 全库 dump（关键）
docker exec postgres pg_dumpall -U root | gzip > $BAK/prod-pgdump-$STAMP.sql.gz

# 2. PG 数据卷二进制（备用）
docker run --rm -v new-api_pg_data:/src:ro -v $BAK:/backup alpine \
  tar czf /backup/prod-pgvol-$STAMP.tgz -C /src .

# 3. 当前生产镜像（秒级回滚保险）
docker save new-api-custom:latest | gzip > $BAK/prod-image-$STAMP.tgz
```

恢复：
- DB: `gunzip -c xxx.sql.gz | docker exec -i postgres psql -U root`
- 镜像: `docker load -i xxx.tgz`

---

## 必备数据库选项（部署到新数据库时灌入）

```sql
INSERT INTO options ("key", "value") VALUES
  ('GeminiImageSizeRatio', '{"1K":1,"2K":1,"4K":1.5}'),
  ('KlingPointRatio', '0.78'),
  ('InviterRebatePercent', '8')
ON CONFLICT ("key") DO UPDATE SET "value" = EXCLUDED."value";

UPDATE options SET value = REPLACE(value,
  '"personal":{"enabled":true,"topup":true,"personal":true}',
  '"personal":{"enabled":true,"topup":true,"personal":true,"invitation":true}')
WHERE key = 'SidebarModulesAdmin';
```

---

## 上游 rebase / force-push 时怎么办

罕见但发生过。`scripts/upgrade.sh --dry-run` 会检测到 baseline 失效。

恢复方案见本文档老版本，关键：
- `archive/upstream-v0.12.15` tag 是当初的 baseline 锚点
- 本地 git 永远保留你已有的 commit，上游怎么改都不影响

---

## 维护

清理 docker 空间（每周或满了再跑）：
```bash
# 删除 7 天前的 build cache
docker builder prune -af --filter until=168h

# 删除 4 个以上的旧 before-* 备份镜像（保留最近 3 个）
docker images new-api-custom --format '{{.Tag}}' | grep "^before-" | tail -n +4 | xargs -I{} docker rmi new-api-custom:{}
```

加 cron 自动跑：
```bash
crontab -e
# 加: 0 3 * * 0 docker builder prune -af --filter until=168h >/dev/null 2>&1
```

---

## 新二开纪律（避免未来升级冲突）

1. **新功能优先放独立文件**：`agents/`, `service/my_xxx.go`, `relay/channel/xxx_custom.go` —— 永不冲突
2. **必须改上游文件时**，commit message 加标记：`fix(slim): ... [touches-upstream]`
3. **新加 ratio/option 配置**，记得：
   - 后端注册（`model/option.go` 两处：OptionMap + updateOptionMap）
   - 前端 UI 字段（`web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`）
   - ���据库灌入默认值
4. **每个 feature 写到 `CUSTOM.md`**：
   - 涉及哪些文件
   - INDEPENDENT 还是 MODIFIED
   - 升级时检查点（搜什么字符串）
5. **commit 信息要清楚**，未来的你会感谢自己

---

## 常用命令速查

```bash
# 当前所有 new-api 容器
docker ps --filter name=new-api

# 看实时日志
docker logs -f new-api          # 生产
docker logs -f new-api-2        # 测试

# 数据库 shell
docker exec -it postgres psql -U root -d new-api
docker exec -it postgres-2 psql -U root -d new-api-test

# 看某个 option 配置
docker exec postgres psql -U root -d new-api -c \
  "SELECT \"key\", \"value\" FROM options WHERE \"key\" = 'XXX';"

# 改某个 option
docker exec postgres psql -U root -d new-api -c \
  "INSERT INTO options (\"key\", \"value\") VALUES ('XXX','VAL') \
   ON CONFLICT (\"key\") DO UPDATE SET \"value\" = EXCLUDED.\"value\";"

# 看 release-slim 提交历史
git log --oneline release-slim ^v0.13.1

# 看二开总览
cat CUSTOM.md
```
