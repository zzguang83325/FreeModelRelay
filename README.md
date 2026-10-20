# FreeModelRelay

**把多个大模型上游，统一成一套 OpenAI / Anthropic / Responses 协议。**

一个 Go 单文件二进制，本地运行，不需要 Docker、Node、数据库。启动后按请求里的 `model` 字段自动路由到对应上游，客户端只需要一个 Base URL 和一个 Key。

![FreeModelRelay 架构总览](docs/architecture.svg)

<details>
<summary>纯文本版本（终端 / 纯文本环境可读）</summary>

```
客户端
  Claude Code · Codex CLI · Cursor · VSCode 插件 · Cherry Studio · SDK · curl
        │
        │  OpenAI    POST /v1/chat/completions
        │  Anthropic POST /v1/messages
        │  Responses POST /v1/responses
        │  模型列表  GET  /v1/models
        ▼
  FreeModelRelay   (默认 127.0.0.1:9999)
        │
        │  按请求里的 model 字段路由
        ├──► opencode zen    免登录，匿名免费额度
        ├──► Cline           多账号轮询，免费池 + 付费模型
        ├──► WorkBuddy       中国版，桌面端凭据自动导入
        ├──► WorkBuddy AI    国际版，同上
        └──► Qoder           阿里，个人访问令牌接入
```

</details>

---

