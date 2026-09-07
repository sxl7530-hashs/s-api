# CUSTOM.md — release-slim 二开追踪

基于上游 `v0.13.1` 的 release-slim 分支记录的所有二开 feature。**升级时对照本表逐项验证不被覆盖**。

## 升级流程速记

```bash
# 1. 看 hotspot
scripts/upgrade.sh --dry-run vX.Y.Z

# 2. 执行 merge
scripts/upgrade.sh vX.Y.Z

# 3. 解决冲突: 重点看下面 [MODIFIED] 文件，对照本表确认二开逻辑还在
# 4. 验证: go build ./... && cd web && bun run build
# 5. 部署测试: docker build -t new-api-test . && docker compose -f docker-compose.test.yml up -d --force-recreate
```

文件类型：
- `[INDEPENDENT]` 新文件，上游不会冲突（除非上游恰好新增同名文件）
- `[MODIFIED]` 改了上游文件，升级可能冲突——对应"What to check"

---

## Feature 1+2: Gemini 画图失败不扣费 + URL 图片支持

**目的**：Gemini 画图模型 finishReason!=STOP 或没返回图片时，不扣费用户额度。同时支持 reverse proxy 返回 markdown URL 形式的图片。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `relay/channel/gemini/relay-gemini-custom.go` | `[INDEPENDENT]` | `hasImageURLInText()`, `hasImageInGeminiResponse()` |
| `relay/channel/gemini/relay-gemini.go` | `[MODIFIED]` | `geminiStreamHandler` 返回 `(usage, imageCount, err)`. `GeminiChatStreamHandler` 检查 `imageCount==0`. `GeminiChatHandler` 在 `responseGeminiChat2OpenAI` 之前检查 finishReason!=STOP 和 `!hasImageInGeminiResponse()`. 搜 `IsGeminiModelSupportImagine` |
| `relay/channel/gemini/relay-gemini-native.go` | `[MODIFIED]` | `GeminiTextGenerationHandler` 同样检查 finishReason 和 `hasImageInGeminiResponse`. `GeminiTextGenerationStreamHandler` 处理 3 返回值 |

## Feature 3: Gemini Image Size Pricing

**目的**：Gemini 画图按 `extra_body.google.image_config.image_size` (1K/2K/4K) 应用不同倍率。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `setting/ratio_setting/gemini_image_ratio.go` | `[INDEPENDENT]` | 全文件二开 |
| `tests/regression/gemini_image_pricing_test.go` | `[INDEPENDENT]` | 回归测试 |
| `dto/openai_request.go` | `[MODIFIED]` | `GetTokenCountMeta()` 末尾从 `ExtraBody.google.image_config` 提取 image_size，调 `GetGeminiImageSizeRatio` 写入 `ImagePriceRatio`（**OpenAI 兼容路径**） |
| `dto/gemini.go` | `[MODIFIED]` | `GeminiChatRequest.GetTokenCountMeta()` 末尾从 `GenerationConfig.ImageConfig` 解析 imageSize/image_size，写入 `ImagePriceRatio`（**原生 Gemini API ��径**，对应生产 4/14 dev e0fd9e438:dto/gemini.go:122） |
| `controller/relay.go` | `[MODIFIED]` | `fastTokenCountMetaForPricing` 在 `*GeneralOpenAIRequest` case 开头判断 `len(r.ExtraBody) > 0` 时 fallback 到 `r.GetTokenCountMeta()` |
| `model/option.go` | `[MODIFIED]` | 注册 `GeminiImageSizeRatio` 到 OptionMap + updateOptionMap |
| `relay/helper/price.go` | `[MODIFIED]` | **核心应用点** — 在 modelRatio 计算前加 `if meta.ImagePriceRatio > 0 { modelRatio = modelRatio * meta.ImagePriceRatio }`. 漏了这条 token-based pricing 路径，按次计费已 OK |
| `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx` | `[MODIFIED]` | UI 加 `GeminiImageSizeRatio` Form.TextArea |

## Feature 4: Kling Map-based Body + Omni-Video

