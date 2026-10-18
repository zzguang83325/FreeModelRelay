# FreeModelRelay 技术文档

> 面向后续维护者的技术参考。使用说明见 README.md，本文只讲"代码怎么组织的、改哪里"。

## 1. 项目定位

Go 编写的本地 API 网关，把五个上游封装成三种标准协议对外提供：

```
客户端 (Trae/Claude Code/Cursor/Cherry Studio/任意 OpenAI 兼容客户端)
   │  OpenAI /v1/chat/completions
   │  Anthropic /v1/messages
   │  OpenAI Responses /v1/responses
   ▼
FreeModelRelay (默认 127.0.0.1:9999)
   │  按 model 自动路由 (resolveRouteModel)
   ├──► Cline 账号池           (多账号轮询, api.cline.bot)
   ├──► opencode zen 免费模型  (opencode.ai/zen, 免登录匿名)
   ├──► WorkBuddy 中国版       (copilot.tencent.com, 桌面端凭据自动导入)
   ├──► WorkBuddy AI 国际版    (www.workbuddy.ai, 同上)
   └──► Qoder 中国区           (gateway.qoder.com.cn, PAT 手动添加)
```

模块名 `FreeModelRelay`。除标准库外只有两个直接依赖（均在 [proxy_pool.go](internal/app/proxy_pool.go#L20-L22) 引用）：`github.com/refraction-networking/utls`（zen 出口代理池的 TLS 指纹载体，`utls.UClient` + `HelloChrome_133`）与 `golang.org/x/net`（`http2` 传输 + `proxy` 的 socks5 拨号）；`brotli` / `klauspost/compress` / `x/crypto` / `x/sys` / `x/text` 都是这两者的间接依赖。

## 2. 构建与运行

```powershell
go build -o FreeModelRelay.exe .
.\FreeModelRelay.exe           # 启动服务 (必须在项目根目录运行, 见 §16)
.\FreeModelRelay.exe -start    # 编译+启动+自动打开管理后台
.\build.ps1                         # 等价于 go build
.\build.ps1 -Test                   # 先 go test 再构建
.\build.ps1 -All                    # 交叉编译 6 平台 → dist/ (与 CI 同一矩阵)
```

**项目约定：所有执行一律先编译成 exe 再运行，且在项目根目录启动**（数据文件按相对路径 `data/` 落盘）。

CLI flags 定义在 [main.go](main.go#L14-L18)，只剩三个：`-host`(**默认 `127.0.0.1`**) `-port`(默认 **9999**) `-start`。

> **已移除 `-login` / `-add-account` / `-list`。** 这三个是 Cline 专用流程，而项目现在覆盖五家上游，各家导入方式不同（Cline 走 OAuth/令牌/批量 JSON，WorkBuddy 走本机自动解密导入，Qoder 走粘贴 PAT），CLI 无法统一；后台还提供测试 / 重置 / 删除，CLI 也做不到。账号管理统一收敛到 `/admin/`，OAuth 设备授权流程由 [admin.go](internal/app/admin.go) 的 `handleOAuthStart` / `handleOAuthPoll` 承担，`pool.AddAccountFromDeviceAuth` 已随之删除。

> [proxy.go](internal/app/proxy.go#L24) 的 `proxyListenAddress` 是展示兜底常量（初值 `127.0.0.1:9999`），[proxy.go](internal/app/proxy.go#L306) 启动时会用实际监听地址覆盖它；实际监听地址以 flag 为准。

## 3. 目录结构

```
main.go                     CLI 入口
build.ps1                   本地构建脚本 (含 6 平台矩阵, 与 CI 同步)
internal/
  app/                      核心业务
    proxy.go                路由注册/鉴权/模型路由/SSE 转发/cline 上游调用 (最大文件)
    responses.go            OpenAI Responses API ↔ chat/completions 双向转换
    pool.go                 Cline 账号池: 选号/冷却/token 刷新/持久化
    models.go               模型缓存/官方 feed 同步/默认模型解析/路由判定
    zen.go                  opencode zen 上游: 身份伪造/免费指纹/限流退避/failover
    catalog.go              opencode 官方模型目录 (models.opencode.ai, 权威免费集合)
    compact.go              上下文超限自动压缩 (移植 opencode SessionCompaction)
    rtk.go                  tool_result 输出预压缩 (超长输出的确定性瘦身)
    errors.go               上游错误透传 (402/429/400 语义保留)
    logs.go                 请求日志中间件 + 诊断文件日志
    proxy_pool.go           zen 出口代理池 + uTLS Chrome 指纹
    stats.go                zen 请求统计 (zen-stats.jsonl)
    workbuddy.go            WorkBuddy 双变体定义 + 凭据解密 + 本机账号导入
    workbuddy_client.go     WorkBuddy 模型同步 / 聊天转发 / 配置
    workbuddy_probe.go      WorkBuddy 模型可用性探测 (带 TTL 缓存)
    qoder.go                Qoder 签名 / 模型同步 / 聊天转发 / 账号管理
    provider_chat.go        第三方 provider 路由 / 展示 id 装饰 / 三协议适配
    provider_sse.go         provider 侧 SSE 中继 / 聚合 / 内层错误信封解包
    provider_credits.go     各来源积分查询与签到
    admin.go                管理后台 REST API 注册 + localhostGuard
    admin_providers.go      第三方 provider 后台路由 + 启动初始化
    admin_html.go           管理后台前端 (单个巨型 HTML 字符串常量)
    admin_zen.go            zen 配置接口实现 (代理池校验等)
    modality.go             模型模态三态 (支持/不支持/未知) + 图片实调探测与缓存
    types.go                Account / AccountPool 数据结构
  cline/auth.go             WorkOS 设备授权 + token 刷新 (api.cline.bot)
  kit/                      工具: data.go 路径 / ident.go ID 伪造 / http.go HTTP 客户端
data/                       运行时数据 (gitignore, 相对路径)
```

## 4. 请求生命周期

以 `POST /v1/chat/completions` 为例：

1. **路径归一化**（[logs.go](internal/app/logs.go#L170-L178)）：`/openai/v1/*`、`/anthropic/v1/*`、`/codex/v1/*` → 重写为 `/v1/*`（兼容替换 free-gate 项目）
2. **诊断日志**（[logs.go](internal/app/logs.go#L80) `logDiagREQ`）：仅 LLM 路径（`isLLMPath`）写 `data/all-requests.log`；后台/管理请求不记任何日志
3. **请求日志**（[logs.go](internal/app/logs.go#L170) `requestLogMiddleware`）：仅 LLM 路径分配 seq（**进入时**分配，保证顺序）、捕获 reqBody/respBody（上限 128KB）存内存（100 条，不落盘）
4. **CORS + 鉴权**（[proxy.go](internal/app/proxy.go#L110) `apiKeyHandler`）：池内无 Keys 则放行；有则校验 `x-api-key` 或 `Authorization: Bearer`
5. **模型路由**（§5）
6. **上游调用**（[proxy.go](internal/app/proxy.go#L224) `resolveRouteModel` 后按 route 分派）：
   - `cline`：[proxy.go](internal/app/proxy.go#L532) `callClineAPI` → `pickAccount` → `ensureAccountToken`（401 自动刷新重试一次）→ `buildUpstreamBody`（sessionID=`sess_<unixmilli>`，默认 max_tokens 128000/effort high）→ POST `api.cline.bot/api/v1/chat/completions`
   - `zen`：[zen.go](internal/app/zen.go#L589) `callZenAPI` → 免费指纹注入 → **恒以流式请求上游**，非流式由 `collectStreamResponse` 聚合
   - `workbuddy` / `workbuddy-ai`：[provider_chat.go](internal/app/provider_chat.go#L239) `handleWBAChat`
   - `qoder`：[provider_chat.go](internal/app/provider_chat.go#L274) `handleQoderChat`
7. **响应回写**：SSE 透传+归一化（`handleStreamResponseWithUsage`）或聚合为完整 JSON；provider 侧经 [provider_sse.go](internal/app/provider_sse.go#L37) `relayProviderSSE` / [provider_sse.go](internal/app/provider_sse.go#L98) `aggregateProviderSSE`；usage 回调记账

## 5. 模型路由与默认模型（改路由先看这节）

三个协议入口共享同一套路由，核心函数：

| 函数 | 位置 | 行为 |
|------|------|------|
| `resolveRouteModel(raw)` | [models.go](internal/app/models.go#L423) | 入口。先判 provider，再剥展示装饰归一成真实 id 后判 cline/zen；空/未知 → 回退默认模型。返回 `(model, route)`，route ∈ `zen`/`reject`/`cline`/`workbuddy`/`workbuddy-ai`/`qoder` |
| `routeModelAny(id)` | [models.go](internal/app/models.go#L412) | `routeModel` 的补全版：先 `providerRoute` 判第三方 provider，再落到 cline/zen 判定。**空 model 兜底必须走它** |
| `routeModel(id)` | [zen.go](internal/app/zen.go#L132) | 只认识 zen/cline/reject：zen 免费→zen；zen 付费→reject；`opencode/` 前缀剥除；其余→cline |
| `isKnownModel(id)` | [models.go](internal/app/models.go#L392) | 模型是否可识别：cline 模型表 ∪ zen 免费 ∪ 第三方 provider |
| `providerRoute(id)` | [provider_chat.go](internal/app/provider_chat.go#L475) | 第三方 provider 判定（§5.2） |
| `isProviderRoute(route)` | [provider_chat.go](internal/app/provider_chat.go#L525) | 路由值是否属于第三方 provider，三协议入口共用 |
| `normalizeRequestModel(id)` | [models.go](internal/app/models.go#L369) | 不在 modelsCache → 回退默认 |
| `getDefaultModel()` | [models.go](internal/app/models.go#L331) | 默认模型：先归一展示 id，cline 表内有则用、zen 免费可用、第三方 provider 可用，否则取 ID 最小的 active 模型 |

**解析顺序（§5.2 详述）**：显式前缀 → 完整装饰串精确匹配 → 裸名兜底 → cline/zen 判定 → 空/未知回退默认。

**Failover（zen→cline）**：[zen.go](internal/app/zen.go#L264) 的 `markZenSuccess`/`markZenFail`/`zenFailedNow`。zen 连续失败 ≥ `FailoverCount`(默认 3) 进入 `FailoverMinutes`(默认 5) 降级窗口，期间 zen 请求临时走 cline 池。配置存 `.zen-config.json`。

**默认模型持久化**：池文件 `defaultModel` 字段，管理后台"模型列表"页设置（`setDefaultModel`，[pool.go](internal/app/pool.go#L66)，支持任意来源）。

### 5.1 模型 id 展示层（装饰 / 分组 / 别名过滤）

对外 id 是**展示层构造**，路由 id 不直接暴露给用户（[provider_chat.go](internal/app/provider_chat.go#L386) `providerDecorateID`）：

```
<分组>/<上游可读显示名> · x<倍率>   付费（multiplier > 0）
<分组>/<上游可读显示名> · free      免费（multiplier == 0 且 free）
<分组>/<上游可读显示名>             上游未标价（既非免费也无已知倍率，不加段）
```

| 常量 / 函数 | 位置 | 说明 |
|------|------|------|
| `providerDecorateSep = " · "` | [provider_chat.go](internal/app/provider_chat.go#L323) | 装饰分隔符；解析时按第一个分隔符截断 |
| `providerFreeTag = "free"` | [provider_chat.go](internal/app/provider_chat.go#L326) | 免费计费段 |
| `providerIsAliasModel(id)` | [provider_chat.go](internal/app/provider_chat.go#L355) | 排除 `auto` / `default` / `default-model`（大小写与空白不敏感）；同步时过滤，被调用时 400 |
| `providerFormatMultiplier(m)` | [provider_chat.go](internal/app/provider_chat.go#L365) | `0.29 → x0.29`、`2 → x2` |
| `providerDecorateID(group,id,displayName,mul,free)` | [provider_chat.go](internal/app/provider_chat.go#L386) | 显示名空格转 `-`；显示名为空（部分 WB 模型上游没给 name）时退回路由 id 作首段 |
| `providerStripDecorate(id)` | [provider_chat.go](internal/app/provider_chat.go#L407) | 按第一个 ` · ` 截断，还原路由 id |
| `providerStripGroup(id)` | [provider_chat.go](internal/app/provider_chat.go#L339) | 只剥 `opencode-zen/` 前缀（cline 的 `cline-free/` 是真实 id 前缀，不剥） |
| `providerNormalizeDisplayID(id)` | [provider_chat.go](internal/app/provider_chat.go#L348) | `providerStripGroup(providerStripDecorate(id))`，展示 id → 路由 id |
| `providerFreeID(id)` | [provider_chat.go](internal/app/provider_chat.go#L332) | 给 cline/zen 整池免费的 id 补 ` · free` 段，使每个 id 末尾要么是 `x<倍率>` 要么是 `free` |
| `providerPublicIDs()` | [provider_chat.go](internal/app/provider_chat.go#L553) | 计算「裸 id 是否必须带 `<provider>/` 前缀」 |

**为什么显示名必须进 id**：用户看到 `dfmodel` 不知道是什么模型。显示名来自上游目录（Qoder 的 `display_name`、WorkBuddy 目录的 name），跨来源同名时靠倍率段区分（WB CN `GLM-5.3 · x0.79` vs Qoder `GLM-5.3 · x0.8`）。

**owned_by 分组**：`/v1/models` 各来源的 `owned_by` 分别是 `cline-free` / `opencode-zen` / `workbuddy` / `workbuddy-ai` / `qoder`，客户端据此分组。WorkBuddy 两个变体的 `source` 都是 `workbuddy`，靠 `owned_by` 区分。

### 5.2 第三方 provider 路由判定（providerRoute）

按四级顺序（[provider_chat.go](internal/app/provider_chat.go#L475)）：

1. **显式前缀**：`workbuddy/`、`wb/` → CN；`workbuddy-ai/`、`wb-ai/`、`workbuddyai/` → INTL；`qoder/`、`qd/` → Qoder（`opencode/` 前缀排除在外，它属于 zen 命名空间）
2. **完整装饰串精确匹配**：`wbResolveDecorated(id)`（[workbuddy_client.go](internal/app/workbuddy_client.go#L658)）/ `qoderResolveDecorated(id)`（[qoder.go](internal/app/qoder.go#L666)）。**必须在剥装饰之前** —— 显示名跨 provider 冲突时倍率段是唯一区分，剥掉装饰再裸名解析会丢失归属
3. **裸名兜底**：剥装饰后 `wbResolveModel(stripped)`（[workbuddy_client.go](internal/app/workbuddy_client.go#L595)）/ `qoderResolveModel(stripped)`，内部路由 id 或显示名都可命中（显示名走 `wbResolveByNameIn`，[workbuddy_client.go](internal/app/workbuddy_client.go#L626)）
4. 返回 `""` → 交给 cline/zen 判定

`providerStripPrefix(id)`（[provider_chat.go](internal/app/provider_chat.go#L511)）得到发给上游的模型名。

**踩过的坑**：`wbPrepareChat` 曾经把 `m.ID` 覆盖成客户端传的显示名，导致上游回 `11102 service info not found` —— 显示名兜底命中时 `m.ID` 才是内部路由 id，**不能覆盖**。

### 5.3 空 model 兜底的真实 bug（已修）

把默认模型设成 provider 模型（如 `workbuddy-ai/GLM-5.3 · x0.79`）后，**省略 `model` 字段**的请求全部 404。根因：`resolveRouteModel` 在 `raw == ""` 时调 `routeModel(eff)`，而 `routeModel` 只返回 zen/cline/reject，把 provider 默认模型判成 `cline`，请求带着这个模型名进了 cline 账号池 → 404。修法是新增 `routeModelAny` 并让两处默认兜底（[models.go](internal/app/models.go#L427)、[models.go](internal/app/models.go#L443)）改用它。回归测试见 `TestRouteModelAny`（[provider_proto_test.go](internal/app/provider_proto_test.go)）。

## 6. 账号池（pool.go / types.go）

- **存储**：`data/.cline-accounts.json`（0600），顶层键 `accounts/currentIdx/defaultModel/keys`。AccessToken 不落盘（`json:"-"`）。
- **选号** `pickAccount`（[pool.go](internal/app/pool.go#L155)）：策略由管理后台配置——`round_robin`（默认，CurrentIdx 循环）/ `fill`（打满第一个）/ `random`。
- **冷却** `markAccountCooldown`（[pool.go](internal/app/pool.go#L266)）：429 时解析上游文案（`parseInferenceCapDuration` 提取 "Try again in 17h 59m"，[proxy.go](internal/app/proxy.go#L2056)），无时长默认 **18 小时**（对应 Cline 每日额度重置）；到期选号时自动解除。
- **Token 刷新**（[pool.go](internal/app/pool.go#L133) `refreshAccountToken`）：POST `api.cline.bot/api/v1/auth/refresh`；成功后 AccessToken 前缀 `workos:`，ExpiresAt 提前 60 秒；失败置 expired。
- **添加账号**：仅管理后台——OAuth 设备授权（`handleOAuthStart` / `handleOAuthPoll`）、手动粘贴 refreshToken、批量导入 JSON 三种方式；无 CLI 入口。

## 7. opencode zen 免费通道（zen.go / ident.go / proxy_pool.go）

上游风控为**多要素组合**且随服务端灰度漂移（2026-10-15 一天内先后出现过 system prompt 白名单、工具实体感两套判据），以下要素经 50+ 次裸放消歧实验确认为当前生效规则，403 FreeTierError 的全部对策：

| 要素 | 实现 |
|------|------|
| ID：官方 id.ts **descending 算法**（毫秒*0x1000+同毫秒计数 → 补码低 48 位 6 字节 hex12 + 14 位 base62，时间戳可被服务端解码校验；sha/随机 hex 形实测 403） | [ident.go](internal/kit/ident.go#L49) `ZenOfficialID`（向量测试 [ident_test.go](internal/kit/ident_test.go) 锚定官方实现） |
| UA：SDK 拼接格式 `opencode/1.18.31 ai-sdk/provider-utils/4.0.23 runtime/bun/1.4.2` | [ident.go](internal/kit/ident.go#L88) `ZenUserAgent()` |
| 请求头 5 个：`x-opencode-session`/`-session-id`（同值）/`-request`(msg_)`/`-project`(global)`/`-client: cli`；同一对话复用同一 session（种子→缓存） | [zen.go](internal/app/zen.go#L589) `callZenAPI` |
| 请求体 `stream:true` 强制（false 恒 403），tools 须含**描述/参数像真实客户端的 bash+read**（占位/空 schema 必 403；客户端同名工具保留） | [zen.go](internal/app/zen.go#L432) `applyFreeTierFingerprint`，真定义嵌入 `zen_free_tools.json`（抓包自官方 CLI 1.19.0） |
| `max_tokens` 缺省 32000（不超模型 Output 限额）+ `stream_options.include_usage`（聚合器可容忍 usage-only chunk） | [zen.go](internal/app/zen.go#L521) `buildZenBody` |
| TLS 指纹/头序/HTTP 版本：**实测无关**（node TLS 透传真实 body 也 403、Go TLS 对齐后 200） | [proxy_pool.go](internal/app/proxy_pool.go) uTLS 传输仅作出口代理池的可选载体 |
| 出口代理池（可选）：多 IP 轮换 + 429 冷却 | [proxy_pool.go](internal/app/proxy_pool.go#L92) `pickZenProxy` |
| 限流退避：429/503 直接命中，502/403 按关键词 | [zen.go](internal/app/zen.go#L305) `isRateLimited`，指数退避+抖动 |
| 并发限制：信号量默认 8 | `zenSem` |

鉴权 key 默认 `public`（匿名额度）。上游收紧时免费通道会整体失效（历史已发生），failover 兜底到账号池。

**模型目录**：`data/catalog.free.json` 来自 `https://models.opencode.ai/api.json`（[catalog.go](internal/app/catalog.go)），判定规则与 opencode CLI 一致（`provider=opencode` 下 `cost.input == 0` 且 status 不是 deprecated/alpha）；目录不可用时回退 seed 白名单（[zen.go](internal/app/zen.go#L37) `zenSeedModels`）与 `-free` 后缀启发式。每 10 分钟同步一次（[zen.go](internal/app/zen.go#L812)）。

**403 回潮排查路径**：先用真实 opencode CLI 抓包（项目级 `opencode.json` 覆写 `provider.opencode.options.baseURL` 指向本地明文转发器；环境变量代理对该 CLI 无效）确认当时规则形态，再改指纹——勿凭旧结论盲改。

### 7.1 上下文压缩与 RTK 预压缩

**触发**：`maybeCompact`（[compact.go](internal/app/compact.go#L331)）——估算 token（字节数/4）> `ctx - max(output, 20000 buffer)` 时执行：序列化消息 → `selectRecent` 保留尾部 `KeepTokens`(默认 8000) → `generateSummary`（走 zen 自身，摘要 4096 tokens）→ 重组消息。失败则 `fallbackTruncate` 截到 60% 预算。会话状态 30 分钟清理一次。

**RTK 预压缩**（[rtk.go](internal/app/rtk.go#L193) `applyRTK`）：发往 zen 前对超过阈值的 `tool_result` 按输出类型做确定性过滤/截断并追加显式省略标记。与 compact 互补——RTK 是单条超长输出提前瘦身（预防），compact 是总长超限后摘要重组（补救）。**level 0 默认关闭**；只处理无歧义安全类型（文件列表/目录树/git status），`git diff` 仅 level≥2；同一输入恒定输出（利于上游 prompt cache）。

## 8. WorkBuddy / WorkBuddy AI（workbuddy*.go）

**双变体**（[workbuddy.go](internal/app/workbuddy.go#L68) `wbVariants`）：

| | CN | INTL |
|------|------|------|
| ID / owned_by | `workbuddy` | `workbuddy-ai` |
| DisplayName | WorkBuddy | WorkBuddy AI |
| Region | `cn` | `global` |
| ChatBase | `https://copilot.tencent.com` | `https://www.workbuddy.ai` |
| Origin | `https://www.codebuddy.cn` | `https://www.workbuddy.ai` |
| Platform | WorkBuddy | WorkBuddy AI |
| Accept-Language | `zh-CN` | `en-US` |
| 登录文件 | `workbuddy-desktop.info` | `workbuddy-desktop-ai.info` |
| 环境变量覆盖 Electron | `WORKBUDDY_ELECTRON_BIN` | `WORKBUDDY_AI_ELECTRON_BIN` |

两变体都有 Electron 可执行名候选列表（[workbuddy.go](internal/app/workbuddy.go#L47-L48) `ElectronExe` 优先匹配 + `ElectronExes` 候选）：CN 为 `WorkBuddy.exe` / `WorkBuddyAI.exe` / `workbuddyai.exe`（优先 `WorkBuddyAI.exe`），INTL 为同一组反向排序（优先 `workbuddyai.exe`）；`ElectronEnv` 环境变量可显式覆盖。曾因写死 exe 名导致发现不到客户端，现另有注册表 `WorkBuddy` 关键词兜底。

**凭据导入三步**（[workbuddy.go](internal/app/workbuddy.go#L25-L34) 注释）：
1. `ELECTRON_RUN_AS_NODE=1` 跑官方 Electron，执行 [workbuddy.go](internal/app/workbuddy.go#L88) 的 helper 表达式 `process.stdout.write(String(process._linkedBinding("electron_browser_workbuddy_storage").loggerGet()))`，取回 `{version, atRestSecretKey}`
2. `protectorKey = sha256(atRestSecretKey utf8)`
3. 用该密钥 AES-256-GCM 解 `.info` 文件信封（AAD 布局 `WB-AAD\0`，keyId `9127dea1b44020a7`）

auth 目录候选见 [workbuddy.go](internal/app/workbuddy.go#L207) `wbAuthDirs()`：`%LOCALAPPDATA%\CodeBuddyExtension\Data\Public\auth` 等。**客户端启动才创建且会轮转**，`Test-Path` 可能瞬时 false。

**客户端版本常量**（[workbuddy.go](internal/app/workbuddy.go#L81-L84)）：`wbClientVersion = "5.8.4"`、`wbCliVersion = "2.137.1"`。

**账号导入**：[workbuddy.go](internal/app/workbuddy.go#L346) `wbReadLocalAccounts()` —— 逐个变体找登录文件 → 取 atRestSecretKey → 解密。启动链在 [proxy.go](internal/app/proxy.go#L77) 调 `startProviderBoot()`，无 WB 账号时自动导入；3 秒后异步 `wbSyncModels()`。

**token 刷新**：`POST {ChatBase}/v2/plugin/auth/token/refresh`。上游回信封 `{"code":0,"msg":"OK","data":{accessToken,...}}`，`wbRefreshAccount` 两形态兼容（早期只读顶层 token 导致刷新静默失败）。

**模型同步与可用性**：`wbSyncModels`（[workbuddy_client.go](internal/app/workbuddy_client.go#L358)）同步目录并排除别名模型；可用性由 [workbuddy_probe.go](internal/app/workbuddy_probe.go#L195) `wbProbeAll` 探测，缓存 TTL 6 小时（`wbProbeTTLMillis`，[workbuddy_probe.go](internal/app/workbuddy_probe.go#L33)），落 `data/.workbuddy-probe.json`。**`/v1/models` 只列 `wbModelListAvailable()` 真正接入的模型**——未接入的（上游回 `11102 service info not found`）列出来客户端选中就是必然失败的请求，`/admin` 仍显示全部。

**配置**：`data/.workbuddy-config.json`（[workbuddy_client.go](internal/app/workbuddy_client.go#L71) `wbConfigPath`），结构 `wbProviderConfig{Enabled, Proxies, MaxRetries, CN, INTL}`，默认 `{Enabled:true, MaxRetries:2, CN:true, INTL:true}`。

**裸 id 冲突**：CN 与 INTL 都有 `hy3` / `glm-5.2` / `glm-5.3` 等 id，`wbModelKey(variant, id)` 用复合键存储；`providerPublicIDs()` 只对**真正重复**的 id 加 `<provider>/` 前缀，无歧义的裸名照旧暴露（设计行为）。

## 9. Qoder（qoder.go）

仅 **china 区**（`openApi` 用 `https://openapi.qoder.com.cn`，同一 PAT 在 global 区回 400）。

**常量**（[qoder.go](internal/app/qoder.go#L38)）：`qoderIdeVersion="1.1.47"`、`qoderClientType="5"`、`qoderMachineType="5"`、`qoderDataPolicy="disagree"`、`qoderLoginVer="v2"`；路径 `qoderChatPath = "algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"`、`qoderModelListPath = "algo/api/v2/model/list?Encode=1"`、`qoderExchangePath = "/api/v1/jobToken/exchange"`、`qoderUserInfoPath = "/api/v1/userinfo"`。

**凭据与签名**（[qoder.go](internal/app/qoder.go#L32-L35) 注释）：PAT（`pat-…`）→ `jobToken/exchange` 换 jobToken（无已知 refresh 端点，过期直接重换）→ 所有业务请求带 Cosy 签名：

```
Authorization: Bearer COSY.<base64(payload)>.<md5(sigInput)>
sigInput = payloadB64 \n cosyKey \n ts \n body \n sigPath
```

- `cosyKey`：16 字节随机 ASCII，用 `qoderRSAPublicKey`（[qoder.go](internal/app/qoder.go#L56)）RSA PKCS1v15 加密
- `aesKey`：AES-128-CBC，AAD/IV 布局见 `qoderCosySign`
- 请求体按 `qoderCustomAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"` 重编码（`Encode=1`）
- 客户端 IP 头固定 `127.0.0.1`（[qoder.go](internal/app/qoder.go#L513)）

**机器码**：[qoder.go](internal/app/qoder.go#L220) 优先复用本机 Qoder 客户端的 `machine_id`，否则自生成并落 `data/.qoder-machine-id`。

**上游行为**：对 `agent_chat_generation` **永远回 `model:"auto"`** —— 这是别名行为不是路由 bug，请求哪个模型由 body 里的 model 字段决定。存在 429 / `insufficient_quota` 配额抖动，同模型重试可成功。

**配置**：`data/.qoder-config.json`（[qoder.go](internal/app/qoder.go#L112)），结构 `qoderProviderConfig{Enabled, Proxies, FreeOnly, MaxRetries}`，默认 `{Enabled:true, MaxRetries:2}`。`FreeOnly` 切换后必须调 `/admin/api/providers/qoder/models/refresh` 重新同步。

**账号**：`data/.qoder-accounts.json`（[qoder.go](internal/app/qoder.go#L155)），字段 `PAT / Region / MachineID / UserID / Email`。删除账号 `qoderDeleteAccount`（[qoder.go](internal/app/qoder.go#L811)）在 `qoderAccMu.Lock()` 临界区内只调无锁的 `qoderLoadAccountsLocked()`，**不要在持锁临界区内调 `qoderLoadAccounts()`** —— 后者会再次加同一把不可重入的锁，直接死锁，曾挂起全部 `/admin` 请求。

## 10. provider SSE 中继与错误信封（provider_sse.go）

| 函数 | 位置 | 作用 |
|------|------|------|
| `relayProviderSSE` | [provider_sse.go](internal/app/provider_sse.go#L37) | 流式透传：逐帧提取后按客户端协议重写；每帧先处理再判断 break（先 break 会丢掉无结尾换行的整行） |
| `aggregateProviderSSE` | [provider_sse.go](internal/app/provider_sse.go#L98) | 非流式聚合，空帧兜底解析原文；签名 `(map[string]any, error)` |
| `wbSSEExtract` / `qoderSSEExtract` | [provider_sse.go](internal/app/provider_sse.go#L286) / [#L297](internal/app/provider_sse.go#L297) | 两家的帧解析器 |
| `innerErrorChunk` | [provider_sse.go](internal/app/provider_sse.go#L328) | 解内层错误信封（Qoder 的 `insufficient_quota` "All models failed" 曾被吞成空回答）；含 `choices` 直接短路返回 nil |
| `synthErrorChunk` | [provider_sse.go](internal/app/provider_sse.go#L363) | 造标准 error 帧（截断 500 字符） |
| `chatStreamToResponsesExtract` | [provider_sse.go](internal/app/provider_sse.go#L387) | provider 流 → Responses API 事件（`output_text.delta` / `reasoning_summary_text.delta` / `function_call`） |

## 11. 管理后台（admin.go / admin_html.go / admin_providers.go）

前端是 [admin_html.go](internal/app/admin_html.go) 里的单个 HTML 字符串常量 `adminHTML`，由 `adminStaticHandler`（[admin.go](internal/app/admin.go#L121)）在 `/admin/` 直接 Write。**改前端 = 改这个字符串**，改完必须重新编译。

**localhostGuard**（[admin.go](internal/app/admin.go#L51)）：所有 `/admin/` 路由挂到子 mux 后整体套该中间件（[admin.go](internal/app/admin.go#L118)），仅允许 `r.RemoteAddr` 为本机回环的请求，其余 403；**不信任 X-Forwarded-For**（可伪造）。判断基于 TCP 对端地址。

Tab 页（[admin_html.go](internal/app/admin_html.go#L209-L219)）：仪表盘 / 模型列表 / Cline账号管理 / 导入Cline账号 / Cline设置 / 请求日志 / opencode 免费模型 / WorkBuddy账号管理 / WorkBuddy国际版 / Qoder账号管理 / 模型聊天。

> **模型列表页是表格视图**，8 列：模型（可读名 + 裸路由 id 副行）/ 分组 / 费率 / 上下文 / 图片 / 视频 / 状态 / 操作，顶部带搜索、分组筛选、仅看免费、仅看支持图片、仅看可用，表头可点击排序。
>
> 裸路由 id 仍然保留在副行 —— 运维需要照它手写 `workbuddy/<id>` 强制路由。对外只暴露可读名的入口是 `/v1/models`，不是这一页。
>
> **模态列是三态**：`✓` 支持 / `✗` 不支持 / `?` 上游没给。数据来源见 §11.1，**不要把 `?` 当成不支持**。

### 11.1 模态数据的来源与探测

| 来源 | 图片 | 视频 | 依据 |
|------|------|------|------|
| opencode-zen | 目录 | 目录 | 官方 `api.json` 的 `modalities.input`（`catalog.go` 的 `catalogEntry.Image/Video`，`MetaKnown` 标记该条目是否带模态字段） |
| WorkBuddy | 目录 | 未知 | 目录只有 `disabledMultimodal` 一个总开关：为 true 时图片/视频都判 false；为 false 时只能确认图片，视频留 null |
| Qoder | 未知 | 未知 | 目录不含模态字段，恒为 null |
| cline | 未知 | 未知 | 官方清单只有 id/name/description/tags，恒为 null |

`POST /admin/api/models/probe-modality`（[modality.go](internal/app/modality.go)）补齐未知项：用 1×1 PNG 发一次真实的 `image_url` 请求，**走的是本代理自己的 `/v1/chat/completions`**，所以结果反映"通过这个网关能不能真发图片"而非上游裸接口行为。

- 判定：200 → 支持；400/415/422 且错误体命中 `image`/`vision`/`multimodal`/`media`/`不支持` 等关键词 → 不支持；其余（404/402/429/500/502）一律**未知**，不能当成不支持。
- 缓存 `data/.modality-probe.json`，TTL 7 天，key 是 `<source>:<baseId>`（`hy3` 在 WB CN 与 INTL 同名，不能只用裸 id）。
- 探测结果通过 `applyModalityProbe` 覆盖目录值，三个模型接口都调它，所以探测完刷新列表就能看到结论。
- 每次探测消耗一次真实额度，**只在用户点「探测」时触发**，不做批量自动探测；视频没有可用的极小样本，暂不自动探测。

### `/admin/api/*` 全量端点（均经 `corsHandler`）

| 端点 | 方法 | 用途 |
|------|------|------|
| /accounts | GET | 账号列表（隐藏 token） |
| /accounts/add · delete · test · refresh-all · delete-all · reset | POST | Cline 账号管理 |
| /accounts/export | GET | 导出 refreshToken JSON |
| /oauth/start · /oauth/status | POST/GET | 设备授权流程 |
| /sso/import · /batch-import | POST | 长 token / 批量导入 |
| /stats | GET | 池统计 |
| /logs · /logs/clear | GET/POST | 请求日志 / 清空 |
| /keys · /keys/generate · /keys/delete | GET/POST | API Key 管理 |
| /models · /models/refresh · /models/probe | GET/POST | cline 模型 / 同步 / 真实探测 |
| /models/probe-modality | POST | 模态实调探测（图片），结果缓存 7 天 |
| /config · /config/update | GET/POST | 策略 / 默认模型 / 伪装头 |
| /opencode/config · /opencode/models · /opencode/stats（+ /refresh） | GET/POST | zen 配置 / 模型 / 统计（`/zen/*` 为别名） |
| /providers/status | GET | 三 provider 状态（enabled / 账号 / 模型数 / 配置） |
| /providers/models | GET | 三 provider 全部模型（**顶层键是 provider 名**，不是 models 数组） |
| /providers/credits | GET | 积分余额 / 签到状态 / 配额 |
| /providers/{workbuddy,workbuddy-ai,qoder}/config | POST | 改 provider 配置 |
| /providers/workbuddy/import | POST | 从本机重新导入 WorkBuddy 凭据 |
| /providers/workbuddy/refresh | POST | 刷新 WorkBuddy token |
| /providers/{workbuddy,workbuddy-ai,qoder}/models/refresh | POST | 重同步模型目录 |
| /providers/qoder/accounts/add · delete | POST | Qoder PAT 账号管理 |
| /providers/{workbuddy,workbuddy-ai,qoder}/checkin | POST | 一键签到领积分 |
| /providers/{workbuddy,workbuddy-ai}/probe | POST | 模型可用性探测 |

注册位置：[admin.go](internal/app/admin.go#L78-L113)（核心）+ [admin_providers.go](internal/app/admin_providers.go#L499) `registerProviderRoutes`（三 provider 18 条）。

**启动初始化** `startProviderBoot()`（[admin_providers.go](internal/app/admin_providers.go#L521)）：无 WB 账号时 `wbReadLocalAccounts()` 自动导入本机凭据；3 秒后异步 `wbSyncModels()` 与（若有 qoder 账号）`qoderSyncModels()`。

## 12. 三协议转换

| 入口 | 位置 | 关键函数 |
|------|------|----------|
| OpenAI Chat（原生） | [proxy.go](internal/app/proxy.go#L286) | `normalizeOpenAIResponse`（[proxy.go](internal/app/proxy.go#L1935)）剥 `{data:{...}}` 包装；provider 侧 [provider_chat.go](internal/app/provider_chat.go#L239) `handleWBAChat` / [#L274](internal/app/provider_chat.go#L274) `handleQoderChat` |
| Anthropic /v1/messages | [proxy.go](internal/app/proxy.go#L297) | `anthropicToOpenAI`（[proxy.go](internal/app/proxy.go#L1073)）/ `openAIToAnthropic`（[proxy.go](internal/app/proxy.go#L1326)）/ `handleAnthropicStreamWithUsage`（[proxy.go](internal/app/proxy.go#L1654)）；工具参数按 schema 裁剪（`filterToolInput`，[proxy.go](internal/app/proxy.go#L1287)）；provider 侧 [proxy.go](internal/app/proxy.go#L1482) 调 `handleProviderAnthropicMessage`（[provider_chat.go](internal/app/provider_chat.go#L172)） |
| OpenAI Responses /v1/responses | [responses.go](internal/app/responses.go) | `responsesToChat`（[responses.go](internal/app/responses.go#L20)）/ `chatToResponses`（[responses.go](internal/app/responses.go#L156)）/ `chatStreamToResponses`（[responses.go](internal/app/responses.go#L264)）；provider 侧 [responses.go](internal/app/responses.go#L466) 调 `handleProviderResponses`（[provider_chat.go](internal/app/provider_chat.go#L206)） |

**三协议对五个来源全部生效** —— 派发点靠 `isProviderRoute(route)` 判定，三处入口缺一处就会把请求静默交给 cline 账号池（用 cline 的额度跑另一个上游且不报错）。

**System Prompt 覆盖**：项目根放 `override.md`，`applyOverride`（[proxy.go](internal/app/proxy.go#L361)）替换所有请求的 system。

## 13. 日志系统（logs.go）

| 日志 | 位置 | 内容 | 保留 |
|------|------|------|------|
| 主日志 | `data/FreeModelRelay.log` + stderr | 业务日志（选号/路由/上游/同步） | 追加 |
| 诊断日志 | `data/all-requests.log` | 所有 LLM HTTP 请求 REQ/RESP（含来源 IP/UA/状态/耗时） | 追加，排查连通问题用 |
| 请求日志 | 内存（`/admin/` 页面查看） | 仅 LLM 请求，含请求/回复内容 | **100 条，重启清空** |
| 流调试 | `data/FreeModelRelay-stream.log` | Anthropic SSE 原始事件 | 追加 |
| zen 统计 | `data/zen-stats.jsonl` | 每请求一行（upstream/model/latency/tokens/rateLimited） | 追加，启动时重建累计 |

**关键实现细节（踩过的坑）**：
- `statusWriter` 包装器必须实现 `Flush()` 透传（[logs.go](internal/app/logs.go#L151)），否则 SSE 全部变空响应
- `statusWriter.Write` 捕获副本时**绝不能修改原始 `b`**（[logs.go](internal/app/logs.go#L133)）——曾把 75KB 管理页截断到 16KB 导致页面显示一半
- seq 在请求**进入时**分配，API 返回按 seq 降序排序——否则并发下顺序跳动
- `kit.HTTPClient` 不能设整体 Timeout（流式必需），防悬挂靠 `ResponseHeaderTimeout: 120s` + 流空闲 watchdog 90s

## 14. data/ 文件一览

| 文件 | 内容 |
|------|------|
| `.cline-accounts.json` | Cline 账号池（accounts/currentIdx/defaultModel/keys） |
| `.zen-config.json` | zen 配置（上游 key/代理池/failover 参数/压缩参数/RTK） |
| `.workbuddy-accounts.json` | WorkBuddy 账号（CN + INTL，自动导入，0600） |
| `.workbuddy-config.json` | WorkBuddy 配置（Enabled/Proxies/MaxRetries/CN/INTL） |
| `.workbuddy-probe.json` | WorkBuddy 模型可用性探测缓存（TTL 6h） |
| `.workbuddy-device.json` | WorkBuddy 设备指纹 |
| `.qoder-accounts.json` | Qoder 账号（PAT/Region/MachineID/UserID/Email） |
| `.qoder-config.json` | Qoder 配置（Enabled/Proxies/FreeOnly/MaxRetries） |
| `.qoder-machine-id` | Qoder 机器码 |
| `.modality-probe.json` | 模态探测结果缓存（key=`<source>:<baseId>`，TTL 7 天） |
| `models.raw.json` | cline 模型原始响应缓存 |
| `catalog.free.json` | opencode 官方免费目录缓存 |
| `zen-stats.jsonl` | zen 请求统计 |
| `FreeModelRelay.log` | 主日志 |
| `all-requests.log` | 诊断日志 |
| `FreeModelRelay-stream.log` | Anthropic SSE 调试（运行时生成） |
| `override.md` | 可选，system prompt 覆盖（放项目根目录） |

路径统一走 `kit.ResolveDataPath`（[data.go](internal/kit/data.go)）。

## 15. 修改指南（按意图速查）

| 想做什么 | 改哪里 |
|----------|--------|
| 加/改管理后台页面 | [admin_html.go](internal/app/admin_html.go) 字符串 + [admin.go](internal/app/admin.go) 加 API |
| 改模型路由/默认模型逻辑 | [models.go](internal/app/models.go)（resolveRouteModel/routeModelAny/getDefaultModel）+ [zen.go](internal/app/zen.go) routeModel |
| 改模型 id 展示（分组/倍率/显示名） | [provider_chat.go](internal/app/provider_chat.go)（providerDecorateID/providerStripDecorate/providerRoute） |
| 改模型列表表格/筛选/模态列 | [admin_html.go](internal/app/admin_html.go)（`loadAllModels`/`renderModelTable`/`modelRow`）+ [modality.go](internal/app/modality.go) |
| 加模态数据来源 | [catalog.go](internal/app/catalog.go)（zen 目录）/ [workbuddy_client.go](internal/app/workbuddy_client.go)（`Multimodal`）/ [qoder.go](internal/app/qoder.go) |
| 改 WorkBuddy 变体/凭据 | [workbuddy.go](internal/app/workbuddy.go)（wbVariants/wbAuthDirs/wbAtRestSecret） |
| 改 WorkBuddy 上游行为 | [workbuddy_client.go](internal/app/workbuddy_client.go) |
| 改 Qoder 签名/上游行为 | [qoder.go](internal/app/qoder.go)（qoderCosySign/qoderEncodeBody） |
| 改 provider SSE 解析/错误信封 | [provider_sse.go](internal/app/provider_sse.go)（SSEExtract/innerErrorChunk） |
| 改积分/签到 | [provider_credits.go](internal/app/provider_credits.go) |
| 改账号调度策略 | [pool.go](internal/app/pool.go) pickAccount |
| 改 zen 指纹/上游行为 | [zen.go](internal/app/zen.go)（callZenAPI/applyFreeTierFingerprint）+ [ident.go](internal/kit/ident.go) |
| 改上下文压缩 | [compact.go](internal/app/compact.go#L21-L24)（`summaryOutputTokens`=4096 / `toolOutputMaxChars`=2000） |
| 改 RTK 预压缩 | [rtk.go](internal/app/rtk.go)（rtkClassify + 各类型处理器） |
| 改日志保留量 | [logs.go](internal/app/logs.go) 常量 `maxReqLogs`/`maxBodyCapture` |
| 改流超时 | [proxy.go](internal/app/proxy.go) idleTimeout(90s) + [http.go](internal/kit/http.go) ResponseHeaderTimeout(120s) |
| 改端口/监听地址 | [main.go](main.go#L16-L17) flag 默认值 |
| 加免费模型种子 | [models.go](internal/app/models.go) 种子表 / [zen.go](internal/app/zen.go#L37) zenSeedModels |
| 改 CI 构建矩阵 | [.github/workflows/build.yml](.github/workflows/build.yml) + [build.ps1](build.ps1)（两者需同步） |

## 16. 已知问题与注意事项

1. **必须在项目根目录运行 exe**——`data/` 是相对路径（[data.go](internal/kit/data.go)），从别处启动会建到别的目录
2. **双开实例**：启动时 `freePort`（[proxy.go](internal/app/proxy.go#L2030)）会自动杀掉占用 9999 的旧进程（PowerShell 实现），会有几秒服务空窗
3. **Trae 客户端 Base URL 必须写 `http://127.0.0.1:9999/v1`**：默认只监听 IPv4 127.0.0.1，而 Windows 把 `localhost` 解析为 `::1`，实测 Trae（UA=hertz）对 `localhost` 的请求会流出本机、落到 Cloudflare 报 "plain HTTP request was sent to HTTPS port" 400（2026-10-12 实测，请求从未到达代理）。IP 字面量不走解析/转发，稳定可用
4. **上游免费模型不稳定**：部分模型会返回空内容/悬挂，`/admin/` 模型探测可甄别
5. **zen 免费通道随上游策略波动**：403 FreeTierError 表示上游收紧，判据随灰度漂移（见 §7），回潮时先抓包复核再改指纹
6. **账号欠费 402**：Cline Credits 余额为负时付费模型全部 402，免费模型不受影响
7. **WorkBuddy 登录文件会轮转**：桌面端启动才创建 auth 目录下的 `.info`，`Test-Path` 可能瞬时 false；凭据读取失败时不要立刻判定"未登录"，先用 `dir /s /b /a` 确认文件存在
8. **Qoder 配额抖动**：上游存在 429 / `insufficient_quota`，同模型重试往往可成功；`FreeOnly` 切换后必须重同步模型目录
9. **WorkBuddy 有未标价模型**：上游目录缺 `credits` 字段但实测按量扣积分（如 `hunyuan-chat`），`/v1/models` 里不加计费段，`/admin` 显式提示「上游未标价」——标成免费会误导用户
