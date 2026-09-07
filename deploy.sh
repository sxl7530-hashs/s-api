#!/bin/bash
#
# New-API 一键部署脚本
# 
# 用法:
#   ./deploy.sh test     # 构建并部署到测试环境 (端口 3002)
#   ./deploy.sh prod     # 构建并部署到生产环境 (端口 3000)
#   ./deploy.sh promote  # 将 dev 合并到 main 并部署生产
#   ./deploy.sh rollback # 回滚生产到上一个 main 版本
#   ./deploy.sh status   # 查看所有环境状态
#

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

PROJECT_DIR="/root/new-api"
PROD_IMAGE="new-api-custom"
TEST_IMAGE="new-api-test"
PROD_COMPOSE="$PROJECT_DIR/docker-compose.yml"
TEST_COMPOSE="$PROJECT_DIR/docker-compose.test.yml"

# 打印带颜色的消息
info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
ok()    { echo -e "${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 显示当前状态
show_status() {
    echo -e "\n${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  New-API 环境状态${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════${NC}\n"
    
    # Git 状态
    cd "$PROJECT_DIR"
    local current_branch=$(git branch --show-current)
    echo -e "  ${BLUE}Git 分支:${NC} $current_branch"
    echo -e "  ${BLUE}最新提交:${NC} $(git log --oneline -1)"
    echo ""
    
    # 分支对比
    local main_commit=$(git log main --oneline -1 2>/dev/null || echo "(unknown)")
    local dev_commit=$(git log dev --oneline -1 2>/dev/null || echo "(unknown)")
    echo -e "  ${YELLOW}分支状态:${NC}"
    echo -e "    main: $main_commit"
    echo -e "    dev:  $dev_commit"
    local diff_count=$(git rev-list main..dev --count 2>/dev/null || echo "?")
    echo -e "    dev 超前 main: ${diff_count} 个提交"
    echo ""
    
    # Docker 容器状态
    echo -e "  ${YELLOW}容器状态:${NC}"
    echo "  ────────────────────────────────────────────"
    docker ps --format "  {{.Names}}\t{{.Image}}\t{{.Status}}" --filter "name=new-api" --filter "name=redis" --filter "name=postgres" 2>/dev/null | grep -E "new-api|redis|postgres" | sort
    echo ""
    
    # Docker 镜像
    echo -e "  ${YELLOW}镜像信息:${NC}"
    echo "  ────────────────────────────────────────────"
    docker images --format "  {{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}" 2>/dev/null | grep -i "new-api" | sort
    echo ""
}

# 构建镜像
build_image() {
    local image_name=$1
    local branch=$2
    
    info "正在从 ${branch} 分支构建镜像 ${image_name}..."
    cd "$PROJECT_DIR"
    
    # 保存当前分支
    local original_branch=$(git branch --show-current)
    
    # 检查是否有未提交的更改
    if ! git diff --quiet 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
        warn "检测到未提交的更改"
        info "暂存更改..."
        git stash
        local stashed=true
    fi
    
    # 切换到目标分支
    if [ "$original_branch" != "$branch" ]; then
        info "切换到 ${branch} 分支..."
        git checkout "$branch"
    fi
    
    # 构建镜像
    info "开始构建 Docker 镜像 (这可能需要几分钟)..."
    docker build --no-cache -t "$image_name" .
    
    # 清理构建缓存，只保留最近 10GB，防止无限膨胀
    info "清理构建缓存..."
    docker builder prune -f --keep-storage=10GB
    
    # 恢复原始分支
    if [ "$original_branch" != "$branch" ]; then
        info "恢复到 ${original_branch} 分支..."
        git checkout "$original_branch"
    fi
    
    # 恢复暂存的更改
    if [ "$stashed" = true ]; then
        info "恢复暂存的更改..."
        git stash pop
    fi
    
    ok "镜像 ${image_name} 构建完成"
}

# 安全检查：验证 compose 文件和镜像配置
verify_compose_config() {
    local compose_file=$1
    local expected_image=$2
    
    if [ ! -f "$compose_file" ]; then
        error "找不到 Docker Compose 文件: $compose_file"
        return 1
    fi
    
    # 验证镜像名称
    local actual_image=$(grep -E "^\s+image:" "$compose_file" | head -1 | awk '{print $2}')
    if [ "$actual_image" != "$expected_image" ]; then
        error "Compose 文件中的镜像 ($actual_image) 与预期 ($expected_image) 不匹配!"
        error "请检查 $compose_file"
        return 1
    fi
    
    # 验证不含默认密码
    if grep -q "123456" "$compose_file"; then
        error "Compose 文件中包含默认密码 '123456'，请修改为生产密码!"
        return 1
    fi
    
    ok "Compose 配置验证通过: 镜像=$actual_image"
    return 0
}

# 部署到测试环境
deploy_test() {
    echo -e "\n${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  🟡 部署到测试环境 (端口 3002)${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════${NC}\n"
    
    # 构建测试镜像 (从 dev 分支)
    build_image "$TEST_IMAGE" "dev"
    
    # 先停掉旧的测试容器 (如果存在)
    info "停止旧的测试环境..."
    docker compose -f "$TEST_COMPOSE" -p test down 2>/dev/null || true
    
    # 也停掉可能在 new-api-2 目录运行的旧容器
    if docker ps -q --filter "name=new-api-2" | grep -q .; then
        warn "发现旧的 new-api-2 容器，正在停止..."
        docker stop new-api-2 2>/dev/null || true
        docker rm new-api-2 2>/dev/null || true
    fi
    
    # 启动新的测试环境
    info "启动测试环境..."
    docker compose -f "$TEST_COMPOSE" -p test up -d
    
    # 等待健康检查
    info "等待服务启动..."
    sleep 5
    
    if docker ps --filter "name=new-api-2" --filter "status=running" | grep -q "new-api-2"; then
        ok "测试环境部署成功！"
        echo -e "  🔗 访问地址: ${GREEN}http://localhost:3002${NC}"
    else
        error "测试环境可能启动失败，请检查日志:"
        echo "  docker logs new-api-2"
    fi
    echo ""
}

# 部署到生产环境
deploy_prod() {
    echo -e "\n${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  🟢 部署到生产环境 (端口 3000)${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════${NC}\n"
    
    # 确认操作
    if [ "$1" != "--yes" ]; then
        echo -e "  ${RED}⚠️  你即将更新生产环境！${NC}"
        echo -e "  这会导致服务短暂中断 (通常 < 10秒)"
        echo ""
        read -p "  确认部署? (y/N): " confirm
        if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
            info "已取消部署"
            return 0
        fi
    fi
    
    # 安全检查
    if ! verify_compose_config "$PROD_COMPOSE" "$PROD_IMAGE"; then
        error "配置验证失败，部署已中止"
        return 1
    fi
    
    # 部署前自动备份数据库
    info "正在备份生产数据库..."
    if [ -f "$PROJECT_DIR/scripts/db_backup.sh" ]; then
        bash "$PROJECT_DIR/scripts/db_backup.sh" backup --pre-deploy
        ok "数据库备份完成"
    else
        warn "跳过数据库备份 (未找到备份脚本)"
    fi

    # 构建生产镜像 (从 dev 分支直接构建，不依赖 main 分支)
    build_image "$PROD_IMAGE" "dev"
    
    # 快速重启 —— 只重启 new-api 容器，不动数据库
    info "正在重启生产服务 (短暂停机)..."
    cd "$PROJECT_DIR"
    docker compose -f "$PROD_COMPOSE" up -d --no-deps --force-recreate new-api
    
    # 等待健康检查
    info "等待服务启动..."
    sleep 8
    
    # 检查容器状态（排除测试环境的 new-api-2）
    if docker ps --filter "name=^new-api$" --filter "status=running" | grep -q "new-api"; then
        ok "生产环境部署成功！"
        echo -e "  🔗 访问地址: ${GREEN}http://localhost:3000${NC}"
    else
        error "生产环境可能启动失败，请检查日志:"
        echo "  docker logs new-api"
        echo ""
        warn "尝试回滚? 运行: ./deploy.sh rollback"
    fi
    echo ""
}

# 将 dev 合并到 main 并部署
promote() {
    echo -e "\n${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  🚀 将 dev 分支提升到生产${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════${NC}\n"
    
    cd "$PROJECT_DIR"
    
    # 检查是否有未提交的更改
    if ! git diff --quiet 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
        error "存在未提交的更改，请先提交或暂存"
        return 1
    fi
    
    # 安全检查：验证 compose 文件
    if ! verify_compose_config "$PROD_COMPOSE" "$PROD_IMAGE"; then
        error "Compose 配置验证失败，请先修正"
        return 1
    fi
    
    # 显示将要合并的提交
    local diff_count=$(git rev-list main..dev --count 2>/dev/null || echo "0")
    info "以下 ${diff_count} 个提交将被合并到 main:"
    echo "  ────────────────────────────────────────────"
    git log main..dev --oneline 2>/dev/null | head -20 || echo "  (没有新的提交)"
    if [ "$diff_count" -gt 20 ] 2>/dev/null; then
        echo "  ... 还有 $((diff_count - 20)) 个提交未显示"
    fi
    echo ""
    
    # 超过 50 个提交差异时给出额外警告
    if [ "$diff_count" -gt 50 ] 2>/dev/null; then
        echo -e "  ${RED}⚠️  警告: dev 超前 main ${diff_count} 个提交，差异较大！${NC}"
        echo -e "  ${RED}   建议先在测试环境充分测试后再 promote${NC}"
        echo ""
    fi
    
    # 确认操作
    echo -e "  ${RED}⚠️  这将把 dev 合并到 main 并部署到生产！${NC}"
    read -p "  确认? (y/N): " confirm
    if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
        info "已取消"
        return 0
    fi
    
    # 备份当前 main 指针，方便回滚
    local main_before=$(git rev-parse main)
    info "记录 main 回滚点: ${main_before:0:8}"
    git tag -f "rollback-point" main 2>/dev/null
    
    # 合并
    info "合并 dev 到 main..."
    git checkout main
    git merge dev --no-ff -m "Merge dev to main: $(date '+%Y-%m-%d %H:%M')"
    
    ok "合并完成"
    
    # 部署到生产
    deploy_prod "--yes"

    # 将 main 回合到 dev，保证 dev 始终是 main 的超集
    info "将 main 回合到 dev..."
    git checkout dev
    git merge main -m "Merge main back to dev: $(date '+%Y-%m-%d %H:%M')"
    ok "dev 分支已同步 main 的最新代码"
    
    # 回到 dev 分支 (日常开发分支)
    git checkout dev
}

# 回滚生产到上一个版本
rollback() {
    echo -e "\n${CYAN}═══════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  ⏪ 回滚生产环境${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════${NC}\n"
    
    cd "$PROJECT_DIR"
    
    # 查找回滚点
    if git rev-parse rollback-point >/dev/null 2>&1; then
        local rollback_commit=$(git rev-parse rollback-point)
        info "找到回滚点: $(git log rollback-point --oneline -1)"
    else
        error "未找到回滚点标签 (rollback-point)"
        info "尝试使用 main 的上一个非合并提交..."
        local rollback_commit=$(git log main --oneline --first-parent -2 | tail -1 | awk '{print $1}')
        if [ -z "$rollback_commit" ]; then
            error "无法确定回滚目标"
            return 1
        fi
        info "回滚目标: $(git log $rollback_commit --oneline -1)"
    fi
    
    echo -e "  ${RED}⚠️  将 main 重置到: $(git log $rollback_commit --oneline -1)${NC}"
    read -p "  确认回滚? (y/N): " confirm
    if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
        info "已取消"
        return 0
    fi
    
    # 重置 main
    git checkout main
    git reset --hard "$rollback_commit"
    ok "main 已重置"
    
    # 重新构建并部署
    deploy_prod "--yes"
}

# 主入口
case "${1:-}" in
    test)
        deploy_test
        ;;
    prod)
        deploy_prod
        ;;
    promote)
        promote
        ;;
    rollback)
        rollback
        ;;
    status)
        show_status
        ;;
    *)
        echo ""
        echo "  用法: $0 {test|prod|promote|rollback|status}"
        echo ""
        echo "  命令:"
        echo "    test      构建 dev 分支并部署到测试环境 (端口 3002)"
        echo "    prod      构建 dev 分支并部署到生产环境 (端口 3000)"
        echo "    promote   将 dev 合并到 main 并部署生产"
        echo "    rollback  回滚生产到上一个版本"
        echo "    status    查看所有环境状态"
        echo ""
        ;;
esac