**目的**：Kling 请求体改用 map 自动转发所有参数（避免类型不匹配 bug），新增 omni-video 模型支持。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `relay/channel/task/kling/adaptor_custom.go` | `[INDEPENDENT]` | `buildRequestMap()`, `getKlingVideoPath()`, helpers |
| `setting/ratio_setting/kling_point_ratio.go` | `[INDEPENDENT]` | `GetKlingPointRatio`, `UpdateKlingPointRatio`, `KlingPointRatio2String` |
| `relay/channel/task/kling/adaptor.go` | `[MODIFIED]` | `BuildRequestBody` 用 `buildRequestMap`. `GetModelList` 含 `kling-v3-omni`, `kling-video-o1`. `requestPayload` 有 `Sound` 字段. `ParseTaskResult` 不论 status 都解析 `FinalUnitDeduction`. `BuildRequestURL`/`FetchTask` 用 `getKlingVideoPath` |
| `middleware/kling_adapter.go` | `[MODIFIED]` | omni-video 请求路由到 `TaskActionOmniGenerate`，写 `c.Set(common.KeyBodyStorage, nil)` 清缓存 |
| `constant/task.go` | `[MODIFIED]` | 加 `TaskActionOmniGenerate = "omniGenerate"` 常量 |
| `model/option.go` | `[MODIFIED]` | 注册 `KlingPointRatio` 到 OptionMap + updateOptionMap |
| `service/task_polling.go` | `[MODIFIED]` | **核心计费应用点** — `settleTaskBillingOnComplete` ��头加 Kling 分支：调用新增的 `computeKlingActualQuota()` (~50 行)，从 task.Data 解析 `final_unit_deduction` 精确小数，按 `可灵点 × KlingPointRatio × groupRatio × QuotaPerUnit` 计算 actualQuota，传给 `RecalculateTaskQuota` 自动多退少补 |
| `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx` | `[MODIFIED]` | UI 加 `KlingPointRatio` Form.Input |

## Feature 5: 邀请管理（Invitation）

**目的**：用户中心新增"邀请管理"页（独立于钱包），看邀请列表和分佣划转记录。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/src/pages/Invitation.jsx` | `[INDEPENDENT]` | 邀请页入口 |
| `web/src/components/invitation/InvitationPage.jsx` | `[INDEPENDENT]` | 邀请页主体 |
| `web/src/components/topup/InvitationCard.jsx` | `[INDEPENDENT]` | 邀请卡片 |
| `web/src/components/topup/modals/InvitedUsersModal.jsx` | `[INDEPENDENT]` | 邀请人列表弹窗 |
| `model/aff_transfer_log.go` | `[INDEPENDENT]` | `AffTransferLog`, `CreateAffTransferLog`, `GetAffTransferLogs` |
| `model/user.go` | `[MODIFIED]` | User 结构加 `CreatedAt`, `RebatePercent` 字段. 默认 sidebar 含 `personal.invitation:true`. 末尾追加 `InvitedUserInfo` 类型 + `GetInvitedUsers()` 函数 |
| `controller/user.go` | `[MODIFIED]` | 加 `GetAffTransferLogs` + `GetInvitedUsers` handler（在 `TransferAffQuota` 后） |
| `router/api-router.go` | `[MODIFIED]` | selfRoute 注册 `/self/aff_transfer_logs` 和 `/self/invited_users`（注意是 `/self/` 前缀） |
| `web/src/App.jsx` | `[MODIFIED]` | 懒加载 Invitation + 路由 `/console/invitation` |
| `web/src/components/layout/SiderBar.jsx` | `[MODIFIED]` | financeItems 加邀请管理入口 + routerMap 加 invitation |

## Feature 6: 邀请人充值返利（Auto Rebate）

**目的**：被邀请人充值时按比例自动返佣给邀请人。**生产关键功能**：生产 `InviterRebatePercent=8`，多个邀请人累计返利千万级。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `common/constants.go` | `[MODIFIED]` | 加 `var InviterRebatePercent = 0.0` 全局变量 |
| `model/user.go` | `[MODIFIED]` | User 结构 `RebatePercent float64 gorm:"default:-1"` (Feature 5 同位置) |
| `model/option.go` | `[MODIFIED]` | 注册 `InviterRebatePercent` 到 OptionMap + updateOptionMap |
| `model/topup.go` | `[MODIFIED]` | 末尾加 `ProcessInviterRebate(userId, topUpQuota int)` 函数. **5 处充值成功点**异步调用：Stripe `Recharge` (line ~155), `ManualCompleteTopUp` (~387), `RechargeCreem` (~462), `RechargeWaffo` (~527), `RechargeWaffoPancake` (~590) |
| `controller/topup.go` | `[MODIFIED]` | 易支付 epay 回调成功后异步调用 (line ~398, 在 `RecordTopupLog` 后) |

返利逻辑：邀请人个人 `RebatePercent>=0` 时优先用个人值，否则用全局 `InviterRebatePercent`。返利累加到邀请人 `aff_quota` (待划转) + `aff_history` (累计)。

## Feature 7: 暗色模式适配

**目的**：上游用 Semi UI token 主题，二开用 Tailwind `dark:` class 模式。补全暗色样式 + 公告 HTML 内联颜色/背景翻转。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/tailwind.config.js` | `[MODIFIED]` | 启用 `darkMode: 'class'`，colors 移到 `theme.extend` 保留 Tailwind 默认色板 |
| `web/src/index.css` | `[MODIFIED]` | 末尾追加 `.notice-themed-content` 主题色规则 + `body[theme-mode='dark']` 内联背景翻转规则 |
| `web/src/components/layout/NoticeModal.jsx` | `[MODIFIED]` | 三处 `dangerouslySetInnerHTML` 容器加 `notice-themed-content` className |