> ## ⚠️ 使用前必读
>
> - **名字里的 `Free` 有两层意思**：一是 opencode zen 那条无需登录的匿名免费通道，二是「自由」——代码开源、本地运行、不锁定任何厂商、数据不出本机。**它不是「所有上游都免费」**：Cline 的付费模型会消耗你自己账号的额度。不过 WorkBuddy 和 Qoder 的付费模型可以靠**每日签到领的积分**来调，同样不用花钱，详见 [3.7](#37-用签到积分免费调用付费模型)。
> - 本项目**不提供、不内置、不分发任何账号、Token 或 API Key**。所有上游凭据都来自你自己已经拥有的账号，或上游公开的匿名免费通道。
> - 各上游的接口与风控随时可能变化，本项目通过复现官方客户端行为接入。**使用即表示你自行承担账号风险**，并遵守各上游的服务条款与当地法律法规。
> - 管理后台默认**没有登录鉴权**，只允许本机回环访问。**不要把它暴露到公网**，也不要为了远程访问去做端口转发。
> - 免费通道（opencode zen）是上游给的匿名额度，随时可能收紧或失效，这不是本项目能保证的能力。

---

## 目录

- [1. 特性](#1-特性)
- [2. 快速开始](#2-快速开始)
- [3. 上游来源配置](#3-上游来源配置)
- [4. 客户端接入](#4-客户端接入)
- [5. 模型与路由](#5-模型与路由)
- [6. API 参考](#6-api-参考)
- [7. 配置参考](#7-配置参考)
- [8. 进阶用法](#8-进阶用法)
- [9. 数据与日志](#9-数据与日志)
- [10. 常见问题](#10-常见问题)
- [11. 已知限制](#11-已知限制)
- [12. 开发与贡献](#12-开发与贡献)
- [13. 项目结构](#13-项目结构)
- [14. 许可证](#14-许可证)

---

## 1. 特性

**协议层**

- 三种入口协议：OpenAI Chat Completions、Anthropic Messages、OpenAI Responses，**对全部上游生效**，不是只对某一家做适配
- 路径别名兼容：`/openai/v1/*`、`/anthropic/v1/*`、`/codex/v1/*` 自动归一化到 `/v1/*`，已有配置可以直接切过来
- 流式与非流式都支持；SSE 逐帧归一化，不做整段缓冲
- 上游错误语义透传：402 欠费、429 限流、400 参数错误保留原状态码与响应体，不会一律压成 500；被上游包在内层信封里的错误（如 `insufficient_quota`）会被解出来，而不是变成空回答

**路由层**

- 模型 id 自带来源与价格：`<分组>/<可读名> · x<倍率>`，`owned_by` 就是分组名，客户端可据此按来源归类
- 五级路由解析：显式前缀 → 完整 id 精确匹配 → 裸名兜底 → 上游归属判定 → 默认模型兜底
- 默认模型可设为任意来源的模型，`model` 字段留空的请求也能正确落位

**账号层**

- 多账号轮询：Cline 支持 `round_robin` / `fill` / `random`；WorkBuddy 与 Qoder 固定轮询
- 命中额度上限自动冷却并按上游文案解析恢复时间（如 `Try again in 17h 59m`），到期自动恢复
- 凭据自动导入：本机装了 WorkBuddy 桌面端并登录，启动时自动解密导入，无需手填 token
- 积分与签到：WorkBuddy（中国版/国际版）与 Qoder 的积分余额查询 + 一键签到，**付费模型用签到领的积分就能调，不需要充值**

**运维层**

- 中文管理后台：账号、模型、密钥、日志、各上游开关与参数，一处管理
- 模型表格：模型 / 分组 / 费率 / 上下文 / 图片 / 视频 / 状态，支持搜索、筛选、排序
- 模态能力三态显示（支持 / 不支持 / 上游未给），目录给不出结论的可一键实调探测
- 请求日志、诊断日志、token 统计，全部落在 `data/` 下，重启不清空（请求详情除外）
- 多 IP 出口代理池（http / https / socks5），用于绕过单 IP 的匿名额度限制
- 上下文超限自动摘要压缩，以及工具输出预压缩（默认关闭）
- 6 平台预编译产物，GitHub Actions 自动构建发布

**依赖**

- 除标准库外只有 2 个直接依赖：`refraction-networking/utls`（出口代理池的 TLS 指纹载体）与 `golang.org/x/net`（http2 + socks5 拨号）

---

## 2. 快速开始

### 2.1 下载预编译二进制

到 Releases 页面按平台下载 `FreeModelRelay-<os>-<arch>`，解压后**放到一个空目录**运行。支持：

| 平台 | 架构 |
|------|------|
| Windows | amd64 / arm64 |
| Linux | amd64 / arm64 |
| macOS | amd64 / arm64 |

### 2.2 从源码构建

需要 **Go 1.25 或更高版本**。

```bash
git clone https://github.com/zzguang83325/FreeModelRelay.git
cd FreeModelRelay

go build -o FreeModelRelay .          # Linux / macOS
go build -o FreeModelRelay.exe .      # Windows
```

Windows 上也可以用附带的脚本：

```powershell
.\build.ps1            # 等价于 go build
.\build.ps1 -Test      # 先跑 go test 再构建
.\build.ps1 -All       # 交叉编译 6 平台到 dist/
```

### 2.3 启动

```bash
./FreeModelRelay.exe            # 默认 127.0.0.1:9999
./FreeModelRelay.exe -port 8080 # 换端口
./FreeModelRelay.exe -host 0.0.0.0   # 允许局域网访问代理端点
```

只想一步到位（编译 + 启动 + 自动打开后台，需要本机装了 Go）：

```bash
go run . -start
```

> **必须在放二进制的目录里启动。** 运行时数据写到相对路径 `data/`，换目录启动会得到一套全新的空配置。

启动后终端会打印监听地址与后台地址。第一次启动时 `data/` 会被自动创建。

### 2.4 打开管理后台

浏览器访问 **<http://127.0.0.1:9999/admin/>**。

后台按功能分页：仪表盘、模型列表、各上游账号管理、导入账号、设置、请求日志、模型聊天。

### 2.5 验证是否可用

```bash
curl http://127.0.0.1:9999/health
# {"activeAccounts":0,"status":"ok","version":"go-1.1"}

curl http://127.0.0.1:9999/v1/models
# 返回五个来源合并后的模型列表
```

此时**即使一个账号都没配**，`opencode-zen` 分组的免费模型通常已经可用——它是匿名通道，不需要登录。

### 2.6 第一个请求

```bash
curl http://127.0.0.1:9999/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "opencode-zen/big-pickle · free",
    "messages": [{"role": "user", "content": "用一句话解释什么是反向代理"}],
    "stream": false
  }'
```

能返回内容就说明链路通了。接着看 [第 3 节](#3-上游来源配置) 配置你自己有额度的上游。

---

## 3. 上游来源配置

### 3.1 来源总览

| 分组（`owned_by`） | 上游 | 需要账号 | 凭据形态 | 额度来源 |
|------|------|------|------|------|
| `opencode-zen` | opencode zen | 否 | 无（匿名 `public` key） | 上游匿名免费额度 |
| `cline-free` | Cline | 是 | refreshToken / OAuth | 该分组整池免费，不扣额度 |
| `workbuddy` | WorkBuddy 中国版 | 是 | 桌面端登录态（自动解密） | 账号积分，**每日签到免费领** |
| `workbuddy-ai` | WorkBuddy 国际版 | 是 | 桌面端登录态（自动解密） | 账号积分，**每日签到免费领** |
| `qoder` | Qoder（阿里） | 是 | 个人访问令牌 `pat-…` | 账号积分，**签到活动免费领** |

> **积分不用花钱**：WorkBuddy 和 Qoder 的付费模型靠「积分」计费，而积分可以每天签到免费领。签到一次能用一整天，不充值也能调用付费模型。具体见 [3.7 用签到积分免费调用付费模型](#37-用签到积分免费调用付费模型)。

所有账号都只在**管理后台**配置，没有命令行参数。各来源的导入方式互不相同，后台还提供测试、重置、删除，这些 CLI 都表达不了。

配好之后可以随时在后台对应页面开关某个来源，或修改它的代理、重试次数等参数。

---

### 3.2 opencode zen（免登录，开箱即用）

**不需要任何配置。** 这是上游公开的匿名免费通道，启动即可调用，`/v1/models` 里 `owned_by=opencode-zen` 的就是它。

- **模型来源**：每 10 分钟拉取一次上游官方目录，只收录 `cost.input == 0` 且未废弃的模型。上游新增免费模型会自动出现，下架的会自动消失。
- **付费模型会被拒绝**：目录里存在但属于付费的 zen 模型，调用时返回 400，不会偷偷扣费。
- **上下文**：部分模型支持到 1M token；超限时自动触发摘要压缩（见 [8.2](#82-上下文压缩)）。

**可调参数**（后台 **opencode 免费模型** 页）：

| 参数 | 默认 | 说明 |
|------|------|------|
| 启用 | 开 | 关闭后 zen 模型从 `/v1/models` 消失 |
| 出口代理池 | 空 | 多行 `http://` / `socks5://`，用于多 IP 轮换 |
| 代理策略 | `round_robin` | `round_robin` / `random` / `fill` |
| 最大并发 | 8 | 防止瞬时打满上游限额 |
| 重试次数 | 3 | 限流与网络错误的重试次数 |
| 故障转移 | 开 | zen 连续失败后临时改走 Cline 账号池 |
| 触发次数 / 窗口 | 3 次 / 5 分钟 | 故障转移的判定条件 |

> 这个通道由上游风控决定生死。如果某天全部返回 403，通常是上游收紧了判据，不是配置错了。可以打开故障转移让请求临时落到 Cline 账号池。

---

### 3.3 Cline

**三种导入方式**，任选其一。入口：后台 **导入Cline账号** 页。

#### 方式一：OAuth 浏览器登录（推荐）

1. 打开 **导入Cline账号 → OAuth 浏览器登录**
2. 点「开始授权」，页面会给出一个链接和一次性验证码，同时自动尝试打开浏览器
3. 在浏览器里用 Google / GitHub / 邮箱登录并确认
4. 回到后台，状态变为已授权即完成，账号自动进入账号池

整个过程走的是 Cline 官方的设备授权流程，Token 由上游签发，本地不保存密码。

#### 方式二：手动输入 Token

已有 refreshToken 时，粘贴到 **导入Cline账号 → 手动输入 Token**。

导入时会**真实调用一次刷新接口做校验**——Token 无效会当场报错，不会留下一个永远 401 的僵尸账号。可选填邮箱作为备注名，不填会自动生成。

#### 方式三：批量导入

适合迁移或批量采购的场景，粘贴 JSON：

```json
{
  "tokens": [
    { "refreshToken": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "email": "a@example.com" },
    { "refreshToken": "yyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy", "email": "b@example.com" }
  ]
}
```

每个 Token 都会被逐个校验，返回「导入 N 个 / 失败 M 个」以及失败原因。

#### 账号管理

**Cline账号管理** 页可以看到每个账号的状态、今日与累计调用次数、token 消耗，并支持：

| 操作 | 作用 |
|------|------|
| ⚡ 测试 | 用该账号发一次真实请求，确认可用 |
| ↻ 重置 | 清除冷却状态，立即恢复参与轮询 |
| ✕ 删除 | 从池中移除 |
| 导出 | 导出全部 refreshToken 为 JSON（迁移或备份） |

**额度与冷却**：付费模型消耗账号额度；命中额度上限（429 `INFERENCE_CAP_ERROR`）时，会从上游文案里解析出恢复时间并标记冷却，到期自动解除。解析不出时间时按 18 小时处理。

**免费模型**：`owned_by=cline-free` 的模型整池免费，不消耗账号额度，也不需要积分。

**付费模型**：Cline 的付费模型**不在** `/v1/models` 里（`cline-free` 分组只收免费池），但用上游原始 id（如 `deepseek/deepseek-v4-pro`）仍然可以直接调用——路由解析不命中时默认落到 Cline 账号池。这类请求会消耗账号额度。

**调度策略**（后台 **Cline设置** 页）：`round_robin`（默认，轮流）、`fill`（打满第一个再换）、`random`（随机）。

> 策略与自定义请求头保存在内存里，**重启后回到默认值**；默认模型是持久化的，不会丢。

---

### 3.4 WorkBuddy 中国版

**前置条件**：本机安装了 WorkBuddy 桌面端（腾讯 CodeBuddy）并已登录。凭据从桌面端本地存储里解密读取，不需要你手工找 token。

**导入步骤**：

1. 确保桌面端处于登录状态，并且**至少启动过一次**（登录文件由客户端在运行时创建和轮转）
2. 启动代理，程序会自动尝试导入本机账号
3. 打开后台 **WorkBuddy账号管理** 页确认；如果为空，点「导入本机账号」
4. Token 过期时点「刷新 token」

**可调参数**：

| 参数 | 默认 | 说明 |
|------|------|------|
| 启用 | 开 | 关闭后 `workbuddy` 分组消失 |
| 中国版 / 国际版 | 都开 | 两个变体可以分别启用 |
| 出口代理 | 空 | 同上，多行 |
| 重试次数 | 2 | |

**积分与签到**：页面顶部显示当前积分余额和今日签到状态，点「签到」或仪表盘的「一键签到」即可领取当天积分。**积分就是调用付费模型的凭据，而签到不花钱**——每天签到一次，就够用来调 `workbuddy/DeepSeek-V3.2 · x0.29` 这类付费模型。详见 [3.7](#37-用签到积分免费调用付费模型)。

**其他**：「模型可用性探测」会用真实请求确认哪些模型当前可用（结果缓存 6 小时）。

> **为什么有的模型在后台有、在 `/v1/models` 里没有？** 因为探测确认它当前不可用（上游回 `service info not found`）。列出来只会让客户端选中一个必然失败的模型，所以对外列表只放真正接得通的。后台仍然显示全量。

**桌面端装在非默认位置**时，可以用环境变量指定 Electron 可执行文件：

```bash
WORKBUDDY_ELECTRON_BIN="D:\path\to\WorkBuddy.exe"
```

---

### 3.5 WorkBuddy 国际版

配置方式与中国版**完全相同**，只是账号来自国际版客户端（`www.workbuddy.ai`），对应后台 **WorkBuddy国际版** 页，分组名是 `workbuddy-ai`。

两个版本是独立的账号体系、独立的模型目录、独立的积分。同名模型（如 `GLM-5.3`）在两个版本里都存在时，靠 id 末尾的倍率段区分。

国际版客户端装在非默认位置时：

```bash
WORKBUDDY_AI_ELECTRON_BIN="D:\path\to\workbuddyai.exe"
```

---

### 3.6 Qoder（阿里）

**前置条件**：Qoder 账号，并生成一个**个人访问令牌**（`pat-…` 开头）。目前只支持**中国区**，同一个令牌在 global 区会返回 400。

**导入步骤**：

1. 后台 **Qoder账号管理** 页
2. 粘贴 `pat-…` 令牌，区域选 `china`
3. 保存后会自动换取短期 jobToken 并同步模型目录

**令牌会过期**，程序在运行时自动重新换取，不需要手动维护。机器码优先复用本机 Qoder 客户端的，保证上游看到的是同一个设备。

**可调参数**：

| 参数 | 默认 | 说明 |
|------|------|------|
| 启用 | 开 | |
| 仅免费模型 | 关 | 打开后只同步不扣积分的模型；**切换后需要点「刷新模型」重新同步** |
| 出口代理 | 空 | |
| 重试次数 | 2 | |

**积分与签到**：Qoder 的付费模型按积分计费，而积分可以免费领——后台 Qoder 页顶部有「签到」，点一下领取当天活动奖励（上游 `campaigns` 里标 `CREDITS` 的条目），仪表盘的「一键签到」也会一并处理。**不需要充值**。详见 [3.7](#37-用签到积分免费调用付费模型)。

> Qoder 上游存在配额抖动：偶发 429 或 `insufficient_quota`，同一个模型重试往往就能成功，程序已内置重试。

---

### 3.7 用签到积分免费调用付费模型

这是本项目最容易被忽略的一点：**WorkBuddy 和 Qoder 的付费模型，用每天签到领的积分就能调，不需要充值。**

#### 积分是什么

WorkBuddy（中国版 / 国际版）和 Qoder 都按「积分」（credits）计费。模型 id 末尾的倍率就是扣积分的系数：

| 模型 id 示例 | 倍率 | 扣不扣积分 |
|---|---|---|
| `workbuddy/Hy3 · free` | 0 | 不扣 |
| `workbuddy/DeepSeek-V3.2 · x0.29` | 0.29 倍 | 扣 |
| `workbuddy-ai/GLM-5.3 · x0.79` | 0.79 倍 | 扣 |
| `qoder/DeepSeek-Flash · x0.1` | 0.1 倍 | 扣 |

倍率是「相对基准价的折扣系数」，`x0.29` 表示按 0.29 倍扣积分。**扣积分不等于要付钱**——因为积分本身可以免费获得。表中后三行就是「付费模型」，它们扣积分，而积分可以签到免费拿。

#### 积分从哪来

| 来源 | 签到入口 | 每次签到大致给多少 |
|---|---|---|
| WorkBuddy 中国版 | 后台「签到」，或仪表盘「一键签到」 | 约 100 积分 / 天（上游活动决定） |
| WorkBuddy 国际版 | 同上（账号体系和积分独立） | 同上 |
| Qoder | 后台「签到」，领取 `campaigns` 里标 `CREDITS` 的奖励 | 按当期活动发放 |

Cline 免费池和 opencode zen 不需要签到：前者整池不扣额度，后者是匿名免费通道。

#### 怎么操作

1. 打开后台 `http://127.0.0.1:9999/admin/`
2. 仪表盘顶部的「💎 积分与签到」区块会列出每个来源的**余额**和**今日签到状态**
3. 点「🎁 一键签到」，对所有开启了签到活动的来源依次签到，已签到的自动跳过
4. 也可以进对应来源的页面单独签到，能看到连续天数、本周签到进度

签到成功后余额立刻刷新，接着就能直接调用带倍率的付费模型了。

#### 需要注意

- **签到活动由上游控制**，可能随时开启、暂停或调整奖励。后台会显示「签到活动未开启或已过期」，这种情况只能等上游重新开放，程序无法绕过。
- **积分会耗尽**：签到补的是每天的基础量。高频调用付费模型仍会用完，用完时上游返回 429 / `insufficient_quota`，等第二天签到，或者换 `· free` 模型。
- **免费模型不扣积分**：id 以 `· free` 结尾的模型倍率为 0，随便用，不消耗签到得来的积分。
- **积分不是钱包余额**：不能提现、不能转移，只在该上游内部有效。
- **多账号可以分别签到**，额度按账号累加，见下一节。

---

### 3.8 多账号与轮询

- **Cline**：策略可选，账号多了建议 `round_robin`；想榨干单个账号额度用 `fill`
- **WorkBuddy / Qoder**：固定轮询，添加多个账号即自动分摊

账号被标记冷却时不会参与轮询，恢复后自动回到队列，不需要手动干预。每个账号的积分和签到状态是独立的，所以多账号时建议每个都签到。

---

## 4. 客户端接入

### 4.1 先搞清楚三件事

**① Base URL 该不该带 `/v1`**

不同客户端对这个约定不一样，填错就会 404：

| 客户端类型 | Base URL |
|------|------|
| OpenAI 兼容（绝大多数） | `http://127.0.0.1:9999/v1` |
| Anthropic 兼容（Claude Code 等） | `http://127.0.0.1:9999`（**不带 `/v1`**，客户端自己拼 `/v1/messages`） |
| 直接填完整 URL 的客户端 | `http://127.0.0.1:9999/v1/chat/completions` |

**② 用 `127.0.0.1`，不要用 `localhost`**

Windows 把 `localhost` 优先解析为 IPv6 的 `::1`，而代理默认只监听 IPv4 的 `127.0.0.1`。个别客户端（实测 Trae）还会把 `localhost` 的请求交给系统代理，结果流出本机落到 Cloudflare，报 `plain HTTP request was sent to HTTPS port`。写 IP 字面量没有这个问题。

**③ API Key 怎么填**

后台 **Cline设置 → API Keys** 生成。**一个 Key 都没生成时，代理不做鉴权**，此时客户端 Key 随便填（甚至留空）都能过。一旦生成了 Key，所有请求必须带 `x-api-key` 或 `Authorization: Bearer`。

---

### 4.2 通用示例：curl / SDK

**OpenAI Chat Completions**

```bash
curl http://127.0.0.1:9999/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-xxxxxxxx" \
  -d '{
    "model": "workbuddy/DeepSeek-V3.2 · x0.29",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

**Anthropic Messages**

```bash
curl http://127.0.0.1:9999/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-xxxxxxxx" \
  -d '{
    "model": "qoder/DeepSeek-Flash · x0.1",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

**OpenAI Responses**

```bash
curl http://127.0.0.1:9999/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-xxxxxxxx" \
  -d '{
    "model": "workbuddy-ai/Balanced · x0.59",
    "input": "你好"
  }'
```

**OpenAI Python SDK**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:9999/v1",
    api_key="sk-xxxxxxxx",
)

resp = client.chat.completions.create(
    model="opencode-zen/big-pickle · free",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
```

**Anthropic Python SDK**

```python
from anthropic import Anthropic

client = Anthropic(
    base_url="http://127.0.0.1:9999",   # 不带 /v1
    api_key="sk-xxxxxxxx",
)

msg = client.messages.create(
    model="qoder/DeepSeek-Flash · x0.1",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(msg.content[0].text)
```

---

### 4.3 Claude Code

Claude Code 走 Anthropic 协议，用环境变量指定端点：

**macOS / Linux**

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:9999
export ANTHROPIC_AUTH_TOKEN=sk-xxxxxxxx
export ANTHROPIC_MODEL="qoder/DeepSeek-Flash · x0.1"
claude
```

**Windows PowerShell**

```powershell
$env:ANTHROPIC_BASE_URL="http://127.0.0.1:9999"
$env:ANTHROPIC_AUTH_TOKEN="sk-xxxxxxxx"
$env:ANTHROPIC_MODEL="qoder/DeepSeek-Flash · x0.1"
claude
```

要点：

- `ANTHROPIC_BASE_URL` **不要带 `/v1`**
- 推荐用 `ANTHROPIC_AUTH_TOKEN`（会放进 `Authorization: Bearer`）；`ANTHROPIC_API_KEY`（走 `x-api-key`）本代理也支持，两者都行
- 模型名建议用 `provider/裸id` 的写法（如 `qoder/DeepSeek-Flash`），避免某些版本对空格和 `·` 处理不当
- 想给「小模型」（生成标题、压缩上下文）单独指定：新版用 `ANTHROPIC_DEFAULT_HAIKU_MODEL`，旧版是 `ANTHROPIC_SMALL_FAST_MODEL`

---

### 4.4 Codex CLI

Codex CLI 走 OpenAI Responses 协议。编辑 `~/.codex/config.toml`：

```toml
model = "workbuddy/DeepSeek-V3.2 · x0.29"
model_provider = "fbp"

[model_providers.fbp]
name = "FreeModelRelay"
base_url = "http://127.0.0.1:9999/v1"
wire_api = "responses"
env_key = "FBP_API_KEY"
```

然后设置 Key：

```bash
export FBP_API_KEY=sk-xxxxxxxx        # Windows: set FBP_API_KEY=sk-xxxxxxxx
```

`wire_api` 必须是 `responses`；填 `chat` 会走错协议。

---

### 4.5 Cursor

1. `Settings → Models → OpenAI API Key`，填入后台生成的 Key
2. 展开 **Override OpenAI Base URL**，填 `http://127.0.0.1:9999/v1`
3. 点 **Verify**，通过后在下方的模型列表里 **Add model**，把 `/v1/models` 里的 id 原样粘进去
4. 在聊天窗口切到刚添加的模型

注意事项：

- Cursor 的自定义模型只对 OpenAI 通道生效，且**每个模型都要手动添加**，不会自动读取整个列表
- 它偶尔会对 Base URL 做连通性检查，如果 Verify 失败但 curl 正常，通常是 Key 没填或 URL 少了 `/v1`
- Cursor 内部有些功能（补全、Agent 的特定工具）绑定了官方模型，切到第三方后不一定全部可用

---

### 4.6 VSCode 系 AI 插件（Cline / Roo Code / Kilo Code）

这类插件都支持「OpenAI Compatible」提供商，配置项一致：

| 字段 | 填什么 |
|------|------|
| API Provider | `OpenAI Compatible` |
| Base URL | `http://127.0.0.1:9999/v1` |
| API Key | 后台生成的 Key |
| Model ID | `/v1/models` 里的 id，如 `cline-free/mimo-v2.6-flash · free` |

有些插件还要求勾选「支持流式」「支持工具调用」「支持图片」——按模型实际能力勾，图片能力可以在后台模型列表页看三态标识。

---

### 4.7 Continue

编辑 `~/.continue/config.yaml`：

```yaml
models:
  - name: freemodelrelay
    provider: openai
    model: "workbuddy/DeepSeek-V3.2 · x0.29"
    apiBase: http://127.0.0.1:9999/v1
    apiKey: sk-xxxxxxxx
    roles:
      - chat
      - edit
      - apply
```

旧版 `config.json` 用 `models[].provider: "openai"` + `apiBase`，字段名相同。

---

### 4.8 Cherry Studio

1. `设置 → 模型服务 → 添加提供商`
2. 类型选 **OpenAI**
3. API 地址填 `http://127.0.0.1:9999/v1`，密钥填后台生成的 Key
4. 点「管理模型」→ 手动添加模型 id（或先点「获取模型列表」）

Cherry Studio 是按「提供商」分组的，不会读 `owned_by`。想让五个来源在侧栏各自成组，就建五个提供商，Base URL 都填同一个地址，然后在各自的「管理模型」里只添加对应分组的模型。

---

### 4.9 ChatBox

1. `设置 → 模型提供方 → 添加自定义提供方`
2. API 模式选 **OpenAI API 兼容**
3. API 域名填 `http://127.0.0.1:9999`
4. API 路径填 `/v1/chat/completions`
5. 模型名填 `/v1/models` 里的 id

---

### 4.10 LobeChat

客户端版：`设置 → 语言模型 → OpenAI → 代理地址` 填 `http://127.0.0.1:9999/v1`。

自部署版用环境变量：

```bash
OPENAI_API_KEY=sk-xxxxxxxx
OPENAI_PROXY_URL=http://127.0.0.1:9999/v1
```

模型需要在 LobeChat 的模型配置里显式启用（它有一份内置白名单，第三方模型名要手动加）。

---

### 4.11 Open WebUI

1. `设置 → 连接 → OpenAI API`
2. URL 填 `http://127.0.0.1:9999/v1`，Key 填后台生成的 Key
3. 保存后模型会自动出现在下拉框里

如果 Open WebUI 跑在 Docker 里，`127.0.0.1` 指的是容器自己，要改用 `http://host.docker.internal:9999/v1`（Linux 上加 `--add-host=host.docker.internal:host-gateway`）。

---

### 4.12 NextChat

`设置 → 自定义接口`：

- 接口地址：`http://127.0.0.1:9999`
- API Key：后台生成的 Key
- 模型：自定义，填 `/v1/models` 里的 id

NextChat 会自己拼 `/v1/chat/completions`，所以接口地址不要带 `/v1`。

---

### 4.13 Dify

1. `设置 → 模型供应商 → OpenAI-API-compatible`
2. Base URL：`http://127.0.0.1:9999/v1`
3. API Key：后台生成的 Key
4. Model name：`/v1/models` 里的 id
5. 按模型实际能力勾选「支持视觉」等开关

Dify 在 Docker 里部署时同样要把 `127.0.0.1` 换成宿主机可达地址。

---

### 4.14 n8n / 低代码平台

**用 OpenAI 节点**：在 Credentials 里把 Base URL 改成 `http://127.0.0.1:9999/v1`。

**用 HTTP Request 节点**：直接 `POST http://127.0.0.1:9999/v1/chat/completions`，Body 用 JSON，Header 带 `Authorization: Bearer sk-...`。这种方式最稳，不受节点封装的限制。

---

### 4.15 LangChain / LlamaIndex

```python
from langchain_openai import ChatOpenAI

llm = ChatOpenAI(
    base_url="http://127.0.0.1:9999/v1",
    api_key="sk-xxxxxxxx",
    model="opencode-zen/big-pickle · free",
)
```

```python
from llama_index.llms.openai_like import OpenAILike

llm = OpenAILike(
    api_base="http://127.0.0.1:9999/v1",
    api_key="sk-xxxxxxxx",
    model="workbuddy/DeepSeek-V3.2 · x0.29",
    is_chat_model=True,
)
```

---

### 4.16 One API / New API（二次分发）

想把这套模型再分发给团队，可以在 One API / New API 里建一个渠道：

- 渠道类型：**OpenAI**
- 代理地址：`http://127.0.0.1:9999`
- 密钥：后台生成的 Key

或者用 **Anthropic** 类型渠道指向 `http://127.0.0.1:9999`，让上游走 Messages 协议。

---

### 4.17 其他 OpenAI 兼容客户端

只要客户端支持自定义 Base URL，基本都能用，记住三条：

1. Base URL 填 `http://127.0.0.1:9999/v1`
2. 模型 id 从 `/v1/models` 复制（带空格和 `·` 也照抄）
3. 如果客户端不接受带空格的模型名，改用 `provider/裸id`（如 `workbuddy/DeepSeek-V3.2`）或直接用裸 id

---

## 5. 模型与路由

### 5.1 模型 id 长什么样

`GET /v1/models` 返回的每个 id 都是**展示层构造**的，由三段组成：

```
<分组>/<上游可读显示名> · x<倍率>     付费模型
<分组>/<上游可读显示名> · free        免费模型
<分组>/<上游可读显示名>              上游没标价（既非免费也无已知倍率）
```

| 分组（`owned_by`） | 来源 | 示例 |
|------|------|------|
| `cline-free` | Cline 免费池 | `cline-free/mimo-v2.6-flash · free` |
| `opencode-zen` | opencode zen | `opencode-zen/big-pickle · free` |
| `workbuddy` | WorkBuddy 中国版 | `workbuddy/DeepSeek-V3.2 · x0.29` |
| `workbuddy-ai` | WorkBuddy 国际版 | `workbuddy-ai/Balanced · x0.59` |
| `qoder` | Qoder | `qoder/DeepSeek-Flash · x0.1` |

> 条目数随上游目录变化，不要写死在配置里。

**倍率**是相对基准价的计费系数，`x0.29` 表示按该账号积分 0.29 倍扣费；`· free` 表示倍率为 0、不扣额度。挑带倍率的模型就是会扣积分的模型——但**积分可以每天签到免费领**，所以「会扣积分」不等于「要花钱」，详见 [3.7](#37-用签到积分免费调用付费模型)。

**可读名**是上游给的展示名，不是内部路由 id。用户不需要知道 `dfmodel` 这种代号。同名模型（中国版与 Qoder 都有 `GLM-5.3`）靠倍率段区分，整串 id 仍然唯一。

**条目字段**：

| 字段 | 说明 |
|------|------|
| `id` | 展示 id，可直接填进客户端 |
| `object` | 固定 `model` |
| `created` | 毫秒时间戳 |
| `owned_by` | 分组名 |
| `source` | 内部来源标记（`free` / `zen-free` / `workbuddy` / `qoder`） |
| `status` | `active` |
| `cost` | `free` / `paid` |
| `context` / `output` | 上下文与输出 token 上限（上游没给时为 0） |

### 5.2 路由解析顺序

三种协议入口共用同一套逻辑，按以下顺序解析：

1. **显式前缀**：`workbuddy/`（别名 `wb/`）、`workbuddy-ai/`（`wb-ai/`、`workbuddyai/`）、`qoder/`（`qd/`）强制指定来源，用于同名模型消歧
2. **完整展示 id 精确匹配**：从 `/v1/models` 原样复制的整串（含倍率段）精确命中
3. **裸名兜底**：在已同步的模型目录里按内部 id 或显示名解析
4. **上游归属判定**：都不命中时，zen 免费模型走 zen；zen 付费模型 400 拒绝；其余走 Cline 账号池
5. **空 / 未知模型**：回退到默认模型，再按默认模型重新走一遍上述判定

**向后兼容**：旧配置里写的干净 id（`dfmodel`、`hy3`、`deepseek/deepseek-v4-flash`）继续可用；反过来，从 `/v1/models` 原样复制的展示 id 也能正确落位。

**别名被排除**：`auto`、`default`、`default-model` 这类「让上游自己挑」的占位符不出现在列表里，直接调用返回 400。

### 5.3 默认模型

后台 **模型列表** 页可以为任意模型点「设为默认」。之后：

- 请求里 `model` 为空或无法识别时，使用这个默认模型
- 默认模型可以是任何来源，包括 zen 免费模型和第三方 provider 模型
- 设置会持久化到账号池文件，重启不丢

### 5.4 模型列表页

后台 **模型列表** 是表格视图，8 列：模型（可读名 + 裸路由 id 副行）、分组、费率、上下文、图片、视频、状态、操作。顶部支持关键词搜索、按分组筛选、仅看免费、仅看支持图片、仅看可用；表头可点击排序。

裸路由 id 保留在副行，方便手写 `workbuddy/<id>` 强制路由。对外只暴露可读名的入口是 `/v1/models`，不是这一页。

**模态列是三态**：

| 显示 | 含义 |
|------|------|
| `✓` | 确认支持 |
| `✗` | 确认不支持 |
| `?` | 上游没给信息，**不代表不支持** |

目录给不出结论时，可以点「探测」——用一张 1×1 的图片真实发一次请求，走的是本代理自己的 `/v1/chat/completions`，所以结论反映「通过这个网关能不能发图片」，而不是上游裸接口的行为。结果缓存 7 天。每次探测消耗一次真实额度，所以不做批量自动探测。

---

## 6. API 参考

### 6.1 端点

| 方法 | 路径 | 协议 | 鉴权 |
|------|------|------|------|
| POST | `/v1/chat/completions` | OpenAI Chat | 是 |
| POST | `/v1/messages` | Anthropic Messages | 是 |
| POST | `/v1/responses` | OpenAI Responses | 是 |
| GET | `/v1/models` | 模型列表 | 是 |
| GET | `/health`、`/v1/health` | 健康检查 | 否 |
| GET | `/admin/` | 管理后台 | 仅本机回环 |

**路径别名**：`/chat/completions`、`/messages`、`/responses`、`/models` 不带 `/v1` 也能用；`/openai/v1/*`、`/anthropic/v1/*`、`/codex/v1/*` 会被归一化到 `/v1/*`。

### 6.2 鉴权

两种头任选其一：

```
x-api-key: sk-xxxxxxxx
Authorization: Bearer sk-xxxxxxxx
```

**未在后台生成任何 Key 时，代理不做鉴权**，所有请求放行。生成了 Key 之后，`/v1/*` 全部需要携带。

管理后台的 `/admin/*` 不受此机制影响，它走的是另一套规则：只允许 TCP 对端为本机回环的连接，其余一律 403，且**不信任 `X-Forwarded-For`**（可伪造）。

### 6.3 错误语义

| 状态码 | 含义 | 处理建议 |
|------|------|------|
| 400 | 参数错误 / 请求了 zen 付费模型 / 未知模型 | 看响应体的 `error.message` |
| 401 | API Key 无效，或账号池为空 | 检查 Key；到后台导入账号 |
| 402 | 上游欠费 | 换账号或换模型 |
| 429 | 上游限流 / 额度用尽 | 等待冷却，或加账号 |
| 502 | 上游返回了无法解析的内容 | 看 `data/all-requests.log` |

上游的 4xx / 5xx 会**保留原状态码与响应体**透传，只有 401 会被本地拦截（避免把「你的 Key 错了」和「上游账号失效」混为一谈）。

---

## 7. 配置参考

### 7.1 命令行参数

| 参数 | 默认 | 说明 |
|------|------|------|
| `-host` | `127.0.0.1` | 监听地址。`0.0.0.0` 允许局域网访问代理端点（后台仍然只限本机） |
| `-port` | `9999` | 监听端口 |
| `-start` | 关 | 编译 + 启动 + 自动打开管理后台（需要本机有 Go） |

### 7.2 环境变量

| 变量 | 作用 |
|------|------|
| `WORKBUDDY_ELECTRON_BIN` | 指定 WorkBuddy 中国版 Electron 可执行文件路径 |
| `WORKBUDDY_AI_ELECTRON_BIN` | 指定 WorkBuddy 国际版 Electron 可执行文件路径 |

> **本程序不读取 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`。** 上游请求默认直连。要走代理，请到后台对应来源页面配置「出口代理池」（支持 `http://`、`https://`、`socks5://`）。这样做的好处是代理可以按来源分别设置、可以多 IP 轮换。

### 7.3 管理后台各页

| 页面 | 用途 |
|------|------|
| 仪表盘 | 总览：账号数、今日调用、token 消耗、各来源状态 |
| 模型列表 | 全部来源的模型表格，可设默认模型、探测模态能力 |
| Cline账号管理 | 账号列表、测试、重置、删除、导出 |
| 导入Cline账号 | OAuth / 手动 Token / 批量导入 |
| Cline设置 | 调度策略、API Keys、自定义请求头 |
| 请求日志 | 最近 100 条 LLM 请求的请求体与响应体 |
| opencode 免费模型 | zen 开关、代理池、并发、重试、故障转移、压缩参数 |
| WorkBuddy账号管理 | 账号、积分、签到、模型探测 |
| WorkBuddy国际版 | 同上，国际版 |
| Qoder账号管理 | PAT 账号、积分、签到、模型同步 |
| 模型聊天 | 直接在页面里试模型，不用切客户端 |

### 7.4 System Prompt 覆盖

在启动目录放一个 `override.md`，内容会替换**所有请求**的系统提示词。删掉文件即恢复客户端自带。用于统一人格设定或强制某种输出格式。

### 7.5 自定义请求头

后台 **Cline设置 → 请求头** 可以增改转发给 Cline 上游的自定义头。默认已经带了一套模拟官方客户端的头，通常不需要改。

---

## 8. 进阶用法

### 8.1 多 IP 出口代理池

适用场景：上游按 IP 限制匿名额度，单 IP 很快打满。

在后台对应来源页填入多个代理，一行一个：

```
http://user:pass@1.2.3.4:8080
socks5://5.6.7.8:1080
http://9.10.11.12:3128
```

策略：

| 策略 | 行为 |
|------|------|
| `round_robin` | 按顺序轮流（默认） |
| `random` | 随机挑一个 |
| `fill` | 固定用第一个，直到它被限流 |

代理本身被限流时会进入冷却，自动跳到下一个。

### 8.2 上下文压缩

当请求的估算 token 超过「上下文上限 − 输出预算」时，自动执行摘要压缩：

1. 序列化对话，保留尾部预算内的最近消息
2. 用模型自身生成摘要
3. 重组成「摘要 + 最近消息」继续请求
4. 摘要失败则降级为截断

参数在后台 **opencode 免费模型** 页可调：

| 参数 | 默认 | 说明 |
|------|------|------|
| 自动压缩 | 开 | |
| 输出缓冲 | 20000 | 为回复预留的 token |
| 尾部保留 | 8000 | 保留最近多少 token 不压缩 |
| 摘要上限 | 4096 | 摘要本身的最大输出 |
| 摘要模型 | 空 | 留空表示用当前请求的模型 |

### 8.3 工具输出预压缩

针对 Agent 场景里超长的 `tool_result`（文件列表、目录树、git status 等）做确定性瘦身，在发往上游前就砍掉噪音，省输入 token。

| 级别 | 行为 |
|------|------|
| 0（默认） | 关闭 |
| 1 | 只处理无歧义的安全类型 |
| 2 | 额外截断 `git diff` |

同一输入恒定输出，不会破坏上游的 prompt cache。

### 8.4 故障转移

zen 连续失败达到阈值后，进入一个降级窗口，期间 zen 请求临时改走 Cline 账号池，窗口结束自动恢复。默认 3 次失败触发、窗口 5 分钟。这是「免费通道突然失效时不至于全线不可用」的兜底。

### 8.5 局域网共享

```bash
./FreeModelRelay.exe -host 0.0.0.0
```

其他设备把 Base URL 指向 `http://<你的局域网IP>:9999/v1`。

**务必先在后台生成 API Key**，否则同网段任何人都能用你的额度。后台页面本身不会对外开放（回环校验），所以远程管理只能通过 SSH 隧道之类的方案，不建议直接把 9999 端口映射到公网。

---

## 9. 数据与日志

所有运行时数据都在启动目录的 `data/` 下。

### 9.1 配置文件

| 文件 | 内容 |
|------|------|
| `.cline-accounts.json` | Cline 账号池、当前索引、默认模型、API Keys |
| `.zen-config.json` | zen 开关、出口代理、故障转移、压缩与预压缩参数 |
| `.workbuddy-accounts.json` | WorkBuddy 账号（中国版 + 国际版，权限 0600） |
| `.workbuddy-config.json` | WorkBuddy 开关、代理、重试、双变体启用状态 |
| `.workbuddy-probe.json` | 模型可用性探测缓存（6 小时） |
| `.workbuddy-device.json` | WorkBuddy 设备指纹 |
| `.qoder-accounts.json` | Qoder 账号与令牌 |
| `.qoder-config.json` | Qoder 开关、代理、重试、仅免费模式 |
| `.qoder-machine-id` | Qoder 机器码 |
| `.modality-probe.json` | 模态探测缓存（7 天） |
| `models.raw.json` | Cline 模型原始响应缓存 |
| `catalog.free.json` | zen 免费模型目录缓存 |

### 9.2 日志

| 文件 | 内容 | 保留 |
|------|------|------|
| `FreeModelRelay.log` | 主日志：启动、选号、路由、上游错误 | 追加 |
| `all-requests.log` | 所有 LLM 请求的 REQ/RESP 摘要（含 UA、状态码、耗时） | 追加 |
| `FreeModelRelay-stream.log` | Anthropic 流式事件原始记录 | 追加 |
| `zen-stats.jsonl` | 每次 zen 请求一行：模型、耗时、token、是否被限流 | 追加 |

后台 **请求日志** 页里的详情只保留最近 100 条，**重启清空**；需要长期留存的看上面几个文件。

排查连通性问题时先看 `all-requests.log`——它会记录请求有没有真的到达代理。

### 9.3 备份与迁移

要迁移到另一台机器，复制整个目录（含 `data/`）即可。里面包含账号凭据，注意妥善保管，不要提交到 Git。

---

## 10. 常见问题

**Q：启动时提示端口被占用？**
程序会自动结束占用同端口的**同名进程**（只杀 `FreeModelRelay*`，不会误杀其他程序），然后重新监听。如果占用者是别的程序，用 `-port` 换一个。

**Q：一个账号都没有，能用吗？**
能。`opencode-zen` 是匿名免费通道，通常开箱即用。其余来源需要各自账号。

**Q：付费模型一定要充钱吗？**
不一定。WorkBuddy（中国版/国际版）和 Qoder 的付费模型按**积分**扣费，而积分可以每天在后台**签到免费领**——仪表盘点一次「🎁 一键签到」就行，不需要充值。注意签到补的是每日基础量，高频调用仍会耗尽，第二天再签；Cline 的付费模型走的是账号本身额度，没有签到机制。详见 [3.7](#37-用签到积分免费调用付费模型)。

**Q：`/v1/models` 里有模型，但调用报 400？**
可能是 zen 的付费模型——它们出现在上游目录里但不属于免费集合，本代理会明确拒绝而不是偷偷扣费。换 `· free` 结尾的模型。

**Q：某个模型在后台能看到，客户端列表里却没有？**
后台显示全量，`/v1/models` 只列当前确认可用的。WorkBuddy 的模型要经过可用性探测，探测失败的不会对外暴露。

**Q：模型列表里的图片/视频列是 `?`，是不支持吗？**
不是。`?` 表示上游目录没有提供这个信息。点「探测」用真实请求确认。

**Q：客户端报 401 `invalid API key`？**
后台生成过 Key，但客户端没带或者带错了。两种头都支持：`x-api-key` 或 `Authorization: Bearer`。

**Q：客户端报 404？**
九成是 Base URL 写错了。OpenAI 兼容客户端要带 `/v1`，Anthropic 客户端（Claude Code）不要带 `/v1`。

**Q：请求卡住很久才失败？**
上游响应头超时是 120 秒，流式空闲超时是 90 秒。如果上游本身不可达，会等到超时才报错。可以先 `curl /health` 确认代理活着，再看 `all-requests.log` 里有没有这条请求。

**Q：局域网里其他设备打不开后台？**
这是设计如此。后台只允许本机回环，即使传了 `-host 0.0.0.0` 也一样。代理端点 `/v1/*` 才会对外。

**Q：重启后调度策略和请求头变了？**
这两项保存在内存里，重启回到默认值。默认模型、账号、Key 都是持久化的，不受影响。

**Q：WorkBuddy 账号导入失败，提示找不到登录信息？**
桌面端必须先登录并**至少启动过一次**，登录文件才会生成。客户端运行时还会轮转这些文件，所以偶尔读不到可以稍后重试，或点「导入本机账号」。

**Q：需要科学上网吗？**
取决于上游可达性。本程序不读取系统代理，要走上游代理请在后台配置出口代理池。

**Q：能同时跑多个实例吗？**
不建议。数据目录是相对路径，同一目录跑两个实例会互相覆盖配置。

---

## 11. 已知限制

- **不支持**：Embeddings、Rerank、语音、图像生成、`/v1/messages/count_tokens`。只做对话与工具调用。
- **Qoder 仅中国区**：同一个 PAT 在 global 区会返回 400。
- **免费通道不稳定**：zen 的匿名额度由上游单方面决定，收紧时可能整体不可用。
- **单机单实例**：没有多租户、没有用户体系，就是给个人或小团队本机用的。
- **管理后台无登录**：安全性完全依赖「只监听回环」这一条。请勿暴露到公网。
- **模型能力以实测为准**：模态、上下文长度等信息来自上游目录，可能滞后；以探测结果和实际调用为准。
- **上游接口随时可能变**：本项目通过复现官方客户端行为接入，上游改版后可能需要更新版本才能恢复。

---

## 12. 开发与贡献

### 12.1 环境

- Go 1.25+
- 无其他构建依赖（不需要 Node、CGO）

### 12.2 常用命令

```bash
go build ./...        # 编译
go vet ./...          # 静态检查
go test ./...         # 跑测试
gofmt -l .            # 检查格式
```

提交前请保证 `go build`、`go vet`、`go test` 全部通过。

### 12.3 改代码时的注意点

- **管理后台前端**是 `internal/app/admin_html.go` 里的一个 HTML 字符串常量，改完必须重新编译才能看到效果
- **模型路由**改动集中在 `internal/app/models.go` 与 `internal/app/provider_chat.go`，改之前先看 [TECHNICAL.md](TECHNICAL.md) 第 5 节
- **新增上游**需要同时处理三种协议入口（chat / messages / responses），漏掉一个会让请求静默落到 Cline 账号池，用错额度还不报错
- **数据文件路径**统一走 `kit.ResolveDataPath`，不要自己拼相对路径

更细的架构说明、模块职责、踩过的坑，见 **[TECHNICAL.md](TECHNICAL.md)**。

### 12.4 提 Issue 时请附上

1. 操作系统与版本
2. 代理版本（或 commit）
3. 客户端是什么、Base URL 怎么填的、模型 id 是什么
4. 复现用的 curl（脱敏后）
5. `data/all-requests.log` 里对应的几行
6. 后台 **请求日志** 页里那条请求的错误信息

### 12.5 PR

- 一个 PR 做一件事
- 涉及行为的改动请附带说明和测试
- 不要把 `data/`、日志文件、任何凭据提交上来

### 12.6 发布流程

推送到 `main` 会自动触发 [.github/workflows/build.yml](.github/workflows/build.yml)，无需手动打包：

1. **test**：`go vet ./...` + `go test ./...`，失败则整个流程终止
2. **build**：6 个平台矩阵并行编译（`CGO_ENABLED=0` 静态链接，`-trimpath -ldflags="-s -w"`）

   | 产物 | 平台 |
   |------|------|
   | `FreeModelRelay-windows-amd64.exe` | Windows x64 |
   | `FreeModelRelay-windows-arm64.exe` | Windows ARM64 |
   | `FreeModelRelay-linux-amd64` | Linux x64 |
   | `FreeModelRelay-linux-arm64` | Linux ARM64 |
   | `FreeModelRelay-macos-amd64` | macOS Intel |
   | `FreeModelRelay-macos-arm64` | macOS Apple Silicon |

3. **release**：汇总产物 → 自动生成下一个语义化版本号 → 创建 Release → 附 `SHA256SUMS.txt`

产物有两种拿法：

- **Releases 页面**：长期保存，推荐给用户下载
- **Actions 运行页 → Artifacts**：每次运行的临时产物，保留 7 天

手动触发：仓库 **Actions → Build Release → Run workflow**。

> 版本号从已有 tag 递增（首个是 `v0.0.1`，之后 `v0.0.2`、`v0.0.3`…）。同一个 commit 重跑会复用已有 tag，不会重复涨版本。想指定版本号，先手动 `git tag v1.0.0 && git push --tags` 再触发。

想在本地产出同样的 6 个文件，用 `.\build.ps1 -All`（产物落 `dist/`），命令与 CI 一致。

---

## 13. 项目结构

```
├── main.go                     CLI 入口
├── build.ps1                   本地构建脚本（-All 交叉编译 6 平台）
├── go.mod                      Go 模块定义
├── README.md                   本文档
├── TECHNICAL.md                架构与维护文档
├── internal/
│   ├── app/                    核心业务
│   │   ├── proxy.go            路由注册 / 鉴权 / 模型路由 / SSE 转发 / Cline 上游
│   │   ├── responses.go        OpenAI Responses ↔ Chat Completions 转换
│   │   ├── pool.go             Cline 账号池：选号 / 冷却 / 刷新 / 持久化
│   │   ├── models.go           模型缓存 / 目录同步 / 默认模型 / 路由判定
│   │   ├── zen.go              opencode zen 上游：指纹 / 退避 / 故障转移
│   │   ├── catalog.go          zen 官方模型目录（免费集合的权威来源）
│   │   ├── compact.go          上下文超限自动压缩
│   │   ├── rtk.go              工具输出预压缩
│   │   ├── errors.go           上游错误透传
│   │   ├── proxy_pool.go       出口代理池 + TLS 指纹
│   │   ├── stats.go            zen 请求统计
│   │   ├── logs.go             请求日志中间件 + 诊断日志
│   │   ├── workbuddy*.go       WorkBuddy 双变体：凭据解密 / 模型 / 转发 / 探测
│   │   ├── qoder.go            Qoder：签名 / 模型 / 转发 / 账号
│   │   ├── provider_chat.go    第三方 provider 路由 / id 装饰 / 协议适配
│   │   ├── provider_sse.go     provider 侧 SSE 中继 / 聚合 / 错误信封
│   │   ├── provider_credits.go 积分查询与签到
│   │   ├── modality.go         模态三态 + 实调探测
│   │   ├── admin*.go           管理后台 API 与前端
│   │   └── types.go            数据结构
│   ├── cline/auth.go           WorkOS 设备授权 + token 刷新
│   └── kit/                    工具：data 路径 / 身份构造 / HTTP 客户端
├── data/                       运行时数据（gitignore）
└── .github/workflows/          CI：6 平台构建 + 自动发布
```

---

## 14. 许可证

[MIT](LICENSE)

---

<sub>本项目仅用于个人学习与自用，与文中提到的任何上游厂商无隶属关系，也未获得其授权或背书。请自行评估使用风险。</sub>
