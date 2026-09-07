#!/usr/bin/env bash
# 从上游 QuantumNous/new-api 升级到新版本的辅助脚本
#
# 用法:
#   scripts/upgrade.sh                       # 升级到 origin/main 最新 release
#   scripts/upgrade.sh v0.12.16              # 升级到指定 tag
#   scripts/upgrade.sh --dry-run v0.12.16    # 只列出影响的文件，不执行 merge
#
# 前提: 当前在 release 分支 (或 release 的派生分支)

set -euo pipefail

UPSTREAM_REMOTE="origin"
DRY_RUN=false
TARGET=""

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    *) TARGET="$arg" ;;
  esac
done

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$CURRENT_BRANCH" != "release" && "$CURRENT_BRANCH" != release-* ]]; then
  echo "❌ 当前分支 $CURRENT_BRANCH 不是 release 或 release-* 分支"
  echo "   升级必须在 release 分支上进行"
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "❌ 工作区不干净，先 commit 或 stash"
  exit 1
fi

# 一次性配置 ours 合并驱动 (每个 clone 需要)
git config merge.ours.driver true

echo "=== 拉取上游更新 ==="
git fetch "$UPSTREAM_REMOTE" --prune 2>&1 | grep -vE "rejected|would clobber" || true
# tags 单独拉，忽略和其他 remote 的同名冲突
git fetch "$UPSTREAM_REMOTE" "refs/tags/v*:refs/tags/v*" 2>&1 | tail -5 || true

if [[ -z "$TARGET" ]]; then
  TARGET=$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
  echo "未指定版本，自动选最新 release tag: $TARGET"
fi

if ! git rev-parse --verify "$TARGET" >/dev/null 2>&1; then
  echo "❌ 目标 $TARGET 不存在"
  exit 1
fi

echo ""
echo "=== 升级报告: $CURRENT_BRANCH → $TARGET ==="

MERGE_BASE=$(git merge-base HEAD "$TARGET")
if [[ -z "$MERGE_BASE" ]]; then
  echo "❌ 和目标没有共同祖先，无法 3-way merge"
  echo ""
  echo "可能原因:"
  echo "  1. 上游 force-push 或 rebase，历史被重写"
  echo "  2. 你的 release 分支的 baseline 消失在上游新历史里"
  echo ""
  echo "检查 archive tag 是否还能定位原始 baseline:"
  git tag --list "archive/upstream-*" | sed 's/^/     /'
  echo ""
  echo "恢复流程见 docs/upgrade-workflow.md 「上游 rebase 时怎么办」章节"
  exit 1
fi

# 进一步检测：原始 baseline (从 archive tag) 是否仍然能从上游新 head 追溯到
ORIGINAL_BASELINE_TAG=$(git tag --list "archive/upstream-*" --sort=-v:refname | head -1)
if [[ -n "$ORIGINAL_BASELINE_TAG" ]]; then
  ORIGINAL_SHA=$(git rev-parse "$ORIGINAL_BASELINE_TAG")
  if ! git merge-base --is-ancestor "$ORIGINAL_SHA" "$TARGET" 2>/dev/null; then
    echo "⚠️  警告: 你的 archive baseline $ORIGINAL_BASELINE_TAG ($ORIGINAL_SHA)"
    echo "   已经不在上游 $TARGET 的历史链上 —— 上游可能 rebase / force-push / squash 过"
    echo "   merge 仍可继续 (会用 $MERGE_BASE 作为共同祖先)，但冲突可能更多"
    echo "   恢复方案见 docs/upgrade-workflow.md 「上游 rebase / force-push 时怎么办」"
    echo ""
    if [[ "$DRY_RUN" != "true" ]]; then
      read -p "继续? [y/N] " -n 1 -r
      echo
      [[ ! $REPLY =~ ^[Yy]$ ]] && exit 0
    fi
  fi
fi

echo "共同祖先: $MERGE_BASE"
echo ""

UPSTREAM_CHANGED=$(git diff --name-only "$MERGE_BASE" "$TARGET")
LOCAL_CHANGED=$(git diff --name-only "$MERGE_BASE" HEAD)

HOTSPOTS=$(comm -12 \
  <(echo "$UPSTREAM_CHANGED" | sort -u) \
  <(echo "$LOCAL_CHANGED" | sort -u))

HOTSPOT_COUNT=$(echo "$HOTSPOTS" | grep -c . || echo 0)
UPSTREAM_COUNT=$(echo "$UPSTREAM_CHANGED" | grep -c . || echo 0)
LOCAL_COUNT=$(echo "$LOCAL_CHANGED" | grep -c . || echo 0)

echo "上游本次改动文件数: $UPSTREAM_COUNT"
echo "你的 baseline 改动文件数: $LOCAL_COUNT"
echo "🔥 双方都改过的文件 (升级重点验证): $HOTSPOT_COUNT"
echo ""
if [[ "$HOTSPOT_COUNT" -gt 0 ]]; then
  echo "Hotspot 文件列表:"
  echo "$HOTSPOTS" | sed 's/^/   /'
  echo ""
fi

if [[ "$DRY_RUN" == "true" ]]; then
  echo "=== DRY RUN 结束，未执行 merge ==="
  exit 0
fi

echo "=== 执行 merge ==="
read -p "确认 merge $TARGET 到 $CURRENT_BRANCH? [y/N] " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  echo "取消"
  exit 0
fi

if git merge --no-commit --no-ff "$TARGET"; then
  echo ""
  echo "✅ merge 无冲突，检查改动后 git commit 即可"
  git status
else
  echo ""
  echo "⚠️  有冲突，对照上面 Hotspot 列表逐个解决:"
  git diff --name-only --diff-filter=U
  echo ""
  echo "解决后: git add <文件> && git commit"
fi