## Feature 8: 用户注册时间显示具体日期

**目的**：用户管理列表的"注册时间"列显示完整日期 (`toLocaleString`)，而非"x天前"相对时间。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/src/components/table/users/UsersColumnDefs.jsx` | `[MODIFIED]` | 加 `renderCreatedAt` 函数 (主显示 `fullDate`，tooltip 显示 `relativeTime`)，在用户列表 columns 中加"注册时间"列 |

## Feature 9: 钱包管理 Tab 顺序（额度充值优先）

**目的**：钱包页"额度充值"Tab 显示在"订阅套餐"前面 + 默认选中。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/src/components/topup/RechargeCard.jsx` | `[MODIFIED]` | `setActiveTab('topup')` 默认选额度充值。`<Tabs>` 内 TabPane 顺序：先 topup (Wallet 图标) 后 subscription (Sparkles 图标) |

## Feature 10: 登录/注册/重置密码页暗色背景

**目的**：4 个 auth 页面在暗色模式下中间一大片白色，需要主题感知背景。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/src/components/auth/LoginForm.jsx` | `[MODIFIED]` | 移除 `bg-gray-100`，改 inline `style={{backgroundColor: 'var(--semi-color-bg-0)'}}`. Title 移除 `!text-gray-800` |
| `web/src/components/auth/RegisterForm.jsx` | `[MODIFIED]` | 同上 |
| `web/src/components/auth/PasswordResetConfirm.jsx` | `[MODIFIED]` | 同上 |
| `web/src/components/auth/PasswordResetForm.jsx` | `[MODIFIED]` | 同上 |

## Feature 11: 渠道分组改关键字模糊搜索

**目的**：渠道管理筛选区"选择分组"下拉改为关键字输入框，后���用 LIKE 模糊匹配。

| 文件 | 类型 | 检��点 |
|---|---|---|
| `web/src/components/table/channels/ChannelsFilters.jsx` | `[MODIFIED]` | `searchGroup` 字段从 `Form.Select` (optionList) 改为 `Form.Input` (`placeholder='分组关键字'`)，无 `onChange` 自动触发，输入完按 Enter 或点搜索按钮 |
| `model/channel.go` | `[MODIFIED]` | `SearchChannels` 和 `SearchTags` 把 `group` 精确匹配 `('%,xxx,%')` 改为 LIKE 模糊匹配 `(' LIKE %xxx%')`，同时整体 keyword 也匹配 group 字段 |

---

## Feature 12: 渠道/标签编辑弹窗分组选择支持模糊搜索

**目的**：新建/编辑渠道时，"分组" multi-select 支持输入关键字过滤候选项（之前只能下拉滚动找）。

| 文件 | 类型 | 检查点 |
|---|---|---|
| `web/src/components/table/channels/modals/EditChannelModal.jsx` | `[MODIFIED]` | `field='groups'` 的 `Form.Select` 加 `filter={selectFilter}` + `autoClearSearchValue={false}` + `searchPosition='dropdown'` |
| `web/src/components/table/channels/modals/EditTagModal.jsx` | `[MODIFIED]` | 同上 |

依赖：`web/src/helpers/utils.jsx` 里的 `selectFilter` 函数（上游已有）。

---

## 配套基础设施（一次性，已就位）

| 文件 | 用途 |
|---|---|
| `.dockerignore` | 排除 `/logs` `/backups` `/data*` 等大目录，build context 从 35G 降到 KB 级 |
| `.gitattributes` | 二开文件 `merge=ours` 策略，防上游覆盖 |
| `scripts/upgrade.sh` | 升级辅助脚本，含 `--dry-run` 和 baseline 失效检测 |
| `docs/upgrade-workflow.md` | 完整升级流程文档 |
| `archive/upstream-v0.12.15` tag | 固定原 baseline SHA，上游 rebase 时仍能定位 |

---

## 关键数据库选项（生产值，部署时同步）

```sql
-- Feature 3
INSERT INTO options (key, value) VALUES ('GeminiImageSizeRatio', '{"1K":1,"2K":1,"4K":1.5}')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Feature 4
INSERT INTO options (key, value) VALUES ('KlingPointRatio', '0.78')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Feature 6
INSERT INTO options (key, value) VALUES ('InviterRebatePercent', '8')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Feature 5：默认 sidebar 加 invitation:true（兼容老用户）
UPDATE options SET value = REPLACE(value,
  '"personal":{"enabled":true,"topup":true,"personal":true}',
  '"personal":{"enabled":true,"topup":true,"personal":true,"invitation":true}')
WHERE key = 'SidebarModulesAdmin';
```

---

## 升级时常见冲突文件 (按概率)

| 冲突文件 | 原因 | 解决参考 |
|---|---|---|
| `model/option.go` | 上游加新选项时常改这里 | 把上游新选项保留 + 我们的 `GeminiImageSizeRatio`/`KlingPointRatio`/`InviterRebatePercent` 全部保留 |
| `model/user.go` | 上游可能改 User 结构或 sidebar 配置 | 保 `CreatedAt`/`RebatePercent` 字段、`personal.invitation:true`、文件末尾的 `InvitedUserInfo` + `GetInvitedUsers` |
| `model/topup.go` | 上游重构支付时常改 | 保 `ProcessInviterRebate` 函数 + 5 处 `go ProcessInviterRebate(...)` 调用 |
| `controller/user.go` | 上游加用户操作时 | 保 `GetAffTransferLogs` + `GetInvitedUsers` handler |
| `router/api-router.go` | 上游加路由时 | 保 `/self/aff_transfer_logs`、`/self/invited_users`、邀请相关路由 |
| `web/src/i18n/locales/zh-CN.json` | 上游加字符串 | 一般直接 union 两边的 keys，不冲突 |
| `web/tailwind.config.js` | 上游可能改 theme | 保 `darkMode: 'class'` 和 colors 在 extend 内 |
| `web/src/index.css` | 任何 CSS 改动 | 保末尾 `.notice-themed-content` 块 |

## 最小验证 checklist (升级后必跑)

- [ ] `go build ./...` 通过
- [ ] `cd web && bun run build` 通过
- [ ] 测试环境健康 `curl http://localhost:3002/api/status` 返回 200
- [ ] 邀请管理页能打开（`/console/invitation`）
- [ ] 钱包管理"额度充值"Tab 在前并默认选中
- [ ] 系统设置 → 模型定价设置 → 拉到底，能看到"Gemini 图片尺寸倍率"和"可灵点倍率"
- [ ] 暗色模式：公告弹窗 HTML 文字可读
- [ ] 用户管理列表"注册时间"是具体日期而非"x天前"
