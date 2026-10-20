package app

const adminHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>FreeModelRelay 管理面板</title>
<style>
:root{
  --bg:#0b0e17;--bg2:#111527;--bg3:#1a2038;--panel:rgba(148,163,184,.055);
  --border:rgba(148,163,184,.14);--border-strong:rgba(148,163,184,.28);
  --text:#e6edf6;--text2:#8b98b4;--text3:#5b6b89;
  --accent:#22d3ee;--accent2:#34d399;--amber:#f59e0b;--danger:#f87171;
  --accent-grad:linear-gradient(135deg,#22d3ee,#34d399);
  --glow:0 0 0 1px rgba(34,211,238,.25),0 0 24px rgba(34,211,238,.12);
  --status-active-bg:rgba(52,211,153,.14);--status-cooldown-bg:rgba(245,158,11,.14);
  --status-expired-bg:rgba(248,113,113,.14);
  --btn-primary-bg:linear-gradient(135deg,#0ea5e9,#22d3ee);--btn-primary-hover:linear-gradient(135deg,#0284c7,#0ea5e9);
  --btn-success-bg:linear-gradient(135deg,#059669,#34d399);--btn-success-hover:linear-gradient(135deg,#047857,#059669);
  --radius:14px;--radius-sm:9px;
}
[data-theme="light"]{
  --bg:#f3f5fa;--bg2:#ffffff;--bg3:#eef1f7;--panel:rgba(255,255,255,.7);
  --border:rgba(15,23,42,.12);--border-strong:rgba(15,23,42,.26);
  --text:#0f172a;--text2:#57617a;--text3:#8a94ab;
  --accent:#0891b2;--accent2:#059669;--amber:#b45309;--danger:#dc2626;
  --accent-grad:linear-gradient(135deg,#0891b2,#059669);
  --glow:0 0 0 1px rgba(8,145,178,.22),0 6px 24px rgba(8,145,178,.10);
  --status-active-bg:rgba(5,150,105,.12);--status-cooldown-bg:rgba(180,83,9,.12);
  --status-expired-bg:rgba(220,38,38,.10);
  --btn-primary-bg:linear-gradient(135deg,#0284c7,#06b6d4);--btn-primary-hover:linear-gradient(135deg,#0369a1,#0284c7);
  --btn-success-bg:linear-gradient(135deg,#059669,#10b981);--btn-success-hover:linear-gradient(135deg,#047857,#059669);
}
*{margin:0;padding:0;box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{font-family:'Inter','Segoe UI','PingFang SC','Microsoft YaHei',system-ui,sans-serif;background:var(--bg);color:var(--text);font-size:14px;line-height:1.55;min-height:100vh}
body::before{content:'';position:fixed;inset:0;z-index:-1;background:
  radial-gradient(900px 500px at 85% -10%,rgba(34,211,238,.09),transparent 60%),
  radial-gradient(800px 500px at -10% 110%,rgba(52,211,153,.07),transparent 60%),
  var(--bg);pointer-events:none}
.mono,code{font-family:'JetBrains Mono','Cascadia Code','Fira Code',Consolas,monospace;font-size:12px}

/* ===== 布局 ===== */
.layout{display:flex;min-height:100vh}
.sidebar{width:236px;background:var(--panel);backdrop-filter:blur(14px);border-right:1px solid var(--border);padding:18px 10px;flex-shrink:0;display:flex;flex-direction:column;position:sticky;top:0;height:100vh;overflow-y:auto}
.sidebar h1{font-size:15px;font-weight:700;padding:2px 10px 16px;border-bottom:1px solid var(--border);margin-bottom:10px;display:flex;align-items:center;gap:8px;letter-spacing:.02em}
.sidebar h1 .logo{width:28px;height:28px;border-radius:8px;background:var(--accent-grad);display:inline-flex;align-items:center;justify-content:center;font-size:14px;color:#04121a;box-shadow:var(--glow)}
.sidebar h1 .brand-name{background:var(--accent-grad);-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent}
.sidebar h1 .theme-toggle{margin-left:auto;padding:4px 8px}
.sidebar h1 span{color:var(--accent)}
.nav-item{display:flex;align-items:center;gap:10px;padding:9px 12px;border-radius:10px;cursor:pointer;color:var(--text2);transition:.18s;font-size:13.5px;margin-bottom:2px;position:relative}
.nav-item .nav-ico{width:18px;text-align:center;font-size:15px;filter:saturate(.8)}
.nav-item:hover{color:var(--text);background:rgba(148,163,184,.09)}
.nav-item.active{color:var(--text);background:linear-gradient(90deg,rgba(34,211,238,.16),rgba(34,211,238,.05));font-weight:600}
.nav-item.active::before{content:'';position:absolute;left:-10px;top:20%;bottom:20%;width:3px;border-radius:3px;background:var(--accent-grad);box-shadow:0 0 12px rgba(34,211,238,.6)}
.sidebar-footer{margin-top:auto;padding:12px 8px 4px;font-size:11.5px;color:var(--text3);border-top:1px solid var(--border)}
.sidebar-footer a{color:var(--accent);text-decoration:none}
.main{flex:1;padding:26px 34px 60px;min-width:0;max-width:1500px;margin:0 auto;width:100%}
h2{font-size:21px;margin-bottom:18px;font-weight:700;letter-spacing:.01em}

/* ===== 卡片 ===== */
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:14px;margin-bottom:26px}
.card{background:var(--panel);backdrop-filter:blur(10px);border:1px solid var(--border);border-radius:var(--radius);padding:18px;position:relative;overflow:hidden;transition:.2s}
.card::after{content:'';position:absolute;inset:0;background:radial-gradient(220px 80px at 85% -10%,rgba(34,211,238,.12),transparent);pointer-events:none}
.card:hover{transform:translateY(-2px);border-color:var(--border-strong);box-shadow:0 10px 30px rgba(2,6,23,.35)}
.card .num{font-size:30px;font-weight:700;font-variant-numeric:tabular-nums;letter-spacing:-.02em;color:var(--text)}
.card .label{font-size:12px;color:var(--text2);margin-top:5px;display:flex;align-items:center;gap:6px}
.card .label::before{content:'';width:7px;height:7px;border-radius:50%;background:var(--lab-c,var(--accent));box-shadow:0 0 10px var(--lab-c,var(--accent))}
.card .num.green{color:var(--accent2);--lab-c:var(--accent2)}
.card .num.red{color:var(--danger);--lab-c:var(--danger)}
.card .num.yellow{color:var(--amber);--lab-c:var(--amber)}
.card .num.blue{color:var(--accent);--lab-c:var(--accent)}
.cards .card{animation:rise .45s ease both}
.cards .card:nth-child(1){animation-delay:.02s}
.cards .card:nth-child(2){animation-delay:.08s}
.cards .card:nth-child(3){animation-delay:.14s}
.cards .card:nth-child(4){animation-delay:.2s}
.cards .card:nth-child(5){animation-delay:.26s}
.cards .card:nth-child(6){animation-delay:.32s}
@keyframes rise{from{opacity:0;transform:translateY(10px)}to{opacity:1;transform:none}}

/* ===== 区块 ===== */
.section{background:var(--panel);backdrop-filter:blur(10px);border:1px solid var(--border);border-radius:var(--radius);margin-bottom:22px;overflow:hidden;animation:rise .4s ease both}
.section-title{padding:13px 18px;border-bottom:1px solid var(--border);font-weight:600;font-size:14px;display:flex;align-items:center;gap:8px;background:rgba(148,163,184,.03)}
.section-body{padding:18px}
.tabs{display:flex;border-bottom:1px solid var(--border);padding:0 8px;gap:4px;overflow-x:auto}
.tab{padding:11px 18px;cursor:pointer;color:var(--text2);border-bottom:2px solid transparent;font-size:13px;white-space:nowrap;transition:.15s;border-radius:8px 8px 0 0}
.tab:hover{color:var(--text);background:rgba(148,163,184,.07)}
.tab.active{color:var(--accent);border-bottom-color:var(--accent);font-weight:600}
.tab-content{display:none;padding:18px}
.tab-content.active{display:block;animation:rise .25s ease both}
.subtab{display:none}
.subtab.active{display:block;animation:rise .25s ease both}

/* ===== 表格 ===== */
.table-wrap{overflow-x:auto}
table{width:100%;border-collapse:collapse}
th,td{text-align:left;padding:10px 14px;border-bottom:1px solid var(--border);font-size:13px;white-space:nowrap}
th{color:var(--text2);font-weight:600;font-size:11.5px;text-transform:uppercase;letter-spacing:.06em}
tbody tr{transition:.12s}
tbody tr:hover{background:rgba(148,163,184,.06)}
tbody tr:last-child td{border-bottom:none}

/* ===== 状态徽章 ===== */
.status{display:inline-flex;align-items:center;gap:6px;padding:3px 10px;border-radius:999px;font-size:12px;font-weight:600}
.status.active{background:var(--status-active-bg);color:var(--accent2)}
.status.cooldown{background:var(--status-cooldown-bg);color:var(--amber)}
.status.expired{background:var(--status-expired-bg);color:var(--danger)}
.status-dot{width:7px;height:7px;border-radius:50%;display:inline-block}
.status-dot.active{background:var(--accent2);box-shadow:0 0 8px var(--accent2)}
.status-dot.cooldown{background:var(--amber);box-shadow:0 0 8px var(--amber)}
.status-dot.expired{background:var(--danger);box-shadow:0 0 8px var(--danger)}

/* ===== 按钮 ===== */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:6px;padding:7px 15px;border:1px solid var(--border);border-radius:var(--radius-sm);background:rgba(148,163,184,.08);color:var(--text);cursor:pointer;font-size:13px;transition:.18s;text-decoration:none;font-family:inherit;white-space:nowrap}
.btn:hover{background:rgba(148,163,184,.16);border-color:var(--border-strong);transform:translateY(-1px)}
.btn:active{transform:none}
.btn-primary{background:var(--btn-primary-bg);border-color:transparent;color:#04121a;font-weight:600;box-shadow:0 4px 14px rgba(14,165,233,.28)}
.btn-primary:hover{background:var(--btn-primary-hover);box-shadow:0 6px 18px rgba(14,165,233,.38)}
.btn-success{background:var(--btn-success-bg);border-color:transparent;color:#04231a;font-weight:600;box-shadow:0 4px 14px rgba(5,150,105,.28)}
.btn-success:hover{background:var(--btn-success-hover)}
.btn-danger{border-color:rgba(248,113,113,.4);color:var(--danger);background:transparent}
.btn-danger:hover{background:rgba(248,113,113,.12);border-color:var(--danger)}
.btn-sm{padding:3px 10px;font-size:12px;border-radius:7px}

/* ===== 表单 ===== */
input,textarea,select{width:100%;padding:9px 13px;background:rgba(2,6,23,.4);border:1px solid var(--border);border-radius:var(--radius-sm);color:var(--text);font-size:13px;font-family:inherit;transition:.15s}
[data-theme="light"] input,[data-theme="light"] textarea,[data-theme="light"] select{background:rgba(15,23,42,.03)}
input::placeholder,textarea::placeholder{color:var(--text3)}
input:focus,textarea:focus,select:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(34,211,238,.15)}
textarea{resize:vertical;min-height:84px;font-family:'JetBrains Mono','Cascadia Code',Consolas,monospace;font-size:12px}
select{cursor:pointer;appearance:none;background-image:linear-gradient(45deg,transparent 50%,var(--text2) 50%),linear-gradient(135deg,var(--text2) 50%,transparent 50%);background-position:calc(100% - 18px) 55%,calc(100% - 13px) 55%;background-size:5px 5px;background-repeat:no-repeat;padding-right:32px}
.form-row{display:flex;gap:14px;align-items:flex-end;margin-bottom:14px;flex-wrap:wrap}
.form-row .field{flex:1;min-width:180px}
.form-row .field label{display:block;font-size:12px;color:var(--text2);margin-bottom:6px;font-weight:500}
.form-actions{display:flex;gap:10px;margin-top:14px;flex-wrap:wrap}
.flex{display:flex;align-items:center;gap:8px}
.gap-4{gap:4px}
.text-right{text-align:right}
.mt-8{margin-top:8px}
.inline-flex{display:inline-flex;align-items:center;gap:6px}
.justify-between{display:flex;justify-content:space-between;align-items:center;gap:10px;flex-wrap:wrap}
.hint{font-size:12px;color:var(--text2);margin-top:8px;line-height:1.6}
.hint strong{color:var(--text)}

/* ===== Toast ===== */
.toast{position:fixed;top:22px;right:22px;padding:12px 20px;border-radius:12px;color:#fff;z-index:9999;opacity:0;transform:translateY(-12px) scale(.97);transition:.3s cubic-bezier(.2,.9,.3,1.2);font-size:13px;max-width:420px;backdrop-filter:blur(12px);border:1px solid rgba(255,255,255,.14);box-shadow:0 12px 40px rgba(2,6,23,.5);white-space:pre-line}
.toast.show{opacity:1;transform:none}
.toast.success{background:rgba(5,150,105,.92)}
.toast.error{background:rgba(220,38,38,.92)}
.toast.info{background:rgba(14,165,233,.92)}
.toast.warning{background:rgba(180,83,9,.92)}

/* ===== 杂项 ===== */
.loading{display:inline-block;width:14px;height:14px;border:2px solid var(--text3);border-top-color:var(--accent);border-radius:50%;animation:spin .7s linear infinite;vertical-align:-2px}
@keyframes spin{to{transform:rotate(360deg)}}
.empty{padding:30px;text-align:center;color:var(--text2)}
.empty-state{padding:44px 20px;text-align:center;color:var(--text2)}
.empty-state .icon{font-size:40px;margin-bottom:10px;display:block;opacity:.8}
.key-display{background:rgba(2,6,23,.45);padding:9px 13px;border-radius:var(--radius-sm);border:1px solid var(--border);font-family:'JetBrains Mono','Cascadia Code',Consolas,monospace;font-size:12px;word-break:break-all;cursor:pointer;transition:.15s}
.key-display:hover{background:rgba(34,211,238,.08);border-color:var(--accent)}
.copy-icon{cursor:pointer;color:var(--text2);padding:2px 6px;border-radius:4px}
.copy-icon:hover{color:var(--text);background:var(--bg3)}
.model-tag{display:inline-block;padding:2px 9px;border-radius:6px;font-size:11px;background:rgba(148,163,184,.1);color:var(--text2);margin:2px;letter-spacing:.02em}
.model-tag.free{border:1px solid rgba(52,211,153,.5);color:var(--accent2);background:rgba(52,211,153,.08)}
.model-tag.pass{border:1px solid rgba(245,158,11,.5);color:var(--amber);background:rgba(245,158,11,.08)}
.model-tag.pay{border:1px solid rgba(129,140,248,.5);color:#818cf8;background:rgba(129,140,248,.08)}
.theme-toggle{display:inline-flex;align-items:center;gap:5px;padding:4px 9px;border:1px solid var(--border);border-radius:8px;background:rgba(148,163,184,.08);color:var(--text2);cursor:pointer;font-size:12px;transition:.15s;font-family:inherit}
.theme-toggle:hover{color:var(--text);background:rgba(148,163,184,.16)}
[data-theme="dark"] .theme-toggle .light-label{display:none}
body:not([data-theme="dark"]) .theme-toggle .dark-label{display:none}
.probe-pill{font-size:11px;color:var(--text3)}
.oauth-card{border:1px solid var(--border);border-radius:12px;padding:16px;background:rgba(148,163,184,.05);margin-top:14px}
.stat-mini{font-family:'JetBrains Mono',Consolas,monospace;font-size:12px;color:var(--text2)}

/* ===== 响应式 ===== */
@media (max-width:980px){
  .layout{flex-direction:column}
  .sidebar{width:100%;height:auto;position:sticky;top:0;flex-direction:row;align-items:center;padding:10px 12px;overflow-x:auto;gap:4px;z-index:50}
  .sidebar h1{display:flex;align-items:center;border-bottom:none;margin:0;padding:0 6px 0 0;gap:6px;font-size:13px;white-space:nowrap}
  .sidebar h1 .brand-name{display:none}
  .sidebar .nav-item{padding:7px 11px;white-space:nowrap}
  .sidebar .nav-item.active::before{display:none}
  .sidebar .theme-toggle{margin-left:4px}
  .sidebar-footer{display:none}
  .main{padding:20px 16px 48px}
}
@media (max-width:560px){
  .cards{grid-template-columns:repeat(2,1fr)}
  h2{font-size:18px}
  .section-title{padding:11px 14px}
  .section-body{padding:14px}
  .form-row .field{min-width:100%}
}
/* 聊天 */
#chatBox{height:440px;overflow-y:auto;border:1px solid var(--border);border-radius:10px;padding:12px;background:rgba(148,163,184,.05);display:flex;flex-direction:column;gap:10px}
.bubble{max-width:86%;padding:9px 12px;border-radius:10px;white-space:pre-wrap;word-break:break-word;font-size:13.5px;line-height:1.6}
.bubble-user{align-self:flex-end;background:var(--accent);color:#fff;border-bottom-right-radius:3px}
.bubble-assistant{align-self:flex-start;background:rgba(148,163,184,.12);border:1px solid var(--border);color:var(--text);border-bottom-left-radius:3px}
.bubble-error{align-self:flex-start;background:rgba(239,68,68,.12);border:1px solid rgba(239,68,68,.4);color:#f87171}
.bubble-who{font-size:11px;opacity:.65;margin-bottom:3px}
#chatInput{flex:1 1 auto;min-width:0;resize:vertical;min-height:64px;font-family:inherit;line-height:1.5;background:rgba(148,163,184,.06);color:var(--text);border:1px solid var(--border);border-radius:10px;padding:8px 12px;font-size:13.5px}
#chatInput:focus{outline:none;border-color:var(--accent)}
#chatBox::-webkit-scrollbar{width:8px}
#chatBox::-webkit-scrollbar-thumb{background:var(--border);border-radius:4px}
/* 模型列表表格 */
.mfilter{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-bottom:12px}
.mfilter input[type=text]{flex:1 1 160px;min-width:140px;background:rgba(148,163,184,.06);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:6px 10px;font-size:12.5px}
.mfilter input[type=text]:focus{outline:none;border-color:var(--accent)}
.mfilter select{background:var(--bg3);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:6px 8px;font-size:12.5px}
.mcheck{display:flex;gap:5px;align-items:center;margin:0;cursor:pointer;white-space:nowrap}
.mtbl{width:100%;border-collapse:collapse;font-size:12.5px;table-layout:fixed}
.mtbl th{text-align:left;font-weight:500;font-size:11.5px;color:var(--text3);padding:0 8px 8px;border-bottom:1px solid var(--border);white-space:nowrap;user-select:none}
.mtbl th.sortable{cursor:pointer}
.mtbl th.sortable:hover{color:var(--text)}
.mtbl td{padding:8px;border-bottom:1px solid var(--border);vertical-align:middle;overflow:hidden}
.mtbl tbody tr:hover td{background:rgba(148,163,184,.05)}
.mtbl tbody tr.isdef td{background:rgba(34,211,238,.07)}
.mtbl .mname{font-weight:500;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.mtbl .mid{font-family:'JetBrains Mono',monospace;font-size:10.5px;color:var(--text3);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:2px}
.mtbl .mact{display:flex;gap:4px;flex-wrap:wrap}
.mtbl .tick{font-size:13px;font-weight:500}
</style></head>
<body>
<div class="layout">
<div class="sidebar">
<h1><span class="logo">⚡</span><span class="brand-name">FreeModelRelay</span><button class="theme-toggle" onclick="toggleTheme()" title="切换主题"><span class="icon" id="themeIcon">🌙</span><span class="light-label">浅色</span><span class="dark-label">深色</span></button></h1>
<div class="nav-item active" data-tab="dashboard"><span class="nav-ico">📊</span> 仪表盘</div>
<div class="nav-item" data-tab="models"><span class="nav-ico">📋</span> 全部模型</div>
<div class="nav-item" data-tab="cline"><span class="nav-ico">👤</span> Cline 账号</div>
<div class="nav-item" data-tab="opencode"><span class="nav-ico">🌐</span> opencode 免费模型</div>
<div class="nav-item" data-tab="workbuddy"><span class="nav-ico">🐧</span> WorkBuddy账号管理</div>
<div class="nav-item" data-tab="workbuddy-ai"><span class="nav-ico">🐧</span> WorkBuddy国际版</div>
<div class="nav-item" data-tab="qoder"><span class="nav-ico">🔷</span> Qoder账号管理</div>
<div class="nav-item" data-tab="trae"><span class="nav-ico">🟣</span> Trae账号管理</div>
<div class="nav-item" data-tab="loomy"><span class="nav-ico">🅾️</span> 科大讯飞</div>
<div class="nav-item" data-tab="chat"><span class="nav-ico">💬</span> 模型聊天</div>
<div class="nav-item" data-tab="egress-proxy"><span class="nav-ico">🧭</span> 网络设置</div>
<div class="nav-item" data-tab="logs"><span class="nav-ico">📜</span> 请求日志</div>
<div class="sidebar-footer">
  <div>管理面板: <a href="/admin/">/admin/</a></div>
  <div>API 地址: <span id="footerApiAddr">http://127.0.0.1:9999</span></div>
</div>
</div>

<div class="main">

<div id="tab-chat" class="tab-panel" style="display:none">
<h2>💬 模型聊天</h2>
<div class="section">
  <div class="section-body">
    <div id="chatBox"></div>
    <div style="display:flex;gap:8px;margin-top:10px;align-items:stretch">
      <select id="chatModel" title="选择模型" style="flex:0 0 220px;align-self:stretch"></select>
      <textarea id="chatInput" placeholder="输入消息，Enter 发送，Shift+Enter 换行"></textarea>
      <button class="btn btn-primary" id="chatSendBtn" onclick="sendChat()">发送</button>
      <button class="btn" onclick="clearChat()">清空</button>
    </div>
    <div class="hint" style="margin-top:6px">多轮上下文仅保存在本页；流式输出经 /v1/chat/completions 真实通道（[cline]=账号池模型，[zen]=opencode 免费模型，[workbuddy]=WorkBuddy 中国区，[workbuddy-ai]=WorkBuddy 国际版，[qoder]=Qoder，[trae]=Trae；第三方来源只列已接入的模型）。</div>
  </div>
</div>
</div>

<div id="tab-models" class="tab-panel" style="display:none">
<h2>📋 全部模型</h2>
<div class="section">
  <div class="section-body">
    <div style="display:flex;gap:14px;margin-bottom:10px;flex-wrap:wrap;align-items:center">
      <span class="hint" style="margin:0">当前默认模型: <strong id="curDefModel" style="color:var(--accent)">-</strong></span>
      <span class="hint" style="margin:0">客户端未传模型或传未知模型时，代理将使用默认模型</span>
      <span class="hint" style="margin:0;margin-left:auto" id="modelCounts"></span>
      <button class="btn btn-sm" onclick="loadAllModels()">🔄 刷新</button>
    </div>
    <div class="mfilter">
      <input type="text" id="mFilterText" placeholder="搜索模型名 / 路由 id" oninput="renderModelTable()">
      <select id="mFilterGroup" onchange="renderModelTable()">
        <option value="">全部分组</option>
        <option value="cline-free">cline-free</option>
        <option value="opencode-zen">opencode-zen</option>
        <option value="workbuddy">workbuddy</option>
        <option value="workbuddy-ai">workbuddy-ai</option>
        <option value="qoder">qoder</option>
        <option value="trae">trae</option>
      </select>
      <label class="hint mcheck"><input type="checkbox" id="mFilterFree" onchange="renderModelTable()">仅看免费</label>
      <label class="hint mcheck"><input type="checkbox" id="mFilterImage" onchange="renderModelTable()">仅看支持图片</label>
      <label class="hint mcheck"><input type="checkbox" id="mFilterAvail" onchange="renderModelTable()">仅看可用</label>
    </div>
    <div id="allModelsList">加载中...</div>
  </div>
</div>
</div>

<div id="tab-dashboard" class="tab-panel">
<h2>📊 仪表盘</h2>
<div class="section">
  <div class="section-title">🤖 各模型来源模型数
    <button class="btn btn-sm" onclick="loadModelStats()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body">
    <div class="cards" id="modelStatCards" style="margin-bottom:0"></div>
  </div>
</div>
<div class="section">
  <div class="section-title">💎 积分与签到（WorkBuddy / 国际版 / Qoder / Trae）
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
    <button class="btn btn-sm btn-primary" onclick="doCheckinAll()" style="margin-left:6px" title="对当前有签到活动的来源依次签到，已签到的会跳过">🎁 一键签到</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="dashCreditsBox" style="padding:12px">加载中...</div></div>
</div>
<div class="section">
  <div class="section-title">📡 调用路径与使用方法</div>
  <div class="section-body" style="font-size:13px;line-height:1.7">
    <div class="hint" style="margin-bottom:10px">所有路径均可直接使用（鉴权关闭时 API Key 随意填写；已在「设置」生成密钥则需带 <code class="mono">Authorization: Bearer &lt;key&gt;</code>）。模型列表见「全部模型」页；客户端传未知模型名时自动回退到默认模型。</div>
    <table style="width:100%;font-size:12px;margin-bottom:12px">
      <thead><tr><th style="width:35%">Base URL</th><th>协议 / 适用客户端</th></tr></thead>
      <tbody>
        <tr><td class="mono">http://127.0.0.1:9999/v1</td><td>OpenAI / Anthropic / Responses 通用（推荐）</td></tr>
        <tr><td class="mono">http://127.0.0.1:9999/openai/v1</td><td>OpenAI 协议（兼容 opencode-free-gate）</td></tr>
        <tr><td class="mono">http://127.0.0.1:9999/anthropic</td><td>Claude Code 等 Anthropic 客户端（SDK 自动拼 /v1/messages）</td></tr>
        <tr><td class="mono">http://127.0.0.1:9999/codex/v1</td><td>codex / Responses 协议</td></tr>
      </tbody>
    </table>
    <div class="hint" style="margin-bottom:4px">OpenAI 协议调用示例：</div>
<pre class="mono" style="font-size:11.5px;background:rgba(148,163,184,.08);border:1px solid var(--border);border-radius:8px;padding:10px;overflow:auto;white-space:pre-wrap;word-break:break-word;margin:0 0 12px">curl http://127.0.0.1:9999/v1/chat/completions -H "Content-Type: application/json" -d "{\"model\":\"deepseek/deepseek-v4-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}"</pre>
    <div class="hint" style="margin-bottom:4px">Claude Code 配置（PowerShell）：</div>
<pre class="mono" style="font-size:11.5px;background:rgba(148,163,184,.08);border:1px solid var(--border);border-radius:8px;padding:10px;overflow:auto;white-space:pre-wrap;word-break:break-word;margin:0">$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:9999/anthropic"
$env:ANTHROPIC_AUTH_TOKEN = "anything"   # 未启用鉴权时随意
$env:ANTHROPIC_MODEL = "deepseek/deepseek-v4-flash"</pre>
  </div>
</div>
<div class="section">
  <div class="section-title">🔑 API 密钥管理</div>
  <div class="section-body">
    <p class="hint">生成的密钥可用于客户端访问代理 API（作为 x-api-key 或 Authorization 头）。</p>
    <div class="form-actions" style="margin-bottom:14px">
      <button class="btn btn-success" onclick="generateKey()">➕ 生成新密钥</button>
    </div>
    <div id="keysList"></div>
    <div id="keyGenResult" style="margin-top:8px"></div>
  </div>
</div>
</div>

<div id="tab-cline" class="tab-panel" style="display:none">
<h2>👤 Cline 账号</h2>
<div class="tabs" id="clineTabs" style="margin-bottom:20px">
  <div class="tab active" data-sub="accounts">👤 账号管理</div>
  <div class="tab" data-sub="import">📥 导入账号</div>
  <div class="tab" data-sub="settings">⚙️ 设置</div>
</div>

<div id="cline-accounts" class="subtab active">
  <div class="flex justify-between" style="margin-bottom:16px">
  <h2 style="font-size:17px">账号列表</h2>
  <div style="display:flex;gap:8px">
    <button class="btn btn-sm" onclick="exportAccounts()">📤 导出账号</button>
    <button class="btn btn-primary btn-sm" onclick="switchClineSub('import')">➕ 添加</button>
    <button class="btn btn-sm" onclick="loadAccounts()">🔄 刷新</button>
  </div>
</div>
<div class="hint" style="margin:-4px 0 16px;padding:11px 14px;border:1px solid var(--border);border-radius:10px;background:rgba(148,163,184,.05)">
  ℹ️ <strong style="color:var(--text)">Tokens</strong>为本代理本地统计（输入+输出，上游返回 usage 时精确，否则按请求体估算），用于估算离官方限流还有多远；⚡ 测试按钮发起真实探测请求；↻ 重置按钮会<strong style="color:var(--text)">探测上游限流状态</strong>：若上游仍限流则保持冷却并提示恢复时间，探测通过才解除冷却并重置今日统计。
</div>
<div class="section">
  <div class="section-body" style="padding:6px">
    <div class="table-wrap">
    <table>
      <thead>
        <tr><th>邮箱</th><th>状态</th><th title="本代理本地统计，不代表官方免费额度">今日/累计 Tokens</th><th>最后使用</th><th>创建时间</th><th>操作</th></tr>
      </thead>
      <tbody id="accountTableBody">
        <tr><td colspan="6" class="empty">加载中...</td></tr>
      </tbody>
    </table>
    </div>
  </div>
</div>
</div>

<div id="cline-import" class="subtab">
<div class="section">
  <div class="tabs" id="importTabs">
    <div class="tab active" data-tab="oauth">🔑 OAuth 浏览器登录</div>
    <div class="tab" data-tab="token">✏️ 手动输入 Token</div>
    <div class="tab" data-tab="batch">📦 批量导入</div>
  </div>

  <div id="import-oauth" class="tab-content active">
    <p class="hint">通过浏览器完成 OAuth 认证，支持 Google/GitHub/邮箱登录，自动获取 refreshToken。</p>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="startOAuth()" id="oauthBtn">🚀 开始 OAuth 登录</button>
    </div>
    <div id="oauthProgress" style="display:none;margin-top:14px" class="oauth-card">
      <div style="display:flex;align-items:center;gap:14px">
        <div class="loading"></div>
        <div>
          <div style="font-weight:600" id="oauthStatus">等待浏览器授权...</div>
          <div class="hint">
            打开 <a href="#" id="oauthUrl" target="_blank" style="color:var(--accent)"></a>
            并输入代码: <strong style="color:var(--accent);font-size:15px;letter-spacing:2px" id="oauthUserCode"></strong>
          </div>
        </div>
      </div>
    </div>
    <div id="oauthResult" style="display:none;margin-top:14px"></div>
  </div>

  <div id="import-token" class="tab-content">
    <p class="hint">输入已有的 Cline refreshToken，系统会自动验证并加入池。</p>
    <div class="form-row">
      <div class="field">
        <label>Refresh Token *</label>
        <input type="text" id="tokenInput" placeholder="粘贴 refreshToken" style="font-family:'JetBrains Mono',Consolas,monospace">
      </div>
      <div class="field">
        <label>邮箱（可选，留空自动生成）</label>
        <input type="text" id="tokenEmail" placeholder="user@example.com">
      </div>
    </div>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="addByToken()">➕ 添加账号</button>
    </div>
    <div id="tokenResult" style="margin-top:8px"></div>
  </div>

  <div id="import-batch" class="tab-content">
    <p class="hint">批量导入多个账号。支持 JSON 数组或每行一个 token。</p>
    <div class="form-row">
      <div class="field">
        <label>JSON 数组格式：[{"refreshToken":"...","email":"..."}]</label>
        <textarea id="batchInput" placeholder='[{"refreshToken":"xxx","email":"u1@x.com"},{"refreshToken":"yyy","email":"u2@x.com"}]'></textarea>
      </div>
    </div>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="batchImport()">📦 导入全部</button>
      <button class="btn" onclick="document.getElementById('fileInput2').click()">📄 选择文件</button>
      <input type="file" id="fileInput2" accept=".json,.txt" style="display:none" onchange="handleFileImport(event)">
    </div>
    <div id="batchResult" style="margin-top:8px"></div>
  </div>
</div>
</div>

<div id="cline-settings" class="subtab">

<div class="section">
  <div class="section-title">🧠 可用模型 <span id="modelsProbeInfo" class="probe-pill" style="font-weight:normal"></span></div>
  <div class="section-body">
    <div class="flex" style="margin-bottom:10px;gap:10px">
      <button class="btn btn-sm btn-primary" onclick="refreshModels()">🔄 刷新模型</button>
      <button class="btn btn-sm" onclick="probeAllModels(this)">🔍 一键检测</button>
      <span class="hint" style="margin:0">自动同步上游官方免费模型（60 秒），仅显示不消耗额度的模型</span>
    </div>
    <div id="modelsList">加载中...</div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔧 代理配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>监听地址</label><input type="text" id="settingAddr" disabled></div>
      <div class="field">
        <label>默认模型</label>
        <div style="display:flex;gap:6px;align-items:center">
          <select id="settingDefModel" style="flex:1;font-family:'JetBrains Mono',Consolas,monospace"></select>
          <button class="btn btn-sm btn-primary" onclick="saveDefaultModel()">💾 保存</button>
        </div>
      </div>
    </div>
    <div class="form-row">
      <div class="field">
        <label>轮询策略</label>
        <select id="settingStrategy" onchange="updateConfig()">
          <option value="round_robin">轮询 (round_robin)</option>
          <option value="fill">填满 (fill)</option>
          <option value="random">随机 (random)</option>
        </select>
      </div>
      <div class="field"><label>引擎版本</label><input type="text" id="settingVersion" disabled></div>
    </div>
    <div class="form-row">
      <div class="field"><label>账号文件</label><input type="text" id="settingPoolPath" disabled></div>
    </div>
  </div>
</div>

<div class="section">
  <div class="section-title">📨 请求头配置（模拟 Cline CLI 发出）</div>
  <div class="section-body">
    <div class="table-wrap">
    <table>
      <thead><tr><th style="width:220px">请求头</th><th>值</th><th style="width:40px"></th></tr></thead>
      <tbody id="headersTableBody">
        <tr><td colspan="3" class="empty">加载中...</td></tr>
      </tbody>
    </table>
    </div>
    <div class="form-actions">
      <button class="btn btn-sm" onclick="addHeaderRow()">➕ 添加请求头</button>
      <button class="btn btn-sm btn-primary" onclick="saveHeaders()">💾 保存请求头</button>
    </div>
    <div class="hint">这些请求头会附加到所有转发给 Cline API 的请求中，以模拟官方客户端行为。</div>
    <div id="headerSaveResult" style="margin-top:8px"></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🗑️ 危险操作</div>
  <div class="section-body">
    <div style="display:flex;gap:10px;flex-wrap:wrap">
      <button class="btn btn-danger" onclick="deleteAllAccounts()">🗑️ 删除全部账号</button>
      <button class="btn btn-danger" onclick="deleteAllKeys()">🗑️ 删除全部密钥</button>
    </div>
  </div>
</div>
</div>
</div>

<div id="tab-logs" class="tab-panel" style="display:none">
<div class="flex justify-between" style="margin-bottom:16px">
  <h2>📜 请求日志 <span class="probe-pill" style="font-weight:normal">仅内存保留最近 100 条，重启即清空</span></h2>
  <div style="display:flex;gap:8px">
    <button class="btn btn-sm" onclick="loadLogs()">🔄 刷新</button>
    <button class="btn btn-sm" onclick="clearLogs()">🗑 清空</button>
  </div>
</div>
<div class="section">
  <div class="section-body" style="padding:6px">
    <div class="table-wrap">
    <table>
      <thead>
        <tr><th>#</th><th>时间</th><th>来源</th><th>方法</th><th>路径</th><th>模型</th><th>路由</th><th>状态</th><th>耗时</th><th>操作</th></tr>
      </thead>
      <tbody id="logsTableBody">
        <tr><td colspan="10" class="empty">加载中...</td></tr>
      </tbody>
    </table>
    </div>
  </div>
</div>
</div>

<div id="tab-egress-proxy" class="tab-panel" style="display:none">
<h2>🧭 网络设置（统一出口）</h2>

<div class="section">
  <div class="section-title">📡 生效状态</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>当前出口</label><span id="epEffective" class="hint" style="margin:0;align-self:center">-</span></div>
      <div class="field"><label>来源</label><span id="epSource" class="hint" style="margin:0;align-self:center">-</span></div>
    </div>
    <div class="form-row">
      <div class="field"><label>系统代理检测</label><span id="epSystem" class="hint" style="margin:0;align-self:center">-</span></div>
    </div>
    <div class="hint" style="margin-top:4px">优先级：手动指定 &gt; Windows 系统代理 &gt; HTTP_PROXY / HTTPS_PROXY 环境变量 &gt; 直连。本机回环地址（127.0.0.1 / ::1）永远直连。opencode zen 页的「代理列表」是 zen 通道专用的多出口轮询池，优先级高于本页设置。</div>
  </div>
</div>

<div class="section">
  <div class="section-title">⚙️ 代理设置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field">
        <label>跟随 Windows 系统代理</label>
        <select id="epFollowSystem"><option value="true">开启（推荐）</option><option value="false">关闭</option></select>
      </div>
      <div class="field">
        <label>手动代理地址（可选，优先级最高）</label>
        <input type="text" id="epManual" placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080（留空=不手动指定）">
      </div>
    </div>
    <div class="hint" style="margin-bottom:8px">v2rayN 等软件「自动配置系统代理」写入的就是系统代理，开启上面的开关即可自动跟随；开启 TUN 模式时本程序无须任何配置也会走 TUN 出口。</div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveEgressProxyConfig()">💾 保存代理设置</button></div>
  </div>
</div>
</div>

<div id="tab-opencode" class="tab-panel" style="display:none">
<h2>🌐 opencode 免费模型（统一网关）</h2>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用 opencode 上游</label>
        <select id="ocEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>API Key</label><input type="text" id="ocKey" placeholder="public"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>Base URL</label><input type="text" id="ocBaseURL" placeholder="https://opencode.ai/zen/v1"></div>
      <div class="field"><label>代理策略</label>
        <select id="ocStrategy"><option value="round_robin">轮询 round_robin</option><option value="random">随机 random</option><option value="fill">固定 fill</option></select>
      </div>
    </div>
    <div class="form-row">
      <div class="field"><label>代理列表</label>
        <textarea id="ocProxies" rows="3" placeholder="每行一个: http://user:pass@host:port 或 socks5://host:port"></textarea>
      </div>
    </div>
    <div class="form-row">
      <div class="field"><label>代理冷却</label><span id="ocCooldownInfo" class="hint" style="margin:0;align-self:center">-</span></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveOcConfig()">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🛡️ 限流防御</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>最大并发</label><input type="text" id="ocMaxConc" placeholder="8"></div>
      <div class="field"><label>限流重试</label><input type="text" id="ocRetries" placeholder="3"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>故障转移</label>
        <select id="ocFailover"><option value="true">开启(切 cline 池)</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>失败阈值</label><input type="text" id="ocFailoverCount" placeholder="3"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>转移窗口(分钟)</label><input type="text" id="ocFailoverMinutes" placeholder="5"></div>
      <div class="field"><label>当前状态</label><span id="ocFailoverInfo" class="stat-mini" style="align-self:center">-</span></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveOcConfig()">💾 保存限流配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🗜️ 上下文压缩（opencode 官方机制）</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>自动压缩</label>
        <select id="ocCompactAuto"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>预留缓冲</label><input type="text" id="ocCompactBuffer" placeholder="20000"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>尾部保留</label><input type="text" id="ocKeepTokens" placeholder="8000"></div>
      <div class="field"><label>摘要模型</label><input type="text" id="ocSummaryModel" placeholder="留空=同请求模型"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>摘要上限</label><input type="text" id="ocMaxSummary" placeholder="4096"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveOcConfig()">💾 保存压缩配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">✂️ RTK 工具输出压缩（实验性，默认关闭）</div>
  <div class="section-body">
    <div style="padding:8px 12px;color:var(--muted,#888);font-size:12px">发往免费通道前，对超长工具输出按安全类型截断/过滤，节省输入 token。仅处理文件列表/目录树/git status 等无歧义类型，截断处带显式标记；level 2 额外截断超长 git diff。</div>
    <div class="form-row">
      <div class="field"><label>压缩级别</label>
        <select id="rtkLevel">
          <option value="0">0 - 关闭</option>
          <option value="1">1 - 安全类型（文件列表/git status）</option>
          <option value="2">2 - 安全类型 + git diff 截断</option>
        </select>
      </div>
      <div class="field"><label>触发阈值（字符）</label><input type="text" id="rtkMinChars" placeholder="20000"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>保留头部行数</label><input type="text" id="rtkKeepHead" placeholder="30"></div>
      <div class="field"><label>保留尾部行数</label><input type="text" id="rtkKeepTail" placeholder="15"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>去相邻重复行</label>
        <select id="rtkGrepDedup"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>过滤噪音行（node_modules/lock 等）</label>
        <select id="rtkFilterNoise"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveOcConfig()">💾 保存 RTK 配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🧠 opencode 模型列表
    <button class="btn btn-sm" onclick="refreshOcModels()" style="margin-left:auto">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px">
    <div id="ocModelsList" style="padding:12px">加载中...</div>
  </div>
</div>

<div class="section">
  <div class="section-title">📊 opencode 统计</div>
  <div class="section-body">
    <div class="table-wrap"><div id="ocStatsBox"></div></div>
    <div id="ocModelStatsBox" style="margin-top:14px"></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🛫 机场订阅（自动注入 zen 代理池）</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用订阅</label>
        <select id="subEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>自动刷新间隔（分钟）</label><input type="text" id="subInterval" placeholder="360"></div>
    </div>
    <div class="form-row">
      <div class="field"><label>订阅地址（每行一个，自动解析 trojan / vless / hysteria2 节点）</label>
        <textarea id="subUrls" rows="4" placeholder="https://example.com/sub?token=xxx"></textarea>
      </div>
    </div>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="saveSubscriptions()">💾 保存并刷新</button>
      <button class="btn" onclick="refreshSubscriptionsNow()">🔄 仅更新订阅</button>
      <span id="subStatus" class="hint" style="align-self:center;margin-left:8px">-</span>
    </div>
    <div class="hint" style="margin-top:4px">节点解析后自动在本机 11200 起的空闲端口开启本地 socks 出口，并入 zen 通道轮询池（对抗 IP 限流），坏节点自动冷却跳过；不影响统一出口代理与其他通道。</div>
  </div>
</div>

<div class="section">
  <div class="section-title">📡 订阅节点</div>
  <div class="section-body">
    <div id="subNodesBox" class="hint">加载中…</div>
  </div>
</div>
</div>

<!-- ============ WorkBuddy (CN) ============ -->
<div id="tab-workbuddy" class="tab-panel" style="display:none">
<h2>🐧 WorkBuddy 账号管理（中国区）</h2>

<div class="section">
  <div class="section-title">💎 积分与签到
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="wbCreditsBox" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🔍 本机凭据</div>
  <div class="section-body">
    <div class="table-wrap"><div id="wbInfo"></div></div>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="importWB()">📥 从本机导入账号</button>
      <button class="btn" onclick="refreshWB('workbuddy')">↻ 刷新令牌</button>
    </div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用 WorkBuddy 上游</label>
        <select id="wbEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>失败重试次数</label><input type="text" id="wbMaxRetries" placeholder="2"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveWBConfig('workbuddy')">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">👤 账号列表</div>
  <div class="section-body" style="padding:6px"><div id="wbAccountsList" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🧠 模型列表（全部）
    <span id="wbModelsProbeInfo" class="probe-pill" style="font-weight:normal"></span>
    <button class="btn btn-sm" onclick="probeWBModels('workbuddy', this)" style="margin-left:auto">🔍 一键检测可用性</button>
    <button class="btn btn-sm" onclick="refreshWBModels('workbuddy')">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="wbModelsList" style="padding:12px">加载中...</div></div>
</div>
</div>

<!-- ============ WorkBuddy (INTL) ============ -->
<div id="tab-workbuddy-ai" class="tab-panel" style="display:none">
<h2>🐧 WorkBuddy 国际版 账号管理（workbuddy.ai）</h2>

<div class="section">
  <div class="section-title">💎 积分与签到
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="wbaCreditsBox" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🔍 本机凭据</div>
  <div class="section-body">
    <div class="table-wrap"><div id="wbaInfo"></div></div>
    <div class="form-actions">
      <button class="btn btn-primary" onclick="importWB()">📥 从本机导入账号</button>
      <button class="btn" onclick="refreshWB('workbuddy-ai')">↻ 刷新令牌</button>
    </div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用 WorkBuddy 国际版上游</label>
        <select id="wbaEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>失败重试次数</label><input type="text" id="wbaMaxRetries" placeholder="2"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveWBConfig('workbuddy-ai')">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">👤 账号列表</div>
  <div class="section-body" style="padding:6px"><div id="wbaAccountsList" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🧠 模型列表（全部）
    <span id="wbaModelsProbeInfo" class="probe-pill" style="font-weight:normal"></span>
    <button class="btn btn-sm" onclick="probeWBModels('workbuddy-ai', this)" style="margin-left:auto">🔍 一键检测可用性</button>
    <button class="btn btn-sm" onclick="refreshWBModels('workbuddy-ai')">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="wbaModelsList" style="padding:12px">加载中...</div></div>
</div>
</div>

<!-- ============ Qoder ============ -->
<div id="tab-qoder" class="tab-panel" style="display:none">
<h2>🔷 Qoder 账号管理（中国区）</h2>

<div class="section">
  <div class="section-title">💎 积分与签到
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="qdCreditsBox" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🔍 上游信息</div>
  <div class="section-body"><div class="table-wrap"><div id="qdInfo"></div></div></div>
</div>

<div class="section">
  <div class="section-title">🔑 添加账号</div>
  <div class="section-body">
    <div class="hint" style="margin:0 0 12px">在 Qoder 官网「个人访问令牌」页复制 <code>pt-…</code> 开头的令牌粘贴到下方。令牌会加密保存在本机 <code>data/.qoder-accounts.json</code>，仅用于换取 jobToken 调用上游。</div>
    <div class="form-row">
      <div class="field"><label>个人访问令牌 (PAT)</label><input type="text" id="qdPat" placeholder="pt-xxxxxxxx_xxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="addQoderAccount()">＋ 添加并验证</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用 Qoder 上游</label>
        <select id="qdEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>仅列出免费模型 (price_factor = 0)</label>
        <select id="qdFreeOnly"><option value="true">是</option><option value="false">否（全部模型）</option></select>
      </div>
      <div class="field"><label>失败重试次数</label><input type="text" id="qdMaxRetries" placeholder="2"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveQoderConfig()">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">👤 账号列表</div>
  <div class="section-body" style="padding:6px"><div id="qdAccountsList" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🧠 模型列表
    <button class="btn btn-sm" onclick="refreshQoderModels()" style="margin-left:auto">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="qdModelsList" style="padding:12px">加载中...</div></div>
</div>
</div>

<!-- ============ Trae ============ -->
<div id="tab-trae" class="tab-panel" style="display:none">
<h2>🟣 Trae 账号管理（CN 区）</h2>

<div class="section">
  <div class="section-title">💎 积分与签到
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="trCreditsBox" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🔍 上游信息</div>
  <div class="section-body"><div class="table-wrap"><div id="trInfo"></div></div></div>
</div>

<div class="section">
  <div class="section-title">🔑 添加账号
    <button class="btn btn-primary" onclick="importTrae()" style="margin-left:auto">📥 从本机导入账号</button>
  </div>
  <div class="section-body">
    <div class="hint" style="margin:0 0 12px">Trae 桌面端把登录态写在
      <code>%APPDATA%\&lt;Trae CN|TRAE SOLO CN&gt;\User\globalStorage\storage.json</code>，
      加密用的是客户端里写死的静态盐值，<strong>不含机器指纹</strong>，所以点上方按钮就能直接解开导入，
      和 WorkBuddy 一样，不用去 F12 里抄 token。导入会连同<strong>设备密钥对</strong>一起取回 ——
      新版刷新接口要拿它签 DeviceProof，缺了就只能在令牌过期后重新导入。<br>
      要从别的机器搬账号，才需要手动粘贴下方 JSON（至少 <code>accessToken</code> 与 <code>deviceId</code>）。
      凭据保存在本机 <code>data/.trae-accounts.json</code>。</div>
    <div class="form-row">
      <div class="field"><label>凭据 JSON（手动兜底）</label>
        <textarea id="trCredJson" rows="7" placeholder='{"accessToken":"eyJ...","refreshToken":"...","deviceId":"...","userId":"...","appVersion":"1.0.0"}' style="font-family:monospace;font-size:12px"></textarea>
      </div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="addTraeAccount()">＋ 添加并同步模型</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用 Trae 上游</label>
        <select id="trEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>仅列出免费模型 (credit_multiplier = 0)</label>
        <select id="trFreeOnly"><option value="true">是</option><option value="false">否（全部模型）</option></select>
      </div>
      <div class="field"><label>失败重试次数</label><input type="text" id="trMaxRetries" placeholder="2"></div>
    </div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveTraeConfig()">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">👤 账号列表</div>
  <div class="section-body" style="padding:6px"><div id="trAccountsList" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🧠 模型列表
    <button class="btn btn-sm" onclick="refreshTraeModels()" style="margin-left:auto">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="trModelsList" style="padding:12px">加载中...</div></div>
</div>
</div>

<!-- ============ Loomy (科大讯飞) ============ -->
<div id="tab-loomy" class="tab-panel" style="display:none">
<h2>🅾️ 科大讯飞 Loomy（模型网关 loomyad.xunfei.cn）</h2>

<div class="section">
  <div class="section-title">💎 点数余额
    <button class="btn btn-sm" onclick="loadCredits()" style="margin-left:auto">🔄 刷新</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="lmCreditsBox" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🔍 上游信息</div>
  <div class="section-body"><div class="table-wrap"><div id="lmInfo"></div></div></div>
</div>

<div class="section">
  <div class="section-title">🔑 添加账号</div>
  <div class="section-body">
    <div class="hint" style="margin:0 0 12px">Loomy 登录态是 14 天 session（无 refresh token），认证方式是 <code>useSessionAuth</code> ——
      session 本身就是网关的 Bearer token。保存<strong>手机号+密码</strong>即可全自动续期（到期前 3 天自动重登）；
      短信登录只需收一次验证码；也可以直接粘贴从其它部署导出的 session（无密码，过期后需手动换新）。<br>
      凭据保存在本机 <code>data/.loomy-accounts.json</code>，每个账号绑定独立设备标识。</div>
    <div class="tab-row" style="margin-bottom:10px">
      <div class="tab active" data-tab="pass" id="lmTabPass" onclick="loomySwitchAddTab('pass')">🔑 密码登录</div>
      <div class="tab" data-tab="sms" id="lmTabSms" onclick="loomySwitchAddTab('sms')">📱 短信验证码</div>
      <div class="tab" data-tab="session" id="lmTabSession" onclick="loomySwitchAddTab('session')">✏️ 导入 Session</div>
    </div>
    <div id="lmAddPass">
      <div class="form-row">
        <div class="field"><label>手机号</label><input type="text" id="lmPhone" placeholder="13800000000"></div>
        <div class="field"><label>密码</label><input type="password" id="lmPassword" placeholder="讯飞账号密码（在讯飞账号中心设置过）"></div>
        <div class="field"><label>备注名（可选）</label><input type="text" id="lmName" placeholder="如：主号"></div>
      </div>
      <div class="form-actions"><button class="btn btn-primary" onclick="addLoomyPassword()">＋ 登录并添加</button></div>
    </div>
    <div id="lmAddSms" style="display:none">
      <div class="form-row">
        <div class="field"><label>手机号</label><input type="text" id="lmSmsPhone" placeholder="13800000000"></div>
        <div class="field"><label>验证码</label><input type="text" id="lmSmsCode" placeholder="收到短信后填写"></div>
        <div class="field"><label>备注名（可选）</label><input type="text" id="lmSmsName" placeholder="如：小号"></div>
      </div>
      <div class="form-actions">
        <button class="btn" onclick="loomySmsSend()">📤 发送验证码</button>
        <button class="btn btn-primary" onclick="loomySmsVerify()">＋ 验证并添加</button>
      </div>
    </div>
    <div id="lmAddSession" style="display:none">
      <div class="form-row">
        <div class="field"><label>Session Token</label>
          <textarea id="lmSession" rows="4" placeholder="粘贴 Loomy 登录 session（其它部署的 .loomy-accounts.json 里可导出）" style="font-family:monospace;font-size:12px"></textarea>
        </div>
      </div>
      <div class="form-row">
        <div class="field"><label>备注名（可选）</label><input type="text" id="lmSessionName" placeholder="如：共享号"></div>
      </div>
      <div class="form-actions"><button class="btn btn-primary" onclick="addLoomySession()">＋ 校验并导入</button></div>
    </div>
  </div>
</div>

<div class="section">
  <div class="section-title">🔄 上游配置</div>
  <div class="section-body">
    <div class="form-row">
      <div class="field"><label>启用科大讯飞上游</label>
        <select id="lmEnabled"><option value="true">开启</option><option value="false">关闭</option></select>
      </div>
      <div class="field"><label>失败重试次数</label><input type="text" id="lmMaxRetries" placeholder="1"></div>
    </div>
    <div class="hint" style="margin:0 0 10px">402（点数不足）自动冷却 10 分钟并换号重试；401（session 失效）有密码时自动重登。
      出站走全局「网络设置」的统一代理规则。</div>
    <div class="form-actions"><button class="btn btn-primary" onclick="saveLoomyConfig()">💾 保存配置</button></div>
  </div>
</div>

<div class="section">
  <div class="section-title">👤 账号列表
    <button class="btn btn-sm" onclick="refreshLoomyAccounts()" style="margin-left:auto">🔄 全部刷新（重登+点数）</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="lmAccountsList" style="padding:12px">加载中...</div></div>
</div>

<div class="section">
  <div class="section-title">🧠 模型列表
    <button class="btn btn-sm" onclick="refreshLoomyModels()" style="margin-left:auto">🔄 手动同步</button>
  </div>
  <div class="section-body" style="padding:6px"><div id="lmModelsList" style="padding:12px">加载中...</div></div>
</div>
</div>

</div>
</div>

<div id="toast" class="toast"></div>

<div id="logModal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.55);z-index:1000;align-items:flex-start;justify-content:center;padding:40px 20px;overflow:auto">
  <div class="modal-card" style="background:var(--bg2,#0f172a);border:1px solid var(--border);border-radius:14px;max-width:880px;width:100%;padding:20px">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;gap:10px">
      <strong id="logDetailMeta" class="mono" style="font-size:12px;flex:1;word-break:break-all"></strong>
      <button class="btn btn-sm" onclick="closeLogDetail()">关闭</button>
    </div>
    <div class="hint" style="margin:8px 0 4px">请求内容</div>
    <pre id="logDetailReq" style="max-height:300px;overflow:auto;white-space:pre-wrap;word-break:break-word;background:rgba(148,163,184,.08);border:1px solid var(--border);border-radius:8px;padding:10px;font-size:12px;margin:0"></pre>
    <div class="hint" style="margin:12px 0 4px">回复内容</div>
    <pre id="logDetailResp" style="max-height:300px;overflow:auto;white-space:pre-wrap;word-break:break-word;background:rgba(148,163,184,.08);border:1px solid var(--border);border-radius:8px;padding:10px;font-size:12px;margin:0"></pre>
  </div>
</div>

<script>
const API = '/admin/api';

// ========== 主题切换 ==========
function getTheme() {
  return localStorage.getItem('theme') || 'dark';
}
function applyTheme(t) {
  if (t === 'dark') {
    document.documentElement.setAttribute('data-theme', 'dark');
    const ic = document.getElementById('themeIcon');
    if (ic) ic.textContent = '🌙';
  } else {
    document.documentElement.setAttribute('data-theme', 'light');
    const ic = document.getElementById('themeIcon');
    if (ic) ic.textContent = '☀️';
  }
}
function toggleTheme() {
  const cur = getTheme();
  const next = cur === 'dark' ? 'light' : 'dark';
  localStorage.setItem('theme', next);
  applyTheme(next);
}
applyTheme(getTheme());

const _ = id => document.getElementById(id);
const esc = s => { const d=document.createElement('div'); d.textContent=s||''; return d.innerHTML; };
const fmtNum = n => (n || 0).toLocaleString('zh-CN');
const fmtTokens = n => {
  n = n || 0;
  if (n >= 1000000) return (n / 1000000).toFixed(2).replace(/\.?0+$/, '') + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  return String(n);
};
if (window.location.host) _('footerApiAddr').textContent = 'http://' + window.location.host;

function toast(msg, t, duration) {
  const el = _('toast');
  el.textContent = msg;
  el.style.whiteSpace = 'pre-line';
  el.className = 'toast ' + (t || 'info') + ' show';
  clearTimeout(el._timer);
  el._timer = setTimeout(() => el.classList.remove('show'), duration || 3500);
}

// ========== 导航 ==========
document.querySelectorAll('.nav-item').forEach(el => {
  el.addEventListener('click', () => {
    if (el.classList.contains('active')) return;
    document.querySelectorAll('.nav-item').forEach(e => e.classList.remove('active'));
    el.classList.add('active');
    document.querySelectorAll('.tab-panel').forEach(e => e.style.display = 'none');
    _('tab-' + el.dataset.tab).style.display = 'block';
    if (el.dataset.tab === 'dashboard') { loadStats(); loadModelStats(); loadAccounts(); loadKeys(); loadCredits(); }
    if (el.dataset.tab === 'cline') { loadAccounts(); loadModels(); loadConfig(); }
    if (el.dataset.tab === 'logs') loadLogs();
    if (el.dataset.tab === 'opencode') { loadOcConfig(); loadOcModels(); loadOcStats(); loadSubscriptions(); }
    if (el.dataset.tab === 'workbuddy') { loadProviders(); loadWBConfig('workbuddy'); loadCredits(); }
    if (el.dataset.tab === 'workbuddy-ai') { loadProviders(); loadWBConfig('workbuddy-ai'); loadCredits(); }
    if (el.dataset.tab === 'qoder') { loadProviders(); loadQoderConfig(); loadCredits(); }
    if (el.dataset.tab === 'trae') { loadProviders(); loadTraeConfig(); loadCredits(); }
    if (el.dataset.tab === 'loomy') { loadProviders(); loadLoomyConfig(); loadCredits(); }
    if (el.dataset.tab === 'chat') loadChatModels();
    if (el.dataset.tab === 'models') loadAllModels();
    if (el.dataset.tab === 'egress-proxy') { loadEgressProxyConfig(); loadSubscriptions(); }
  });
});

function switchTab(name) {
  document.querySelectorAll('.nav-item').forEach(e => {
    e.classList.toggle('active', e.dataset.tab === name);
  });
  document.querySelectorAll('.tab-panel').forEach(e => e.style.display = 'none');
  _('tab-' + name).style.display = 'block';
  if (name === 'dashboard') { loadStats(); loadModelStats(); loadAccounts(); loadKeys(); loadCredits(); }
  if (name === 'cline') { loadAccounts(); loadModels(); loadConfig(); }
  if (name === 'logs') loadLogs();
  if (name === 'opencode') { loadOcConfig(); loadOcModels(); loadOcStats(); loadSubscriptions(); }
  if (name === 'workbuddy') { loadProviders(); loadWBConfig('workbuddy'); loadCredits(); }
  if (name === 'workbuddy-ai') { loadProviders(); loadWBConfig('workbuddy-ai'); loadCredits(); }
  if (name === 'qoder') { loadProviders(); loadQoderConfig(); loadCredits(); }
  if (name === 'trae') { loadProviders(); loadTraeConfig(); loadCredits(); }
  if (name === 'chat') loadChatModels();
  if (name === 'models') loadAllModels();
  if (name === 'egress-proxy') { loadEgressProxyConfig(); loadSubscriptions(); }
}

// Cline 子标签
function switchClineSub(name) {
  document.querySelectorAll('#clineTabs .tab').forEach(e => {
    e.classList.toggle('active', e.dataset.sub === name);
  });
  document.querySelectorAll('#cline-accounts,#cline-import,#cline-settings').forEach(e => e.classList.remove('active'));
  _('cline-' + name).classList.add('active');
  if (name === 'accounts') loadAccounts();
  if (name === 'settings') { loadModels(); loadConfig(); }
}
document.querySelectorAll('#clineTabs .tab').forEach(el => {
  el.addEventListener('click', () => switchClineSub(el.dataset.sub));
});

// 导入子标签
document.querySelectorAll('#importTabs .tab').forEach(el => {
  el.addEventListener('click', () => {
    document.querySelectorAll('#importTabs .tab').forEach(e => e.classList.remove('active'));
    el.classList.add('active');
    document.querySelectorAll('#import-oauth,#import-token,#import-batch').forEach(e => e.classList.remove('active'));
    _('import-' + el.dataset.tab).classList.add('active');
  });
});

// ========== API 请求 ==========
async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const res = await fetch(API + path, opts);
  const data = await res.json();
  if (!data.success && data.error) throw new Error(data.error);
  return data;
}

// ========== 仪表盘 ==========
async function loadStats() {
  try {
    const d = await api('GET', '/stats');
    const s = d.data;
    if (s.version) _('settingVersion').value = s.version;
    if (s.strategy) _('settingStrategy').value = s.strategy;
  } catch (e) { /* ignore */ }
}

// 各模型来源的模型总数 / 免费模型数, 并在有积分的来源上补充剩余积分。
let modelStatsData = null;

async function loadModelStats() {
  const box = _('modelStatCards');
  if (!box) return;
  try {
    const d = await api('GET', '/model-stats');
    modelStatsData = d.data || [];
    renderModelStatCards();
  } catch (e) {
    box.innerHTML = '<div class="empty">加载模型统计失败: ' + esc(e.message) + '</div>';
  }
}

function renderModelStatCards() {
  const box = _('modelStatCards');
  if (!box || !modelStatsData) return;
  if (!modelStatsData.length) { box.innerHTML = '<div class="empty">暂无模型来源数据</div>'; return; }
  box.innerHTML = modelStatsData.map(s => {
    // 只有带积分体系的来源(WorkBuddy / Qoder / Trae)才有余额, 其余来源不显示该行。
    const p = (creditsData && creditsData[s.key]) || null;
    let creditRow = '';
    if (p) {
      const c = p.credits;
      if (c) {
        const pct = c.total > 0 ? Math.min(100, Math.round((c.remain / c.total) * 1000) / 10) : 0;
        const col = pct > 50 ? 'var(--accent2)' : (pct > 20 ? 'var(--amber)' : 'var(--danger)');
        creditRow = '<div style="font-size:12px;color:var(--text2);margin-top:6px">剩余积分 <b style="color:' + col + '">' +
          fmtCredits(c.remain) + '</b> ' + esc(c.unit || 'credits') + '</div>';
      } else if (p.creditsError) {
        creditRow = '<div style="font-size:12px;color:var(--text3);margin-top:6px">剩余积分查询失败</div>';
      }
    }
    return '<div class="card">' +
      '<div class="label" style="font-weight:600;color:var(--text);font-size:13px"><span style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' +
        esc(s.label) + '</span></div>' +
      '<div style="font-size:12px;color:var(--text2);margin-top:8px;line-height:1.7">大模型数: <b style="color:var(--text)">' +
        esc(String(s.total)) + '</b></div>' +
      '<div style="font-size:12px;color:var(--text2);line-height:1.7">免费模型: <b style="color:var(--accent2)">' +
        esc(String(s.free)) + '</b></div>' +
      creditRow +
    '</div>';
  }).join('');
}

// ========== 账号管理 ==========
async function loadAccounts() {
  try {
    const d = await api('GET', '/accounts');
    const list = d.data.accounts;
    const tbody = _('accountTableBody');
    if (!list || list.length === 0) {
      tbody.innerHTML = '<tr><td colspan="6" class="empty">暂无账号，前往 <a href="#" onclick="switchClineSub(\'import\')" style="color:var(--accent);cursor:pointer">导入账号</a> 页添加</td></tr>';
      return;
    }
    const sn = { active: '活跃', cooldown: '冷却', expired: '已过期' };
    tbody.innerHTML = list.map(a => {
      const lu = a.lastUsed ? new Date(a.lastUsed).toLocaleString('zh-CN') : '-';
      const cr = a.createdAt ? new Date(a.createdAt).toLocaleString('zh-CN') : '-';
      // 冷却标签：展示预计恢复时间
      let statusExtra = '';
      if (a.status === 'cooldown') {
        const until = a.cooldownUntil ? new Date(a.cooldownUntil).toLocaleString('zh-CN') : '';
        statusExtra = until ? '<div style="font-size:10px;color:var(--text3);margin-top:2px">预计 ' + esc(until) + ' 恢复</div>' : '';
      }
      return '<tr>' +
        '<td>' + esc(a.email) + '</td>' +
        '<td><span class="status ' + a.status + '"><span class="status-dot ' + a.status + '"></span>' + (sn[a.status] || a.status) + '</span>' + statusExtra + '</td>' +
          '<td title="今日 ' + fmtNum(a.tokensToday) + ' / 累计 ' + fmtNum(a.tokensTotal) + ' tokens（上游返回 usage 时精确，否则为估算值）">' + fmtTokens(a.tokensToday) + ' / ' + fmtTokens(a.tokensTotal) + '</td>' +
        '<td class="mono" style="font-size:11px">' + lu + '</td>' +
        '<td class="mono" style="font-size:11px">' + cr + '</td>' +
        '<td style="white-space:nowrap">' +
          '<button class="btn btn-sm" onclick="testAccount(\'' + a.accountId + '\', this)" title="测试账号是否可用（成功会清除冷却/过期状态）">⚡</button> ' +
          '<button class="btn btn-sm" onclick="resetAccount(\'' + a.accountId + '\', this)" title="检测限流并解除：探测上游，若仍限流则保持冷却并提示恢复时间">↻</button> ' +
          '<button class="btn btn-sm btn-danger" onclick="deleteAccount(\'' + a.accountId + '\')" title="删除">✕</button>' +
        '</td></tr>';
    }).join('');
  } catch (e) { toast('加载账号失败: ' + e.message, 'error'); }
}

async function testAccount(id, btn) {
  const original = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>测试中'; }
  try {
    const d = await api('POST', '/accounts/test', { accountId: id });
    const r = d.data || {};
    const statusMap = { active: '可用', cooldown: '冷却', expired: '已失效', error: '错误' };
    const label = statusMap[r.status] || r.status;
    const prevMap = { active: '活跃', cooldown: '冷却', expired: '已过期', '': '' };
    let msg = '账号 ' + esc(r.email || '') + ' — ' + label;
    if (r.prevStatus && r.prevStatus !== r.status) msg += '（原状态: ' + (prevMap[r.prevStatus] || r.prevStatus) + '）';
    if (r.cooldownUntil) msg += '\n预计恢复: ' + esc(r.cooldownUntil);
    if (r.remaining) msg += '（剩余 ' + esc(r.remaining) + '）';
    if (r.reason) msg += '\n原因: ' + esc(r.reason);
    if (r.httpStatus) msg += '\nHTTP: ' + r.httpStatus;
    const type = r.status === 'active' ? 'success' : (r.status === 'cooldown' ? 'warning' : 'error');
    toast(msg, type, 6000);
    loadAccounts(); loadStats();
  } catch (e) {
    toast('测试失败: ' + e.message, 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = original; }
  }
}

async function deleteAccount(id) {
  if (!confirm('确定删除此账号？')) return;
  try {
    await api('POST', '/accounts/delete', { accountId: id });
    toast('账号已删除', 'success');
    loadAccounts(); loadStats();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function resetAccount(id, btn) {
  const original = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.innerHTML = '<span class="loading"></span>检测中'; }
  try {
    const d = await api('POST', '/accounts/reset', { accountId: id });
    const r = d.data || {};
    const type = d.success ? 'success' : (r.status === 'cooldown' ? 'warning' : 'error');
    let msg = d.message || '检测完成';
    if (r.remaining && r.status !== 'active') msg += '（剩余 ' + esc(r.remaining) + '）';
    toast(msg, type, 6000);
    loadAccounts(); loadStats();
  } catch (e) {
    toast('检测失败: ' + e.message, 'error');
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = original; }
  }
}

async function deleteAllAccounts() {
  if (!confirm('⚠️ 确定删除所有账号？不可撤销！')) return;
  try {
    await api('POST', '/accounts/delete-all', {});
    toast('全部账号已删除', 'success');
    loadAccounts(); loadStats();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

// ========== OAuth 登录 ==========
async function startOAuth() {
  const btn = _('oauthBtn');
  btn.disabled = true;
  btn.innerHTML = '<span class="loading"></span> 启动中...';
  _('oauthProgress').style.display = 'block';
  _('oauthResult').style.display = 'none';
  _('oauthStatus').textContent = '正在连接 WorkOS...';
  try {
    const d = await api('POST', '/oauth/start');
    const s = d.data;
    _('oauthStatus').textContent = '请在浏览器中打开链接并输入代码';
    const u = _('oauthUrl');
    u.textContent = s.verificationUri;
    u.href = s.verificationUri;
    _('oauthUserCode').textContent = s.userCode;
    const poll = setInterval(async () => {
      try {
        const r = await api('GET', '/oauth/status?sessionId=' + s.sessionId);
        if (r.data.done) {
          clearInterval(poll);
          btn.disabled = false;
          btn.innerHTML = '🚀 开始 OAuth 登录';
          if (r.data.success) {
            _('oauthProgress').style.display = 'none';
            _('oauthResult').innerHTML = '<div style="color:var(--accent2);font-weight:600;font-size:14px">✓ 账号添加成功: ' + esc(r.data.email) + '</div>';
            _('oauthResult').style.display = 'block';
            loadAccounts(); loadStats();
            toast('账号添加成功！', 'success');
          } else {
            _('oauthStatus').textContent = '失败: ' + (r.data.error || '未知错误');
            toast('OAuth 失败', 'error');
          }
        }
      } catch(e) {}
    }, 2000);
  } catch (e) {
    btn.disabled = false;
    btn.innerHTML = '🚀 开始 OAuth 登录';
    _('oauthStatus').textContent = '错误: ' + e.message;
    toast('OAuth 失败: ' + e.message, 'error');
  }
}

// ========== Token 导入 ==========
async function addByToken() {
  const token = _('tokenInput').value.trim();
  if (!token) { toast('请输入 refreshToken', 'error'); return; }
  const email = _('tokenEmail').value.trim();
  try {
    const d = await api('POST', '/accounts/add', { refreshToken: token, email: email || undefined });
    toast('账号添加成功: ' + (d.data.email || ''), 'success');
    _('tokenInput').value = '';
    _('tokenEmail').value = '';
    loadAccounts(); loadStats();
  } catch (e) { toast('添加失败: ' + e.message, 'error'); }
}

// ========== 批量导入 ==========
async function batchImport() {
  const raw = _('batchInput').value.trim();
  if (!raw) { toast('请输入账号数据', 'error'); return; }
  let tokens;
  try { tokens = JSON.parse(raw); if (!Array.isArray(tokens)) tokens = [tokens]; }
  catch { tokens = raw.split('\n').filter(t => t.trim()).map(t => ({ refreshToken: t.trim() })); }
  try {
    const d = await api('POST', '/batch-import', { tokens });
    toast(d.message || '导入完成', 'success');
    _('batchInput').value = '';
    loadAccounts(); loadStats();
  } catch (e) { toast('导入失败: ' + e.message, 'error'); }
}

async function handleFileImport(event) {
  const file = event.target.files[0];
  if (!file) return;
  const text = await file.text();
  let tokens;
  try { tokens = JSON.parse(text); if (!Array.isArray(tokens)) tokens = [tokens]; }
  catch { tokens = text.split('\n').filter(t => t.trim()).map(t => ({ refreshToken: t.trim() })); }
  try {
    const d = await api('POST', '/batch-import', { tokens });
    toast(d.message || '导入了 ' + tokens.length + ' 个账号', 'success');
    loadAccounts(); loadStats();
  } catch (e) { toast('导入失败: ' + e.message, 'error'); }
  event.target.value = '';
}

// ========== API 密钥管理 ==========
async function loadKeys() {
  try {
    const d = await api('GET', '/keys');
    const keys = d.data.keys;
    const el = _('keysList');
    if (!keys || keys.length === 0) {
      el.innerHTML = '<div class="empty-state"><span class="icon">🔑</span>暂无 API 密钥</div>';
      return;
    }
    el.innerHTML = keys.map(k =>
      '<div class="flex" style="margin-bottom:8px">' +
        '<span class="key-display" style="flex:1" onclick="copyText(\'' + k + '\')" title="点击复制">' + esc(k) + '</span>' +
        '<button class="btn btn-sm btn-danger" onclick="deleteKey(\'' + k + '\')">✕</button>' +
      '</div>'
    ).join('');
  } catch (e) { _('keysList').innerHTML = '<div class="empty">加载失败</div>'; }
}

async function generateKey() {
  try {
    const d = await api('POST', '/keys/generate');
    const key = d.data.key;
    _('keyGenResult').innerHTML =
      '<div style="background:rgba(52,211,153,.08);border:1px solid rgba(52,211,153,.4);border-radius:10px;padding:12px">' +
        '<div style="color:var(--accent2);font-weight:600;margin-bottom:8px">✓ 新密钥已生成（点击复制）</div>' +
        '<div class="key-display" onclick="copyText(\'' + key + '\')">' + esc(key) + '</div>' +
      '</div>';
    loadKeys();
    toast('密钥已生成', 'success');
    setTimeout(() => _('keyGenResult').innerHTML = '', 8000);
  } catch (e) { toast('生成失败: ' + e.message, 'error'); }
}

async function deleteKey(key) {
  if (!confirm('确定删除此密钥？')) return;
  try {
    await api('POST', '/keys/delete', { key });
    toast('密钥已删除', 'success');
    loadKeys();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function deleteAllKeys() {
  if (!confirm('确定删除所有 API 密钥？')) return;
  try {
    const d = await api('GET', '/keys');
    const keys = d.data.keys || [];
    for (const k of keys) await api('POST', '/keys/delete', { key: k });
    toast('全部密钥已删除', 'success');
    loadKeys();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

function copyText(t) {
  navigator.clipboard.writeText(t).then(() => toast('已复制到剪贴板', 'success')).catch(() => {
    const ta = document.createElement('textarea');
    ta.value = t; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta);
    toast('已复制到剪贴板', 'success');
  });
}

// ========== 请求日志 ==========
const ROUTE_LABEL = {
  zen: 'opencode', cline: 'cline 池', admin: '管理', meta: '元信息', other: '其他',
  workbuddy: 'WorkBuddy', 'workbuddy-ai': 'WorkBuddy 国际版', qoder: 'Qoder', trae: 'Trae'
};
const STATUS_CLASS = s => s >= 500 ? 'color:var(--danger)' : (s >= 400 ? 'color:var(--amber)' : 'color:var(--accent2)');

let lastLogs = [];
async function loadLogs() {
  try {
    const d = await api('GET', '/logs');
    const logs = d.data.logs || [];
    lastLogs = logs;
    const tbody = _('logsTableBody');
    if (!logs.length) { tbody.innerHTML = '<tr><td colspan="10" class="empty">暂无大模型请求记录</td></tr>'; return; }
    tbody.innerHTML = logs.map((l, i) => {
      const t = l.time ? new Date(l.time).toLocaleString('zh-CN') : '-';
      const route = ROUTE_LABEL[l.route] || l.route || '-';
      const st = l.status || 0;
      return '<tr>' +
        '<td class="mono" style="font-size:11px;color:var(--muted)">' + (l.seq || '-') + '</td>' +
        '<td class="mono" style="font-size:11px">' + t + '</td>' +
        '<td class="mono" style="font-size:11px">' + esc(l.client || '-') + '</td>' +
        '<td>' + esc(l.method || '-') + '</td>' +
        '<td class="mono" style="font-size:11px">' + esc(l.path || '-') + '</td>' +
        '<td class="mono" style="font-size:12px">' + esc(l.model || '-') + '</td>' +
        '<td><span class="model-tag">' + esc(route) + '</span></td>' +
        '<td style="font-weight:600;color:' + STATUS_CLASS(st) + '">' + st + '</td>' +
        '<td class="mono" style="font-size:11px">' + (l.durationMs != null ? l.durationMs + ' ms' : '-') + '</td>' +
        '<td><button class="btn btn-sm" onclick="showLogDetail(' + i + ')">详情</button></td>' +
      '</tr>';
    }).join('');
  } catch (e) { tbody.innerHTML = '<tr><td colspan="10" class="empty">加载失败</td></tr>'; }
}

async function clearLogs() {
  try {
    await api('POST', '/logs/clear');
    toast('请求日志已清空');
    loadLogs();
  } catch (e) { toast('清空失败: ' + e.message, 'error'); }
}

function showLogDetail(i) {
  const l = lastLogs[i];
  if (!l) return;
  let req = l.reqBody || '(未捕获)';
  try { req = JSON.stringify(JSON.parse(req), null, 2); } catch (_) {}
  let resp = l.respBody || '(无响应内容)';
  try { if (resp.startsWith('{')) resp = JSON.stringify(JSON.parse(resp), null, 2); } catch (_) {}
  _('logDetailMeta').textContent = [l.time ? new Date(l.time).toLocaleString('zh-CN') : '', l.model || '-', l.path, 'HTTP ' + (l.status || 0), (l.durationMs || 0) + 'ms'].join('  ·  ');
  _('logDetailReq').textContent = req;
  _('logDetailResp').textContent = resp;
  _('logModal').style.display = 'flex';
}
function closeLogDetail() { _('logModal').style.display = 'none'; }
// 点击弹窗外的主页面区域关闭详情；弹窗内部点击不关闭(方便复制)，仍可点"关闭"按钮
document.addEventListener('DOMContentLoaded', () => {
  _('logModal').addEventListener('click', () => { closeLogDetail(); });
  const card = _('logModal').querySelector('.modal-card');
  if (card) card.addEventListener('click', (e) => { e.stopPropagation(); });
});

// ========== 导出账号 ==========
async function exportAccounts() {
  try {
    const res = await fetch(API + '/accounts/export');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = 'cline-accounts-export.json';
    document.body.appendChild(a); a.click(); document.body.removeChild(a);
    URL.revokeObjectURL(url);
    toast('账号已导出（JSON）', 'success');
  } catch (e) { toast('导出失败: ' + e.message, 'error'); }
}

// ========== 配置管理 ==========
async function updateConfig() {
  const strategy = _('settingStrategy').value;
  try {
    await api('POST', '/config/update', { strategy });
    toast('策略已更新为: ' + strategy, 'success');
  } catch (e) { toast('更新失败: ' + e.message, 'error'); }
}

function addHeaderRow() {
  const tbody = _('headersTableBody');
  const tr = document.createElement('tr');
  tr.innerHTML =
    '<td><input type="text" class="header-key" placeholder="Header-Name" style="font-size:12px;font-family:monospace"></td>' +
    '<td><input type="text" class="header-val" placeholder="value" style="font-size:12px;font-family:monospace"></td>' +
    '<td><button class="btn btn-sm btn-danger" onclick="this.closest(\'tr\').remove()">✕</button></td>';
  tbody.appendChild(tr);
}

async function saveHeaders() {
  const tbody = _('headersTableBody');
  const rows = tbody.querySelectorAll('tr');
  const headers = {};
  let hasEmpty = false;
  rows.forEach(tr => {
    const keyInput = tr.querySelector('.header-key');
    const valInput = tr.querySelector('.header-val');
    if (keyInput && valInput) {
      const k = keyInput.value.trim();
      const v = valInput.value.trim();
      if (k) { headers[k] = v; }
      else if (v) { hasEmpty = true; }
    }
  });
  if (hasEmpty) { toast('存在有值无键的行，已忽略', 'info'); }
  try {
    const d = await api('POST', '/config/update', { headers });
    toast('请求头已保存', 'success');
    _('headerSaveResult').innerHTML =
      '<div style="color:var(--accent2);font-size:12px">✓ 已保存 ' + Object.keys(d.data.headers).length + ' 个请求头</div>';
    setTimeout(() => _('headerSaveResult').innerHTML = '', 5000);
    loadConfig();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

const MODEL_STYLE = {
  active:  { label: '可用', css: 'color:var(--accent2);border:1px solid rgba(52,211,153,.5);background:rgba(52,211,153,.08)' },
  empty:   { label: '响应为空', css: 'color:var(--amber);border:1px solid rgba(245,158,11,.5);background:rgba(245,158,11,.08)' },
  pass:    { label: '需订阅', css: 'color:var(--amber);border:1px solid rgba(245,158,11,.5);background:rgba(245,158,11,.08)' },
  removed: { label: '已下架', css: 'color:var(--text3);border:1px solid var(--border)' },
  error:   { label: '异常', css: 'color:var(--danger);border:1px solid rgba(248,113,113,.5);background:rgba(248,113,113,.08)' },
  unknown: { label: '未探测', css: 'color:var(--text3);border:1px dashed var(--border-strong)' }
};
const COST_LABEL = { free: '免费', pass: '订阅', quota: '消耗额度' };

async function loadModels() {
  try {
    const d = await api('GET', '/models');
    const models = d.data.models || [];
    let info = '';
    if (d.data.lastSync) info += '· 官方清单: ' + new Date(d.data.lastSync).toLocaleTimeString('zh-CN');
    _('modelsProbeInfo').textContent = info;
    if (!models.length) { _('modelsList').innerHTML = '<div class="empty">暂无模型</div>'; return; }
    _('modelsList').innerHTML = models.map(m => {
      const st = MODEL_STYLE[m.status] || MODEL_STYLE.unknown;
      const cost = COST_LABEL[m.cost] || m.cost || '';
      const synced = m.syncedAt ? new Date(m.syncedAt).toLocaleTimeString('zh-CN') : '-';
      const u = m.usage || {};
      const fmtTok = n => n >= 1e6 ? (n/1e6).toFixed(1) + 'M' : n >= 1e3 ? (n/1e3).toFixed(1) + 'k' : (n || 0);
      const usageTip = '今日: ' + (u.todayRequests || 0) + ' 次 · 输入 ' + fmtTok(u.todayPromptTokens) + ' · 输出 ' + fmtTok(u.todayCompletionTokens) +
        '\n累计: ' + (u.totalRequests || 0) + ' 次 · 输入 ' + fmtTok(u.totalPromptTokens) + ' · 输出 ' + fmtTok(u.totalCompletionTokens);
      return '<div style="display:flex;align-items:center;gap:10px;padding:8px 12px;margin:5px 0;background:rgba(148,163,184,.06);border:1px solid var(--border);border-radius:10px;transition:.15s">' +
        '<span style="font-family:\'JetBrains Mono\',monospace;font-size:13px;flex:1">' + esc(m.id) + '</span>' +
        (m.cost === 'free' ? '<span style="font-size:11px;color:var(--accent2)">不扣费</span>' : '') +
        (cost ? '<span class="model-tag">' + esc(cost) + '</span>' : '') +
        '<span class="model-tag" style="' + st.css + '">' + st.label + '</span>' +
        '<span style="font-size:11px;color:var(--text3);text-align:right;min-width:140px" title="' + esc(usageTip) + '">' +
          (u.todayRequests || u.totalRequests ? (u.todayRequests || 0) + '/' + (u.totalRequests || 0) + ' 次 · ' + fmtTok((u.todayPromptTokens || 0) + (u.todayCompletionTokens || 0)) + '/' + fmtTok((u.totalPromptTokens || 0) + (u.totalCompletionTokens || 0)) + ' tok' : '暂无用量') +
        '</span>' +
        '<span style="font-size:11px;color:var(--text3);min-width:60px;text-align:right">' + synced + '</span>' +
        '<button class="btn btn-sm" style="min-width:52px" onclick="probeOneModel(\'' + esc(m.id) + '\', this)" title="发送一条真实探测请求验证可用性">检测</button>' +
        '</div>';
    }).join('');
  } catch (e) { _('modelsList').textContent = '加载失败'; }
}

async function refreshModels() {
  try {
    _('modelsProbeInfo').textContent = '· 同步中...';
    const d = await api('POST', '/models/refresh');
    toast(d.data.message || '同步已开始', 'info');
    setTimeout(loadModels, 3000);
  } catch (e) { toast('刷新失败: ' + e.message, 'error'); _('modelsProbeInfo').textContent = ''; }
}

async function probeOneModel(id, btn) {
  if (btn.disabled) return;
  btn.disabled = true; btn.textContent = '...';
  try {
    const d = await api('POST', '/models/probe', { model: id });
    const ok = d.data.verdict === 'ok';
    toast((ok ? '可用' : '异常') + ' · ' + d.data.ms + 'ms' + (d.data.reason ? ' · ' + d.data.reason : ''), ok ? 'success' : 'error');
  } catch (e) { toast('探测失败: ' + e.message, 'error'); }
  loadModels();
}

async function probeAllModels(btn) {
  if (btn.disabled) return;
  try {
    const d = await api('GET', '/models');
    const models = d.data.models || [];
    let ok = 0, bad = 0;
    btn.disabled = true;
    for (let i = 0; i < models.length; i++) {
      btn.textContent = '检测中 ' + (i + 1) + '/' + models.length;
      try {
        const r = await api('POST', '/models/probe', { model: models[i].id });
        if (r.data.verdict === 'ok') ok++; else bad++;
      } catch (_) { bad++; }
      loadModels();
    }
    toast('检测完成: 可用 ' + ok + ' / 异常 ' + bad, 'info');
  } catch (e) { toast('探测失败: ' + e.message, 'error'); }
  btn.textContent = '🔍 一键检测'; btn.disabled = false;
}

async function loadModelOptions() {
  try {
    const d = await api('GET', '/models');
    const models = d.data.models || [];
    const sel = _('settingDefModel');
    if (!sel) return;
    sel.innerHTML = models.map(m => {
      const st = MODEL_STYLE[m.status] || MODEL_STYLE.unknown;
      return '<option value="' + esc(m.id) + '">' + esc(m.id) + ' (' + st.label + ')</option>';
    }).join('');
    const c = await api('GET', '/config');
    if (c.data.defaultModel) sel.value = c.data.defaultModel;
    if (!sel.value && models.length) sel.value = models[0].id;
  } catch (e) { /* ignore */ }
}

async function saveDefaultModel() {
  const v = _('settingDefModel').value;
  if (!v) { toast('请选择模型', 'error'); return; }
  try {
    const d = await api('POST', '/config/update', { defaultModel: v });
    toast('默认模型已保存: ' + d.data.defaultModel, 'success');
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

// ========== 配置加载 ==========
async function loadConfig() {
  try {
    const d = await api('GET', '/config');
    const c = d.data;
    if (c.address) _('settingAddr').value = c.address;
    if (c.strategy) _('settingStrategy').value = c.strategy;
    if (c.version) _('settingVersion').value = c.version;
    if (c.poolPath) _('settingPoolPath').value = c.poolPath;
    loadModelOptions();
    if (c.headers) {
      const tbody = _('headersTableBody');
      tbody.innerHTML = Object.entries(c.headers).map(([k, v]) =>
        '<tr>' +
          '<td><input type="text" class="header-key" value="' + esc(k) + '" style="font-size:12px;font-family:monospace;width:100%"></td>' +
          '<td><input type="text" class="header-val" value="' + esc(v) + '" style="font-size:12px;font-family:monospace;width:100%"></td>' +
          '<td><button class="btn btn-sm btn-danger" onclick="this.closest(\'tr\').remove()">✕</button></td>' +
        '</tr>'
      ).join('');
    }
  } catch (e) { /* ignore */ }
}

// ========== opencode 免费模型 ==========
async function loadOcConfig() {
  try {
    const d = await api('GET', '/opencode/config');
    const c = d.data;
    _('ocEnabled').value = String(c.enabled);
    _('ocKey').value = c.key || 'public';
    _('ocBaseURL').value = c.baseURL || '';
    _('ocProxies').value = (c.proxies || []).join('\n');
    _('ocStrategy').value = c.proxyStrategy || 'round_robin';
    _('ocMaxConc').value = c.maxConcurrency || 8;
    _('ocRetries').value = c.retries || 3;
    _('ocFailover').value = String(c.failover);
    _('ocFailoverCount').value = c.failoverCount || 3;
    _('ocFailoverMinutes').value = c.failoverMinutes || 5;
    _('ocCompactAuto').value = String(c.compaction ? c.compaction.auto : true);
    _('ocCompactBuffer').value = c.compaction ? c.compaction.buffer : 20000;
    _('ocKeepTokens').value = c.compaction ? c.compaction.keepTokens : 8000;
    _('ocSummaryModel').value = c.compaction ? (c.compaction.summaryModel || '') : '';
    _('ocMaxSummary').value = c.compaction ? c.compaction.maxSummary : 4096;
    const rk = c.rtk || {};
    _('rtkLevel').value = String(rk.level || 0);
    _('rtkMinChars').value = rk.minChars || 20000;
    _('rtkKeepHead').value = rk.keepHead || 30;
    _('rtkKeepTail').value = rk.keepTail || 15;
    _('rtkGrepDedup').value = String(rk.grepDedup !== false);
    _('rtkFilterNoise').value = String(rk.filterNoise !== false);
    const rt = c.runtime || {};
    _('ocFailoverInfo').innerHTML = rt.failoverActive
      ? '<span style="color:var(--danger)">🔴 故障转移中 (opencode 不可用, 请求走 cline 池)</span>'
      : '<span style="color:var(--accent2)">🟢 正常</span>';
    const cd = rt.proxyCooldowns || {};
    const keys = Object.keys(cd);
    _('ocCooldownInfo').textContent = keys.length
      ? keys.map(k => k + ' 冷却至 ' + cd[k]).join('; ')
      : '暂无冷却中的代理';
  } catch (e) { /* ignore */ }
}

async function saveOcConfig() {
  const proxies = _('ocProxies').value.split('\n').map(s => s.trim()).filter(Boolean);
  const PROXY_RE = /^(https?|socks5h?):\/\/[^\s]+:\d+/;
  const bad = proxies.find(p => !PROXY_RE.test(p));
  if (bad) { toast('代理格式无效: ' + bad + '（需 http(s)://host:port 或 socks5://host:port）', 'error'); return; }
  const body = {
    enabled: _('ocEnabled').value === 'true',
    key: _('ocKey').value.trim(),
    baseURL: _('ocBaseURL').value.trim(),
    proxies: proxies,
    proxyStrategy: _('ocStrategy').value,
    maxConcurrency: parseInt(_('ocMaxConc').value) || 8,
    retries: parseInt(_('ocRetries').value) || 3,
    failover: _('ocFailover').value === 'true',
    failoverCount: parseInt(_('ocFailoverCount').value) || 3,
    failoverMinutes: parseInt(_('ocFailoverMinutes').value) || 5,
    compaction: {
      auto: _('ocCompactAuto').value === 'true',
      buffer: parseInt(_('ocCompactBuffer').value) || 20000,
      keepTokens: parseInt(_('ocKeepTokens').value) || 8000,
      summaryModel: _('ocSummaryModel').value.trim(),
      maxSummary: parseInt(_('ocMaxSummary').value) || 4096
    },
    rtk: {
      level: parseInt(_('rtkLevel').value) || 0,
      minChars: parseInt(_('rtkMinChars').value) || 20000,
      keepHead: parseInt(_('rtkKeepHead').value) || 30,
      keepTail: parseInt(_('rtkKeepTail').value) || 15,
      grepDedup: _('rtkGrepDedup').value === 'true',
      filterNoise: _('rtkFilterNoise').value === 'true'
    }
  };
  try {
    const d = await api('POST', '/opencode/config/update', body);
    toast('opencode 配置已保存', 'success');
    loadOcConfig();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

const EP_SOURCE_TEXT = {
  manual: '手动指定',
  system: 'Windows 系统代理',
  env: '环境变量 (HTTP_PROXY / HTTPS_PROXY)',
  direct: '直连'
};

async function loadEgressProxyConfig() {
  try {
    const d = await api('GET', '/egress-proxy');
    const c = d.data || {};
    _('epFollowSystem').value = String(c.followSystem !== false);
    _('epManual').value = c.manual || '';
    _('epEffective').textContent = c.effective || '直连';
    _('epSource').textContent = EP_SOURCE_TEXT[c.source] || c.source || '-';
    _('epSystem').textContent = c.systemOn
      ? ('已启用 · ' + (c.systemProxy || '-'))
      : '未启用（将直连，除非手动指定或设置了环境变量）';
  } catch (e) {
    _('epEffective').textContent = '加载失败';
    _('epSource').textContent = '-';
    _('epSystem').textContent = '-';
  }
}

async function saveEgressProxyConfig() {
  const manual = _('epManual').value.trim();
  if (manual) {
    const PROXY_RE = /^(https?|socks5h?):\/\/[^\s]+:\d+/;
    if (!PROXY_RE.test(manual)) { toast('代理格式无效: ' + manual + '（需 http(s)://host:port 或 socks5://host:port）', 'error'); return; }
  }
  try {
    await api('POST', '/egress-proxy/update', {
      manual: manual,
      followSystem: _('epFollowSystem').value === 'true'
    });
    toast('代理设置已保存', 'success');
    loadEgressProxyConfig();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function loadSubscriptions() {
  try {
    const d = await api('GET', '/subscriptions');
    const c = d.data || {};
    _('subEnabled').value = String(c.enabled === true);
    _('subInterval').value = c.intervalMin || 360;
    _('subUrls').value = (c.urls || []).join('\n');
    const err = c.lastError ? ' · <span style="color:#d9534f">' + esc(c.lastError) + '</span>' : '';
    _('subStatus').innerHTML = '生效 ' + (c.liveCount || 0) + ' 节点 · 上次刷新: ' + (c.lastRefresh || '-') + err;
    const nodes = c.nodes || [];
    if (!nodes.length) {
      _('subNodesBox').textContent = '暂无生效节点（保存订阅地址后自动拉取解析）';
    } else {
      const rows = nodes.map(n => '<tr><td style="text-align:left">' + esc(n.name) + '</td><td>' + esc(n.protocol) + '</td><td style="text-align:left;font-family:monospace;font-size:12px">' + esc(n.server) + '</td><td>' + n.remotePort + '</td><td><b>' + n.localPort + '</b></td></tr>').join('');
      _('subNodesBox').innerHTML = '<div class="table-wrap" style="max-height:300px;overflow-y:auto"><table><thead><tr><th style="text-align:left">节点</th><th>协议</th><th style="text-align:left">服务器</th><th>远程端口</th><th>本地端口</th></tr></thead><tbody>' + rows + '</tbody></table></div><div class="hint">共 ' + nodes.length + ' 个节点，本地端口自动分配（11200 起），已并入 zen 轮询池。</div>';
    }
  } catch (e) { _('subStatus').textContent = '加载失败'; }
}

async function saveSubscriptions() {
  const urls = _('subUrls').value.split('\n').map(s => s.trim()).filter(Boolean);
  for (const u of urls) {
    if (!/^https?:\/\/.+/i.test(u)) { toast('订阅地址无效: ' + u, 'error'); return; }
  }
  const interval = parseInt(_('subInterval').value, 10) || 360;
  try {
    await api('POST', '/subscriptions/update', {
      enabled: _('subEnabled').value === 'true',
      urls: urls,
      intervalMin: interval
    });
    toast('订阅配置已保存，正在后台刷新…', 'success');
    setTimeout(loadSubscriptions, 3000);
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function refreshSubscriptionsNow() {
  _('subStatus').textContent = '正在更新订阅…';
  try {
    const d = await api('POST', '/subscriptions/refresh');
    const hasErr = d.data && d.data.lastError;
    toast(hasErr ? '订阅已更新（部分失败，详见状态）' : '订阅已更新', hasErr ? 'error' : 'success');
    loadSubscriptions();
  } catch (e) {
    toast('更新失败: ' + e.message, 'error');
    loadSubscriptions();
  }
}

async function loadOcModels() {
  try {
    const d = await api('GET', '/opencode/models');
    const models = d.data.models || [];
    const fmtTok = n => n >= 1e6 ? (n/1e6).toFixed(1) + 'M' : n >= 1e3 ? (n/1e3).toFixed(1) + 'k' : (n || 0);
    const pair = (u, k) => { if (!u) return '0 / 0'; return fmtTok(u['today'+k]) + ' / ' + fmtTok(u['total'+k]); };
    _('ocModelsList').innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型 ID</th><th>上下文</th><th>输出</th><th>来源</th><th title="今日 / 累计">请求数</th><th title="今日 / 累计">输入 Tok</th><th title="今日 / 累计">输出 Tok</th></tr></thead><tbody>' +
      models.map(m => '<tr><td style="text-align:left;font-family:monospace">' + esc(m.id) + '</td><td>' + m.context + '</td><td>' + m.output + '</td><td>' + m.source + '</td>' +
        '<td>' + pair(m.usage, 'Requests') + '</td><td>' + pair(m.usage, 'PromptTokens') + '</td><td>' + pair(m.usage, 'CompletionTokens') + '</td></tr>').join('') +
      '</tbody></table></div><div class="hint">共 ' + models.length + ' 个免费模型（每 10 分钟自动同步）· 用量列为 今日 / 累计</div>';
  } catch (e) { _('ocModelsList').textContent = '加载失败'; }
}

async function refreshOcModels() {
  try {
    const d = await api('POST', '/opencode/models/refresh');
    toast(d.message || '同步完成', 'success');
    loadOcModels();
  } catch (e) { toast('同步失败: ' + e.message, 'error'); }
}

async function loadOcStats() {
  try {
    const d = await api('GET', '/opencode/stats');
    const t = d.data.today || {}, s = d.data.total || {};
    _('ocStatsBox').innerHTML = '<table><thead><tr><th style="text-align:left"></th><th>请求数</th><th>输入 tokens</th><th>输出 tokens</th><th>压缩消耗</th><th>限流命中</th></tr></thead><tbody>' +
      '<tr><td style="text-align:left">今日</td><td>' + (t.requests || 0) + '</td><td>' + (t.promptTokens || 0) + '</td><td>' + (t.completionTokens || 0) + '</td><td>' + (t.compaction || 0) + '</td><td>' + (t.rateLimited || 0) + '</td></tr>' +
      '<tr><td style="text-align:left">累计</td><td>' + (s.requests || 0) + '</td><td>' + (s.promptTokens || 0) + '</td><td>' + (s.completionTokens || 0) + '</td><td>' + (s.compaction || 0) + '</td><td>' + (s.rateLimited || 0) + '</td></tr>' +
      '</tbody></table>';
    const bm = t.byModel || {};
    const rows = Object.keys(bm).map(k => '<tr><td style="text-align:left;font-family:monospace">' + esc(k) + '</td><td>' + bm[k].requests + '</td><td>' + bm[k].promptTokens + '</td><td>' + bm[k].completionTokens + '</td></tr>').join('');
    _('ocModelStatsBox').innerHTML = '<div style="font-size:13px;font-weight:600;margin-bottom:6px">按模型分布（今日）</div>' +
      '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型</th><th>请求数</th><th>输入 tokens</th><th>输出 tokens</th></tr></thead><tbody>' +
      (rows || '<tr><td colspan="4" style="text-align:left;color:var(--text2)">暂无数据</td></tr>') + '</tbody></table></div>';
  } catch (e) { /* ignore */ }
}

// ========== 第三方 provider（WorkBuddy / WorkBuddy 国际版 / Qoder / Trae） ==========
let providerStatus = null;

async function loadProviders() {
  try {
    const d = await api('GET', '/providers/status');
    providerStatus = d.data;
    renderWB('workbuddy', 'wb');
    renderWB('workbuddy-ai', 'wba');
    renderQoder();
    renderTrae();
  } catch (e) { toast('加载 provider 状态失败: ' + e.message, 'error'); }
}

function fmtTime(ms) {
  if (!ms) return '-';
  const d = new Date(ms);
  return isNaN(d.getTime()) ? '-' : d.toLocaleString('zh-CN');
}

function renderWB(key, px) {
  const p = providerStatus ? providerStatus[key] : null;
  if (!p) return;
  const allModels = p.models || [];
  const nFree = allModels.filter(m => m.cost === 'free').length;
  // 上游目录里没有 credits 字段的条目: 既不是免费也没有已知倍率。
  // 实测它们仍按量扣积分(HN 系约 0.01/2.5k token), 所以必须单独计数提示。
  const nUnpriced = allModels.filter(m => m.cost !== 'free' && !m.credits).length;
  const nAvail = allModels.filter(m => m.available).length;
  const rows = [
    ['显示名', esc(p.displayName)],
    ['区域', p.region === 'global' ? 'global（workbuddy.ai）' : 'cn（codebuddy.cn）'],
    ['上游 Chat Base', '<span class="mono">' + esc(p.chatBase) + '</span>'],
    ['桌面端可执行文件', p.electron ? '<span class="mono">' + esc(p.electron) + '</span>' : '<span style="color:var(--danger)">未找到（可设 WORKBUDDY_ELECTRON_BIN 环境变量）</span>'],
    ['凭据目录', '<span class="mono" style="font-size:11px">' + esc(p.authDir) + '</span>' + (p.fileFound ? ' <span style="color:var(--accent2)">✓ 已找到 ' + esc(p.desktopFile) + '</span>' : ' <span style="color:var(--amber)">未找到 ' + esc(p.desktopFile) + '</span>')],
    ['账号数', String((p.accounts || []).length)],
    ['模型数', String(allModels.length) + '（上游目录全部列出）'],
    ['其中免费', String(nFree) + ' 个（credits 倍率为 0，不扣积分）'],
    ['上游未标价', nUnpriced
      ? '<span style="color:var(--amber)">' + nUnpriced + ' 个（上游目录没给 credits 字段，但实测按量扣积分，<code>/v1/models</code> 里不加计费段）</span>'
      : '0 个'],
    ['可调用', nAvail === allModels.length
      ? '<span style="color:var(--accent2)">' + nAvail + ' / ' + allModels.length + '</span>'
      : '<span style="color:var(--amber)">' + nAvail + ' / ' + allModels.length + '（' + (allModels.length - nAvail) + ' 个上游未接入，已从 /v1/models 隐藏）</span>']
  ];
  const box = _(px + 'Info');
  if (box) box.innerHTML = '<table><tbody>' + rows.map(r => '<tr><td style="text-align:left;color:var(--text2);width:170px">' + r[0] + '</td><td style="text-align:left">' + r[1] + '</td></tr>').join('') + '</tbody></table>';

  const accBox = _(px + 'AccountsList');
  const accs = p.accounts || [];
  if (accBox) {
    if (!accs.length) {
      accBox.innerHTML = '<div class="empty">暂无账号。请确认本机已登录 WorkBuddy 桌面端，然后点上方「从本机导入账号」。</div>';
    } else {
      accBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">账号</th><th style="text-align:left">UID / UIN</th><th>状态</th><th>过期时间</th><th style="text-align:left">备注</th></tr></thead><tbody>' +
        accs.map(a => '<tr>' +
          '<td style="text-align:left">' + esc(a.email || a.nickname || '-') + '</td>' +
          '<td style="text-align:left;font-family:monospace;font-size:11px">' + esc(a.uid) + '<br>' + esc(a.uin) + '</td>' +
          '<td>' + (a.hasToken ? (a.expired ? '<span class="status expired"><span class="status-dot expired"></span>已过期</span>' : '<span class="status active"><span class="status-dot active"></span>有效</span>') : '<span class="status cooldown"><span class="status-dot cooldown"></span>无令牌</span>') + '</td>' +
          '<td class="mono" style="font-size:11px">' + esc(a.expiresAtText || '-') + '</td>' +
          '<td style="text-align:left;font-size:11px;color:var(--text3)">' + esc(a.lastError || '') + '</td>' +
        '</tr>').join('') + '</tbody></table></div>';
    }
  }

  const mBox = _(px + 'ModelsList');
  if (mBox) {
    const models = allModels;
    const info = _(px + 'ModelsProbeInfo');
    if (info) {
      info.textContent = models.length
        ? '共 ' + models.length + ' 个 · 可调用 ' + nAvail + ' 个' + (models.length > nAvail ? ' · ' + (models.length - nAvail) + ' 个上游未接入' : '')
        : '';
    }
    if (!models.length) {
      mBox.innerHTML = '<div class="empty">暂无模型，点右上角「🔄 手动同步」拉取（需先有可用账号）。</div>';
    } else {
      mBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型 ID</th><th style="text-align:left">名称</th><th>计费</th><th>倍率</th><th>能力</th><th>可用性</th><th>上下文</th><th>输出</th></tr></thead><tbody>' +
        models.map(m => '<tr>' +
          '<td style="text-align:left;font-family:monospace">' + esc(m.baseId || m.id) + '</td>' +
          '<td style="text-align:left">' + esc(m.name || '') + '</td>' +
          '<td>' + (m.cost === 'free'
            ? '<span class="model-tag free">免费</span>'
            : (m.credits
              ? '<span class="model-tag pay">付费</span>'
              : '<span class="model-tag" style="color:var(--text3)" title="上游目录里这条没有 credits 字段；实测调用仍按量扣积分（HN 系约 0.01/2.5k token），所以既不是免费也没有已知倍率">未标价</span>')) + '</td>' +
          '<td class="mono" style="font-size:11px">' + (m.credits || '上游未提供') + '</td>' +
          '<td style="font-size:11px">' + (m.tools ? '🛠 ' : '') + (m.reasoning ? '🧠' : '') + '</td>' +
          '<td>' + (m.available
            ? '<span class="status active"><span class="status-dot active"></span>可用</span>'
            : '<span class="status expired" title="' + esc(m.probeNote || '') + '"><span class="status-dot expired"></span>未接入</span>') + '</td>' +
          '<td>' + (m.context || 0) + '</td>' +
          '<td>' + (m.output || 0) + '</td>' +
        '</tr>').join('') + '</tbody></table></div>' +
        '<div class="hint">上游目录里的模型全部列出；「未接入」指上游返回 <code>11102 service info not found</code>，已从 <code>/v1/models</code> 隐藏。' +
        '调用时可用 <code>workbuddy/&lt;模型ID&gt;</code>（本区）或 <code>workbuddy-ai/&lt;模型ID&gt;</code>（国际版）强制路由。</div>';
    }
  }
}

function renderQoder() {
  const p = providerStatus ? providerStatus.qoder : null;
  if (!p) return;
  const rows = [
    ['上游 OpenAPI', '<span class="mono">' + esc(p.openApiUrl) + '</span>'],
    ['上游 Chat Base', '<span class="mono">' + esc(p.chatBase) + '</span>'],
    ['账号数', String((p.accounts || []).length)],
    ['模型数', String(p.modelCount || 0) + (p.freeOnly ? '（仅免费）' : '（全部）')]
  ];
  const box = _('qdInfo');
  if (box) box.innerHTML = '<table><tbody>' + rows.map(r => '<tr><td style="text-align:left;color:var(--text2);width:170px">' + r[0] + '</td><td style="text-align:left">' + r[1] + '</td></tr>').join('') + '</tbody></table>';

  const accBox = _('qdAccountsList');
  const accs = p.accounts || [];
  if (accBox) {
    if (!accs.length) {
      accBox.innerHTML = '<div class="empty">暂无账号，请在上方粘贴 Qoder 个人访问令牌添加。</div>';
    } else {
      accBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">账号</th><th style="text-align:left">令牌</th><th>令牌状态</th><th>jobToken 有效期</th><th style="text-align:left">备注</th><th></th></tr></thead><tbody>' +
        accs.map(a => '<tr>' +
          '<td style="text-align:left">' + esc(a.email || a.name || a.userId || a.id) + '</td>' +
          '<td style="text-align:left;font-family:monospace;font-size:11px">' + esc(a.patTail || '') + '</td>' +
          '<td>' + (a.hasToken ? '<span class="status active"><span class="status-dot active"></span>已换取</span>' : '<span class="status cooldown"><span class="status-dot cooldown"></span>未换取</span>') + '</td>' +
          '<td class="mono" style="font-size:11px">' + esc(a.tokenExpiry || '-') + '</td>' +
          '<td style="text-align:left;font-size:11px;color:var(--text3)">' + esc(a.lastError || '') + '</td>' +
          '<td><button class="btn btn-sm btn-danger" onclick="delQoderAccount(\'' + a.id + '\')" title="删除">✕</button></td>' +
        '</tr>').join('') + '</tbody></table></div>';
    }
  }

  const mBox = _('qdModelsList');
  const models = p.models || [];
  if (mBox) {
    if (!models.length) {
      mBox.innerHTML = '<div class="empty">暂无模型，点右上角「🔄 手动同步」拉取（需先添加账号）。</div>';
    } else {
      mBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型 ID</th><th style="text-align:left">显示名</th><th>倍率</th><th>上下文</th><th>推理</th></tr></thead><tbody>' +
        models.map(m => '<tr><td style="text-align:left;font-family:monospace">' + esc(m.id) + '</td><td style="text-align:left">' + esc(m.name || '') + '</td><td>' + (m.price ?? 0) + '</td><td>' + (m.context || 0) + '</td><td>' + (m.reason ? '✓' : '') + '</td></tr>').join('') +
        '</tbody></table></div><div class="hint">共 ' + models.length + ' 个模型 · 「显示名」是上游可读名称（如 dfmodel = DeepSeek-V4-Pro）· 倍率 0 表示免费 · 调用时可用 <code>qoder/&lt;模型ID&gt;</code> 或 <code>qd/&lt;模型ID&gt;</code> 强制路由</div>';
    }
  }
}

// ========== 积分 / 签到 ==========
let creditsData = null;

async function loadCredits() {
  try {
    const d = await api('GET', '/providers/credits');
    creditsData = d.data;
    renderCredits('workbuddy', 'wb');
    renderCredits('workbuddy-ai', 'wba');
    renderCredits('qoder', 'qd');
    renderCredits('trae', 'tr');
    renderDashCredits();
    renderModelStatCards();
  } catch (e) {
    ['wbCreditsBox', 'wbaCreditsBox', 'qdCreditsBox', 'trCreditsBox'].forEach(id => {
      const el = _(id);
      if (el) el.innerHTML = '<div class="empty">加载积分失败: ' + esc(e.message) + '</div>';
    });
    const db = _('dashCreditsBox');
    if (db) db.innerHTML = '<div class="empty">加载积分失败: ' + esc(e.message) + '</div>';
  }
}

const CREDIT_SOURCES = [
  { key: 'workbuddy', label: 'WorkBuddy（中国区）', prefix: 'wb' },
  { key: 'workbuddy-ai', label: 'WorkBuddy 国际版', prefix: 'wba' },
  { key: 'qoder', label: 'Qoder', prefix: 'qd' },
  { key: 'trae', label: 'Trae', prefix: 'tr' },
  { key: 'loomy', label: '科大讯飞', prefix: 'lm' }
];

// 仪表盘上的紧凑视图：一行一个来源，余额 + 签到状态 + 就地签到按钮。
function renderDashCredits() {
  const box = _('dashCreditsBox');
  if (!box) return;
  if (!creditsData) { box.innerHTML = '<div class="empty">暂无数据</div>'; return; }
  const rows = CREDIT_SOURCES.map(s => {
    const p = creditsData[s.key] || {};
    const c = p.credits;
    const st = p.checkin;
    let left;
    if (c) {
      const pct = c.total > 0 ? Math.min(100, Math.round((c.remain / c.total) * 1000) / 10) : 0;
      const col = pct > 50 ? 'var(--accent2)' : (pct > 20 ? 'var(--amber)' : 'var(--danger)');
      left = '<span style="font-size:20px;font-weight:600;color:' + col + '">' + fmtCredits(c.remain) + '</span>' +
        '<span style="font-size:12px;color:var(--text3)"> / ' + fmtCredits(c.total) + ' ' + esc(c.unit || 'credits') + '</span>' +
        (c.planName ? '<span class="badge">' + esc(c.planName) + '</span>' : '');
    } else {
      left = '<span style="font-size:12px;color:var(--text3)">' + esc(p.creditsError || '余额查询失败') + '</span>';
    }
    let right;
    if (!st) {
      right = '<span style="font-size:11px;color:var(--text3)">' + esc(p.checkinError || '无签到状态') + '</span>' +
        '<button class="btn btn-sm" onclick="doCheckin(\'' + s.key + '\')">🎁 签到</button>';
    } else if (!st.active) {
      right = '<span style="font-size:11px;color:var(--text3)">该区暂无签到活动</span>';
    } else if (st.todayCheckedIn) {
      right = '<span class="status active"><span class="status-dot active"></span>今日已签到</span>' +
        (st.streakDays ? '<span style="font-size:11px;color:var(--text3)">连续 ' + st.streakDays + ' 天</span>' : '') +
        (st.totalCredits ? '<span style="font-size:11px;color:var(--text3)">累计 ' + fmtCredits(st.totalCredits) + '</span>' : '');
    } else {
      right = '<span style="font-size:11px;color:var(--amber)">今日未签到' + (st.dailyCredit ? ' · 可领 +' + fmtCredits(st.dailyCredit) : '') + '</span>' +
        '<button class="btn btn-sm btn-primary" onclick="doCheckin(\'' + s.key + '\')">🎁 立即签到</button>';
    }
    return '<div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:10px 14px;margin:6px 0;' +
      'background:rgba(148,163,184,.06);border:1px solid var(--border);border-radius:10px">' +
      '<span style="min-width:150px;font-size:13px;color:var(--text)">' + esc(s.label) + '</span>' +
      '<span style="display:flex;align-items:baseline;gap:6px;flex:1;min-width:220px">' + left + '</span>' +
      '<span style="display:flex;align-items:center;gap:8px">' + right + '</span>' +
      '</div>';
  }).join('');
  box.innerHTML = renderAutoCheckinBanner() + rows;
}

// 启动期自动签到结果横幅: 让用户一眼看到"程序启动时已经自动领过了"。
function renderAutoCheckinBanner() {
  const ac = creditsData && creditsData.autoCheckin;
  if (!ac || !ac.lastRunAt) return '';
  const ps = ac.providers || {};
  const items = CREDIT_SOURCES.map(s => {
    const o = ps[s.key];
    if (!o) return '';
    const mark = o.status === 'success' ? '✅' : (o.status === 'already' ? '☑️' : (o.status === 'error' ? '❌' : '⏭️'));
    const color = o.status === 'error' ? 'var(--danger)' : (o.status === 'success' ? 'var(--accent2)' : 'var(--text3)');
    return '<span style="display:inline-flex;align-items:center;gap:4px;font-size:11px;color:' + color + '">' +
      mark + ' ' + esc(s.label) + ' ' + esc(o.message || o.status) + '</span>';
  }).filter(Boolean);
  if (!items.length) return '';
  const t = new Date(ac.lastRunAt);
  const pad = n => (n < 10 ? '0' + n : '' + n);
  return '<div style="padding:10px 14px;margin:6px 0;background:rgba(148,163,184,.06);border:1px solid var(--border);border-radius:10px">' +
    '<div style="font-size:12px;color:var(--text2);margin-bottom:6px">🤖 启动自动签到 · ' +
    esc(t.getFullYear() + '-' + pad(t.getMonth() + 1) + '-' + pad(t.getDate()) + ' ' + pad(t.getHours()) + ':' + pad(t.getMinutes()) + ':' + pad(t.getSeconds())) +
    '</div><div style="display:flex;gap:12px;flex-wrap:wrap">' + items.join('') + '</div></div>';
}

async function doCheckinAll() {
  const targets = CREDIT_SOURCES.filter(s => {
    const st = creditsData && creditsData[s.key] ? creditsData[s.key].checkin : null;
    return st && st.active && !st.todayCheckedIn;
  });
  if (!targets.length) { toast('没有待签到的来源（可能都已签到或该区无活动）', 'info'); return; }
  const msgs = [];
  for (const s of targets) {
    try {
      const d = await api('POST', '/providers/' + s.key + '/checkin', {});
      msgs.push(s.label + '：' + (d.message || '完成'));
    } catch (e) {
      msgs.push(s.label + '：失败 ' + e.message);
    }
  }
  toast(msgs.join('\n'), 'info', 8000);
  loadCredits();
}

function fmtCredits(n) {
  if (n == null) return '-';
  const r = Math.round(n * 100) / 100;
  return r.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

function renderCredits(key, px) {
  const box = _(px + 'CreditsBox');
  if (!box) return;
  const p = creditsData ? creditsData[key] : null;
  if (!p) { box.innerHTML = '<div class="empty">暂无数据</div>'; return; }
  if (p.error) { box.innerHTML = '<div class="empty">' + esc(p.error) + '</div>'; return; }

  const c = p.credits;
  const st = p.checkin;
  let html = '';
  if (c) {
    const pct = c.total > 0 ? Math.min(100, Math.round((c.remain / c.total) * 1000) / 10) : 0;
    const barColor = pct > 50 ? 'var(--accent2)' : (pct > 20 ? 'var(--amber)' : 'var(--danger)');
    html += '<div style="display:flex;align-items:baseline;gap:10px;flex-wrap:wrap;margin-bottom:8px">' +
      '<span style="font-size:26px;font-weight:600;color:' + barColor + '">' + fmtCredits(c.remain) + '</span>' +
      '<span style="color:var(--text2);font-size:13px">/ ' + fmtCredits(c.total) + ' ' + esc(c.unit || 'credits') + '</span>' +
      (c.planName ? '<span class="badge">' + esc(c.planName) + '</span>' : '') +
      (c.isPaid ? '<span class="badge">付费</span>' : '') +
      '</div>' +
      '<div style="height:6px;border-radius:3px;background:rgba(148,163,184,.2);overflow:hidden;margin-bottom:10px">' +
      '<div style="height:100%;width:' + pct + '%;background:' + barColor + '"></div></div>' +
      '<div style="font-size:12px;color:var(--text3);margin-bottom:10px">已用 ' + fmtCredits(c.used) +
      (c.packageCount > 1 ? ' · 共 ' + c.packageCount + ' 个套餐包聚合' : '') + '</div>';
  } else if (p.creditsError) {
    html += '<div class="empty" style="margin-bottom:8px">余额查询失败: ' + esc(p.creditsError) + '</div>';
  }
  if (key === 'qoder' && p.quota && p.quota.addOnQuota && p.quota.addOnQuota.total > 0 && p.quota.userQuota.total === 0) {
    html += '<div style="font-size:12px;color:var(--text3);margin-bottom:10px">Qoder 套餐额度为 0，当前使用的是加购额度（addOnQuota）</div>';
  }
  if (key === 'qoder' && p.quotaExceeded) {
    html += '<div style="font-size:12px;color:var(--danger);margin-bottom:10px">⚠ 配额已用尽，调用会返回 insufficient_quota</div>';
  }
  // Trae 的用量接口已经实调确认: 余额在 usage_summary(总额/已用),
  // 上面那块 credits 卡片已经渲染好了; 这里补上分档明细(每张权益包),
  // 以及原始 JSON 供排查。
  if (key === 'trae' && p.usage) {
    const u = p.usage;
    const packs = Array.isArray(u.user_entitlement_pack_list) ? u.user_entitlement_pack_list : [];
    if (packs.length) {
      const rows = packs.map(pk => {
        const base = pk.entitlement_base_info || {};
        const quota = base.quota || ((base.product_extra || {}).package_extra || {}).quota || {};
        const limit = quota.credits_limit;
        const used = (pk.usage || {}).credits_amount;
        if (!limit && !used) return '';
        return '<tr><td>' + esc(pk.display_desc || pk.group_name || '-') + '</td>' +
          '<td class="mono">' + (limit != null ? esc(String(limit)) : '-') + '</td>' +
          '<td class="mono">' + (used != null ? esc(String(used)) : '-') + '</td></tr>';
      }).filter(Boolean);
      if (rows.length) {
        html += '<details style="margin-bottom:10px"><summary style="font-size:12px;color:var(--text2);cursor:pointer">分档明细（' +
          rows.length + ' 项）</summary><div class="table-wrap" style="margin-top:8px"><table style="font-size:12px">' +
          '<thead><tr><th style="text-align:left">权益包</th><th>额度</th><th>已用</th></tr></thead><tbody>' + rows.join('') + '</tbody></table></div></details>';
      }
    }
    html += '<details style="margin-bottom:10px"><summary style="font-size:11px;color:var(--text3);cursor:pointer">上游原始用量数据</summary>' +
      '<pre class="mono" style="font-size:11px;color:var(--text3);white-space:pre-wrap;margin:6px 0 0">' + esc(JSON.stringify(u, null, 2)) + '</pre></details>';
  }
  if (key === 'trae' && st && st.deviceUsed) {
    html += '<div style="font-size:12px;color:var(--amber);margin-bottom:10px">该设备标识已领取过（Trae 签到按 deviceId 幂等）</div>';
  }

  html += '<div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding-top:10px;border-top:1px solid var(--border)">';
  if (st) {
    if (!st.active) {
      html += '<span style="font-size:12px;color:var(--text3)">该区暂无签到活动</span>';
    } else if (st.todayCheckedIn) {
      html += '<span class="status active"><span class="status-dot active"></span>今日已签到</span>';
      if (st.streakDays) html += '<span style="font-size:12px;color:var(--text2)">连续 ' + st.streakDays + ' 天</span>';
      if (st.dailyCredit) html += '<span style="font-size:12px;color:var(--text3)">每日 +' + fmtCredits(st.dailyCredit) + '</span>';
    } else {
      html += '<span style="font-size:12px;color:var(--amber)">今日未签到</span>';
      if (st.dailyCredit) html += '<span style="font-size:12px;color:var(--text2)">可领 +' + fmtCredits(st.dailyCredit) + ' 积分</span>';
    }
    if (st.themeName) html += '<span style="font-size:12px;color:var(--text3)">' + esc(st.themeName) + '</span>';
    if (st.totalCredits) html += '<span style="font-size:12px;color:var(--text3)">活动累计 ' + fmtCredits(st.totalCredits) + '</span>';
    html += '<button class="btn btn-sm' + (st.active && !st.todayCheckedIn ? ' btn-primary' : '') + '" style="margin-left:auto" onclick="doCheckin(\'' + key + '\')">🎁 立即签到</button>';
  } else if (p.checkinError) {
    html += '<span style="font-size:12px;color:var(--text3)">签到状态: ' + esc(p.checkinError) + '</span>' +
      '<button class="btn btn-sm" style="margin-left:auto" onclick="doCheckin(\'' + key + '\')">🎁 立即签到</button>';
  }
  html += '</div>';
  if (p.account) html += '<div style="font-size:11px;color:var(--text3);margin-top:8px">账号 ' + esc(p.account) + '</div>';
  box.innerHTML = html;
}

async function doCheckin(key) {
  const label = { 'workbuddy': 'WorkBuddy', 'workbuddy-ai': 'WorkBuddy 国际版', 'qoder': 'Qoder', 'trae': 'Trae' }[key] || key;
  try {
    const d = await api('POST', '/providers/' + key + '/checkin', {});
    toast(label + '：' + (d.message || '签到完成'), d.success ? 'success' : 'info', 5000);
  } catch (e) {
    toast(label + ' 签到失败: ' + e.message, 'error', 9000);
  }
  loadCredits();
}

async function loadWBConfig(which) {
  try {
    const d = await api('GET', '/providers/status');
    const p = which === 'workbuddy-ai' ? d.data['workbuddy-ai'] : d.data['workbuddy'];
    const px = which === 'workbuddy-ai' ? 'wba' : 'wb';
    _(px + 'Enabled').value = String(!!p.enabled);
    _(px + 'MaxRetries').value = p.maxRetries == null ? 2 : p.maxRetries;
  } catch (e) { /* ignore */ }
}

async function saveWBConfig(which) {
  const px = which === 'workbuddy-ai' ? 'wba' : 'wb';
  try {
    await api('POST', '/providers/' + which + '/config', {
      enabled: _(px + 'Enabled').value === 'true',
      maxRetries: parseInt(_(px + 'MaxRetries').value) || 0
    });
    toast('配置已保存', 'success');
    loadProviders();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function loadQoderConfig() {
  try {
    const p = providerStatus ? providerStatus.qoder : (await api('GET', '/providers/status')).data.qoder;
    _('qdEnabled').value = String(!!p.enabled);
    _('qdFreeOnly').value = String(!!p.freeOnly);
    _('qdMaxRetries').value = p.maxRetries == null ? 2 : p.maxRetries;
  } catch (e) { /* ignore */ }
}

async function saveQoderConfig() {
  try {
    await api('POST', '/providers/qoder/config', {
      enabled: _('qdEnabled').value === 'true',
      freeOnly: _('qdFreeOnly').value === 'true',
      maxRetries: parseInt(_('qdMaxRetries').value) || 0
    });
    toast('配置已保存', 'success');
    loadProviders();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function importWB() {
  try {
    const d = await api('POST', '/providers/workbuddy/import', {});
    toast(d.message || '导入完成', 'success', 6000);
    loadProviders();
  } catch (e) { toast('导入失败: ' + e.message, 'error', 9000); }
}

async function refreshWB(variant) {
  try {
    const d = await api('POST', '/providers/workbuddy/refresh', { variant: variant });
    toast(d.message || '刷新完成', d.success ? 'success' : 'error', 7000);
    loadProviders();
  } catch (e) { toast('刷新失败: ' + e.message, 'error', 9000); }
}

async function refreshWBModels(which) {
  try {
    const d = await api('POST', '/providers/' + which + '/models/refresh', {});
    toast(d.message || '同步完成', 'success', 6000);
    loadProviders();
  } catch (e) { toast('同步失败: ' + e.message, 'error', 9000); }
}

// probeWBModels 对某变体的全部模型实调一次判定"上游是否真的接入"。
// 上游目录不区分已接入/未接入(9 个模型一调就 11102), 所以只能实调。
async function probeWBModels(which, btn) {
  const label = which === 'workbuddy' ? 'WorkBuddy' : 'WorkBuddy 国际版';
  if (!confirm('将对该区每个模型实调一次（max_tokens=1，失败的模型不消耗积分，但需要几十秒）。继续？')) return;
  const old = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; btn.textContent = '检测中...'; }
  try {
    const d = await api('POST', '/providers/' + which + '/probe', {});
    toast(label + '：' + (d.message || '检测完成'), 'success', 9000);
  } catch (e) {
    toast(label + ' 检测失败: ' + e.message, 'error', 9000);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = old; }
    loadProviders();
  }
}

async function addQoderAccount() {
  const pat = _('qdPat').value.trim();
  if (!pat) { toast('请先粘贴 Qoder 个人访问令牌', 'error'); return; }
  try {
    const d = await api('POST', '/providers/qoder/accounts/add', { pat: pat, region: 'china' });
    _('qdPat').value = '';
    toast(d.message || '添加成功', 'success', 6000);
    loadProviders();
  } catch (e) { toast('添加失败: ' + e.message, 'error', 9000); }
}

async function delQoderAccount(id) {
  if (!confirm('确定删除该 Qoder 账号？')) return;
  try {
    await api('POST', '/providers/qoder/accounts/delete', { id: id });
    toast('已删除', 'success');
    loadProviders();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function refreshQoderModels() {
  try {
    const d = await api('POST', '/providers/qoder/models/refresh', {});
    toast(d.message || '同步完成', 'success', 6000);
    loadProviders();
  } catch (e) { toast('同步失败: ' + e.message, 'error', 9000); }
}

// ========== Trae ==========
const TRAE_SOURCE_TEXT = { desktop: '本机客户端', cli: '本机 CLI', manual: '手动粘贴' };
const TRAE_EDITION_TEXT = { cn: 'Trae CN', sg: 'Trae 国际', solo: 'TRAE SOLO CN', 'solo-sg': 'TRAE SOLO' };

function renderTrae() {
  const p = providerStatus ? providerStatus.trae : null;
  if (!p) return;
  const rows = [
    ['区域', 'cn（www.trae.cn）'],
    ['账号数', String((p.accounts || []).length)],
    ['模型数', String(p.modelCount || 0) + (p.freeOnly ? '（仅免费）' : '（全部）')]
  ];
  const box = _('trInfo');
  if (box) box.innerHTML = '<table><tbody>' + rows.map(r => '<tr><td style="text-align:left;color:var(--text2);width:170px">' + r[0] + '</td><td style="text-align:left">' + r[1] + '</td></tr>').join('') + '</tbody></table>';

  const accBox = _('trAccountsList');
  const accs = p.accounts || [];
  if (accBox) {
    if (!accs.length) {
      accBox.innerHTML = '<div class="empty">暂无账号。请确认本机已登录 Trae 桌面端，然后点上方「📥 从本机导入账号」。</div>';
    } else {
      accBox.innerHTML = '<div class="table-wrap"><table><thead><tr>' +
        '<th style="text-align:left">账号</th><th style="text-align:left">版本</th><th style="text-align:left">来源</th>' +
        '<th style="text-align:left">userId</th><th style="text-align:left">deviceId</th>' +
        '<th>令牌状态</th><th>有效期至</th><th>可续期</th><th style="text-align:left">备注</th><th></th>' +
        '</tr></thead><tbody>' +
        accs.map(a => '<tr>' +
          '<td style="text-align:left;font-size:12px">' + esc(a.userName || a.id) + '</td>' +
          '<td style="text-align:left;font-size:11px">' + esc(TRAE_EDITION_TEXT[a.edition] || a.edition || '-') + '</td>' +
          '<td style="text-align:left;font-size:11px">' + esc(TRAE_SOURCE_TEXT[a.source] || a.source || '-') + '</td>' +
          '<td style="text-align:left;font-family:monospace;font-size:11px">' + esc(a.userId || '-') + '</td>' +
          '<td style="text-align:left;font-family:monospace;font-size:11px">' + esc(a.deviceId || '-') + '</td>' +
          '<td>' + (a.hasToken ? (a.expired ? '<span class="status expired"><span class="status-dot expired"></span>已过期</span>' : '<span class="status active"><span class="status-dot active"></span>有效</span>') : '<span class="status cooldown"><span class="status-dot cooldown"></span>无令牌</span>') + '</td>' +
          '<td class="mono" style="font-size:11px">' + esc(a.expiresAtText || '-') + '</td>' +
          // 没有 refreshToken 就只能等令牌过期后重新导入; 有 refresh 但缺设备密钥
          // 对时仍能走旧接口兜底, 所以两者分开提示。
          '<td>' + (a.renewable
            ? (a.hasDeviceKey ? '<span class="status active"><span class="status-dot active"></span>可续期</span>'
                              : '<span class="status cooldown"><span class="status-dot cooldown"></span>降级续期</span>')
            : '<span class="status expired"><span class="status-dot expired"></span>不可续期</span>') + '</td>' +
          '<td style="text-align:left;font-size:11px;color:var(--text3)">' + esc(a.lastError || '') + '</td>' +
          '<td><button class="btn btn-sm btn-danger" onclick="delTraeAccount(\'' + a.id + '\')" title="删除">✕</button></td>' +
        '</tr>').join('') + '</tbody></table></div>' +
        '<div class="hint">「可续期」= 有 refreshToken 且未过期（新接口需设备密钥对签名）；' +
        '「降级续期」= 缺设备密钥对，只能走旧接口；「不可续期」= 过期后需重新导入。</div>';
    }
  }

  const mBox = _('trModelsList');
  const models = p.models || [];
  if (mBox) {
    if (!models.length) {
      mBox.innerHTML = '<div class="empty">暂无模型，点右上角「🔄 手动同步」拉取（需先添加账号）。</div>';
    } else {
      mBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型 ID</th><th style="text-align:left">显示名</th><th>倍率</th><th>上下文</th><th>推理</th></tr></thead><tbody>' +
        models.map(m => '<tr><td style="text-align:left;font-family:monospace">' + esc(m.id) + '</td><td style="text-align:left">' + esc(m.name || '') + '</td><td>' + (m.price ?? 0) + '</td><td>' + (m.context || 0) + '</td><td>' + (m.reason ? '✓' : '') + '</td></tr>').join('') +
        '</tbody></table></div><div class="hint">共 ' + models.length + ' 个模型 · 倍率 0 表示免费 · 调用时可用 <code>trae/&lt;模型ID&gt;</code> 或 <code>tr/&lt;模型ID&gt;</code> 强制路由</div>';
    }
  }
}

async function loadTraeConfig() {
  try {
    const p = providerStatus ? providerStatus.trae : (await api('GET', '/providers/status')).data.trae;
    _('trEnabled').value = String(!!p.enabled);
    _('trFreeOnly').value = String(!!p.freeOnly);
    _('trMaxRetries').value = p.maxRetries == null ? 2 : p.maxRetries;
  } catch (e) { /* ignore */ }
}

async function saveTraeConfig() {
  try {
    await api('POST', '/providers/trae/config', {
      enabled: _('trEnabled').value === 'true',
      freeOnly: _('trFreeOnly').value === 'true',
      maxRetries: parseInt(_('trMaxRetries').value) || 0
    });
    toast('配置已保存', 'success');
    loadProviders();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function importTrae() {
  try {
    const d = await api('POST', '/providers/trae/import', {});
    toast(d.message || '导入完成', 'success', 8000);
    loadProviders();
  } catch (e) { toast('导入失败: ' + e.message, 'error', 9000); }
}

async function addTraeAccount() {
  const raw = _('trCredJson').value.trim();
  if (!raw) { toast('请先粘贴 Trae 凭据 JSON', 'error'); return; }
  let cred;
  try {
    cred = JSON.parse(raw);
  } catch (e) {
    toast('JSON 解析失败: ' + e.message, 'error', 9000);
    return;
  }
  if (!cred.accessToken) { toast('凭据缺少 accessToken 字段', 'error'); return; }
  try {
    const d = await api('POST', '/providers/trae/accounts/add', cred);
    _('trCredJson').value = '';
    toast(d.message || '添加成功', 'success', 6000);
    loadProviders();
  } catch (e) { toast('添加失败: ' + e.message, 'error', 9000); }
}

async function delTraeAccount(id) {
  if (!confirm('确定删除该 Trae 账号？')) return;
  try {
    await api('POST', '/providers/trae/accounts/delete', { id: id });
    toast('已删除', 'success');
    loadProviders();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function refreshTraeModels() {
  try {
    const d = await api('POST', '/providers/trae/models/refresh', {});
    toast(d.message || '同步完成', 'success', 6000);
    loadProviders();
  } catch (e) { toast('同步失败: ' + e.message, 'error', 9000); }
}

// ========== 科大讯飞 Loomy ==========
function loomySwitchAddTab(name) {
  ['pass', 'sms', 'session'].forEach(t => {
    const tab = _('lmTab' + t.charAt(0).toUpperCase() + t.slice(1));
    const body = _('lmAdd' + t.charAt(0).toUpperCase() + t.slice(1));
    if (tab) tab.classList.toggle('active', t === name);
    if (body) body.style.display = t === name ? '' : 'none';
  });
}

function renderLoomy() {
  const p = providerStatus ? providerStatus.loomy : null;
  if (!p) return;
  const rows = [
    ['模型网关', 'https://loomyad.xunfei.cn/api/v1（OpenAI 兼容）'],
    ['账号服务', 'account.xfinfr.com（HMAC-SHA1 签名登录）'],
    ['账号数', String((p.accounts || []).length) + ' · 模型数 ' + String(p.modelCount || 0)],
    ['计费', '点数制：永久点数 + 每日赠送（优先消耗），倍率 x0.1 ~ x12'],
    ['图片生成', '支持 /v1/images/generations（模型列表里带 🎨 生图 标志的模型）']
  ];
  const box = _('lmInfo');
  if (box) box.innerHTML = '<table><tbody>' + rows.map(r => '<tr><td style="text-align:left;color:var(--text2);width:150px">' + r[0] + '</td><td style="text-align:left">' + r[1] + '</td></tr>').join('') + '</tbody></table>';

  const accBox = _('lmAccountsList');
  const accs = p.accounts || [];
  if (accBox) {
    if (!accs.length) {
      accBox.innerHTML = '<div class="empty">暂无账号。用上方「密码登录 / 短信验证码 / 导入 Session」任一方式添加。</div>';
    } else {
      accBox.innerHTML = '<div class="table-wrap"><table><thead><tr>' +
        '<th style="text-align:left">账号</th><th style="text-align:left">手机号</th>' +
        '<th>点数(可用)</th><th>永久/每日</th><th>Session</th><th>有效期至</th>' +
        '<th>调用</th><th>耗点</th><th style="text-align:left">备注</th><th></th>' +
        '</tr></thead><tbody>' +
        accs.map(a => {
          const sess = a.sessionTail
            ? (a.daysLeft >= 0 && a.daysLeft < 3
              ? '<span class="status expired"><span class="status-dot expired"></span>将过期</span>'
              : '<span class="status active"><span class="status-dot active"></span>有效</span>')
            : '<span class="status cooldown"><span class="status-dot cooldown"></span>无</span>';
          const renewal = a.hasPassword ? '可自动续期' : '<span style="color:var(--amber)">需手动换</span>';
          return '<tr>' +
          '<td style="text-align:left;font-size:12px">' + esc(a.name || '未命名') + '</td>' +
          '<td style="text-align:left;font-family:monospace;font-size:11px">' + esc(a.loginId || '-') + '</td>' +
          '<td style="font-weight:600">' + fmtNum(a.available) + '</td>' +
          '<td class="mono" style="font-size:11px">' + fmtNum(a.balance) + ' / ' + fmtNum(a.dailyBalance) + '</td>' +
          '<td>' + sess + (a.inCooldown ? ' <span class="status cooldown"><span class="status-dot cooldown"></span>冷却</span>' : '') + '</td>' +
          '<td class="mono" style="font-size:11px">' + esc(a.expireAtText || '-') + '<div style="font-size:10px;color:var(--text3)">' + renewal + '</div></td>' +
          '<td>' + fmtNum(a.requests) + '</td>' +
          '<td>' + (a.pointsUsed ? Number(a.pointsUsed).toFixed(1) : '0') + '</td>' +
          '<td style="text-align:left;font-size:11px;color:var(--text3)">' + esc(a.lastError || '') + '</td>' +
          '<td><button class="btn btn-sm" onclick="refreshLoomyOne(\'' + a.id + '\')" title="重登并刷新点数">🔄</button> ' +
          '<button class="btn btn-sm btn-danger" onclick="delLoomyAccount(\'' + a.id + '\')" title="删除">✕</button></td>' +
          '</tr>';
        }).join('') + '</tbody></table></div>' +
        '<div class="hint">「有效」按 session 剩余天数判断（剩余 &lt;3 天显示「将过期」，有密码的账号会在后台自动重登）；' +
        '「耗点」是本次运行累计消耗的点数（来自上游 usage.points_consumed）。</div>';
    }
  }

  const mBox = _('lmModelsList');
  const models = p.models || [];
  if (mBox) {
    if (!models.length) {
      mBox.innerHTML = '<div class="empty">暂无模型，点右上角「🔄 手动同步」拉取（需先添加账号）。</div>';
    } else {
      mBox.innerHTML = '<div class="table-wrap"><table><thead><tr><th style="text-align:left">模型 ID</th><th style="text-align:left">显示名</th><th>倍率</th><th>上下文</th><th>能力</th></tr></thead><tbody>' +
        models.map(m => {
          const caps = [
            m.reason ? '🧠' : '', m.image ? '👁' : '',
            m.imageGen ? '<span title="走 /v1/images/generations 调用">🎨 生图</span>' : ''
          ].filter(Boolean).join(' ');
          return '<tr><td style="text-align:left;font-family:monospace">' + esc(m.id) + '</td>' +
            '<td style="text-align:left">' + esc(m.name || '') + '</td>' +
            '<td>' + (m.price ?? 0) + '</td><td>' + (m.context || 0) + '</td>' +
            '<td style="white-space:nowrap">' + (caps || '-') + '</td></tr>';
        }).join('') +
        '</tbody></table></div><div class="hint">共 ' + models.length + ' 个模型 · 倍率 0 表示上游未标价（按点数实扣） · ' +
        '调用时可用 <code>loomy/&lt;模型ID&gt;</code>、<code>xf/&lt;模型ID&gt;</code> 强制路由 · 🎨 模型走 <code>/v1/images/generations</code></div>';
    }
  }
}

async function loadLoomyConfig() {
  try {
    const p = providerStatus ? providerStatus.loomy : (await api('GET', '/providers/status')).data.loomy;
    _('lmEnabled').value = String(!!p.enabled);
    _('lmMaxRetries').value = p.maxRetries == null ? 1 : p.maxRetries;
    renderLoomy();
  } catch (e) { /* ignore */ }
}

async function saveLoomyConfig() {
  try {
    await api('POST', '/providers/loomy/config', {
      enabled: _('lmEnabled').value === 'true',
      maxRetries: parseInt(_('lmMaxRetries').value) || 0
    });
    toast('配置已保存', 'success');
    loadProviders();
  } catch (e) { toast('保存失败: ' + e.message, 'error'); }
}

async function addLoomyPassword() {
  const phone = _('lmPhone').value.trim();
  const password = _('lmPassword').value;
  if (!phone || !password) { toast('请填写手机号和密码', 'error'); return; }
  try {
    const d = await api('POST', '/providers/loomy/accounts/add', {
      mode: 'password', phone: phone, password: password, name: _('lmName').value.trim()
    });
    _('lmPassword').value = '';
    toast(d.message || '添加成功', 'success', 8000);
    loadProviders();
  } catch (e) { toast('添加失败: ' + e.message, 'error', 10000); }
}

async function loomySmsSend() {
  const phone = _('lmSmsPhone').value.trim();
  if (!phone) { toast('请填写手机号', 'error'); return; }
  try {
    const d = await api('POST', '/providers/loomy/sms/send', { phone: phone });
    window._loomyMsgId = (d.data && d.data.msgid) || '';
    toast(d.message || '验证码已发送', 'success');
  } catch (e) { toast('发送失败: ' + e.message, 'error', 9000); }
}

async function loomySmsVerify() {
  const phone = _('lmSmsPhone').value.trim();
  const code = _('lmSmsCode').value.trim();
  if (!phone || !code) { toast('请填写手机号和验证码', 'error'); return; }
  if (!window._loomyMsgId) { toast('请先点「发送验证码」', 'error'); return; }
  try {
    const d = await api('POST', '/providers/loomy/sms/verify', {
      phone: phone, code: code, msgid: window._loomyMsgId, name: _('lmSmsName').value.trim()
    });
    window._loomyMsgId = '';
    _('lmSmsCode').value = '';
    toast(d.message || '添加成功', 'success', 8000);
    loadProviders();
  } catch (e) { toast('验证失败: ' + e.message, 'error', 10000); }
}

async function addLoomySession() {
  const session = _('lmSession').value.trim();
  if (!session) { toast('请粘贴 session token', 'error'); return; }
  try {
    const d = await api('POST', '/providers/loomy/accounts/add', {
      mode: 'session', session: session, name: _('lmSessionName').value.trim()
    });
    _('lmSession').value = '';
    toast(d.message || '导入成功', 'success', 8000);
    loadProviders();
  } catch (e) { toast('导入失败: ' + e.message, 'error', 10000); }
}

async function delLoomyAccount(id) {
  if (!confirm('确定删除该科大讯飞账号？')) return;
  try {
    await api('POST', '/providers/loomy/accounts/delete', { id: id });
    toast('已删除', 'success');
    loadProviders();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function refreshLoomyOne(id) {
  try {
    const d = await api('POST', '/providers/loomy/refresh', { id: id });
    toast(d.message || '刷新完成', d.success ? 'success' : 'error', 8000);
    loadProviders();
  } catch (e) { toast('刷新失败: ' + e.message, 'error', 10000); }
}

async function refreshLoomyAccounts() {
  try {
    const d = await api('POST', '/providers/loomy/refresh', {});
    toast(d.message || '刷新完成', d.success ? 'success' : 'error', 8000);
    loadProviders();
  } catch (e) { toast('刷新失败: ' + e.message, 'error', 10000); }
}

async function refreshLoomyModels() {
  try {
    const d = await api('POST', '/providers/loomy/models/refresh', {});
    toast(d.message || '同步完成', 'success', 6000);
    loadProviders();
  } catch (e) { toast('同步失败: ' + e.message, 'error', 9000); }
}

// ========== 模型聊天 ==========
let chatHistory = [];
let chatPending = false;
async function loadChatModels() {
  const sel = _('chatModel');
  try {
    // 三个来源合并, 带来源标签, 按去重后的顺序展示
    const items = [];
    const push = (id, src) => { if (id && !items.some(it => it.id === id)) items.push({ id: id, src: src }); };
    try {
      const d = await api('GET', '/models');
      (d.data.models || []).forEach(m => push(m.id, 'cline'));
    } catch (_) {}
    try {
      const z = await api('GET', '/opencode/models');
      (z.data.models || []).forEach(m => push(m.id, 'zen'));
    } catch (_) {}
    // 第三方 provider: 只放可用模型, 未接入的选中也只会 502
    (await fetchProviderModels()).forEach(m => { if (m.available !== false) push(m.id, m.src); });
    if (!items.length) { sel.innerHTML = '<option value="">暂无模型</option>'; return; }
    const sig = items.map(it => it.id).join(',');
    if (sel.dataset.sig !== sig) {
      sel.innerHTML = items.map(it => '<option value="' + esc(it.id) + '">[' + it.src + '] ' + esc(it.id) + '</option>').join('');
      sel.dataset.sig = sig;
    }
    if (!sel.value) {
      const def = items.find(it => it.id === 'deepseek/deepseek-v4-flash') || items[0];
      sel.value = def.id;
    }
  } catch (e) { sel.innerHTML = '<option value="">模型加载失败</option>'; }
}
function chatRender() {
  const box = _('chatBox');
  if (!chatHistory.length) { box.innerHTML = '<div class="hint" style="margin:auto;text-align:center">选择模型，输入消息开始对话</div>'; return; }
  box.innerHTML = chatHistory.map(m => {
    if (m.role === 'error') return '<div class="bubble bubble-error">' + esc(m.content) + '</div>';
    const who = m.role === 'user' ? '你' : esc(m.model || 'assistant');
    return '<div class="bubble bubble-' + m.role + '"><div class="bubble-who">' + who + '</div>' + esc(m.content) + '</div>';
  }).join('');
  box.scrollTop = box.scrollHeight;
}
function chatMessages() {
  return chatHistory.filter(m => m.role === 'user' || m.role === 'assistant')
    .map(m => ({ role: m.role, content: m.content }));
}
async function sendChat() {
  if (chatPending) return;
  const input = _('chatInput');
  const text = input.value.trim();
  if (!text) return;
  const model = _('chatModel').value;
  if (!model) { toast('请先选择模型', 'error'); return; }
  input.value = '';
  chatHistory.push({ role: 'user', content: text });
  const msg = { role: 'assistant', content: '', model: model };
  chatHistory.push(msg);
  chatPending = true;
  _('chatSendBtn').disabled = true;
  chatRender();
  try {
    const headers = { 'Content-Type': 'application/json' };
    const res = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: headers,
      body: JSON.stringify({ model: model, messages: chatMessages(), stream: true })
    });
    if (!res.ok) {
      const errText = await res.text();
      let em = 'HTTP ' + res.status;
      try { const ej = JSON.parse(errText); if (ej.error && ej.error.message) em = ej.error.message; } catch (_) {}
      throw new Error(em);
    }
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const rd = await reader.read();
      if (rd.done) break;
      buf += dec.decode(rd.value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, idx).trim();
        buf = buf.slice(idx + 1);
        if (!line.startsWith('data:')) continue;
        const payload = line.slice(5).trim();
        if (payload === '[DONE]') continue;
        try {
          const j = JSON.parse(payload);
          const d = j.choices && j.choices[0] && j.choices[0].delta;
          if (d && typeof d.content === 'string' && d.content) msg.content += d.content;
        } catch (_) {}
      }
      chatRender();
    }
    if (!msg.content.trim()) throw new Error('空回复');
  } catch (e) {
    if (chatHistory[chatHistory.length - 1] === msg && !msg.content) chatHistory.pop();
    chatHistory.push({ role: 'error', content: '请求失败: ' + e.message });
  }
  chatPending = false;
  _('chatSendBtn').disabled = false;
  chatRender();
  _('chatInput').focus();
}
function clearChat() {
  if (chatPending) return;
  chatHistory = [];
  chatRender();
}

// ========== 模型列表页(全部模型 + 默认模型设置) ==========
// fetchProviderModels 取三个第三方 provider 的模型目录(WorkBuddy CN/INTL + Qoder)。
// 这些模型不在 /admin/api/models(cline)与 /admin/api/opencode/models(zen)里,
// 必须单独拉, 否则列表页与聊天页根本看不到它们。
async function fetchProviderModels() {
  const out = [];
  // priced 表示"上游目录给了价": WorkBuddy 看 credits 字段(x0.29 credits),
  // Qoder 看 priceFactor。两者都可能缺 —— 缺了就是未标价, 不能当成免费。
  const srcs = [['workbuddy', 'workbuddy'], ['workbuddy-ai', 'workbuddy-ai'], ['qoder', 'qoder'], ['trae', 'trae'], ['loomy', 'loomy']];
  try {
    const d = await api('GET', '/providers/models');
    const data = d.data || {};
    srcs.forEach(function (pair) {
      (data[pair[0]] || []).forEach(function (m) {
        const isQd = pair[0] === 'qoder';
        // 上游是否给了价: WorkBuddy 看 credits 字段(x0.29 credits), Qoder 看
        // priceFactor(两者都总有该键, qfmodel 的 0 是真免费)。缺失即"未标价",
        // 不能当成免费 —— 见 providerDecorateID 的注释。
        // Trae 单独用 priced 标记: 它的倍率键 credit_multiplier 可能整个缺失,
        // 缺省值也是 0, 光看数值分不清"免费"和"上游没给", 必须看键在不在。
        let priced;
        if (pair[0] === 'trae') priced = !!m.priced;
        else if (isQd) priced = (m.priceFactor !== undefined && m.priceFactor !== null);
        else priced = !!m.credits;
        out.push({
          id: m.id,
          baseId: m.baseId || m.id,
          src: pair[1],
          name: m.name || '',
          available: m.available === undefined ? null : m.available,
          probeNote: m.probeNote || '',
          context: m.context || 0,
          cost: m.cost || (priced ? ((m.multiplier || m.priceFactor || 0) === 0 ? 'free' : 'paid') : 'paid'),
          credits: m.credits || '',
          priced: priced,
          multiplier: m.multiplier || 0,
          price: m.priceFactor || m.price || 0,
          reason: !!m.reason,
          tools: !!m.tools,
          // 模态三态: true/false 来自上游目录, null 表示上游没给(后台显示"?"),
          // 点过"探测"后由 /models/probe-modality 的结果覆盖。
          image: m.image === undefined ? null : m.image,
          video: m.video === undefined ? null : m.video,
          // 图片生成能力: loomy 目录按关键词判定, true 时走 /v1/images/generations
          imageGen: m.imageGen === true
        });
      });
    });
  } catch (_) {}
  return out;
}

// ========== 模型列表页(表格 + 筛选 + 模态探测) ==========
// src 是内部路由键, group 是对外 /v1/models 的 owned_by。
const MODEL_GROUP = { cline: 'cline-free', zen: 'opencode-zen', workbuddy: 'workbuddy', 'workbuddy-ai': 'workbuddy-ai', qoder: 'qoder', trae: 'trae', loomy: 'loomy' };
// 第三方 provider 的 src 值, 用于检测"某个来源一条模型都没拿到"(同步延迟/失败)
const PROVIDER_GROUPS = ['workbuddy', 'workbuddy-ai', 'qoder', 'trae', 'loomy'];
// 计数条固定按这个顺序展示(含 0), 某个来源掉线时能一眼看出来
const MODEL_GROUP_ORDER = ['cline-free', 'opencode-zen', 'workbuddy', 'workbuddy-ai', 'qoder', 'trae', 'loomy'];
let MODEL_ITEMS = [];
let MODEL_DEF = '';
let MODEL_SORT = { key: 'group', dir: 1 };
let MODEL_RETRY = 0;

function fmtCtx(n) {
  if (!n) return '—';
  if (n >= 1000000) return (n / 1000000).toFixed(1).replace('.0', '') + 'M';
  if (n >= 1000) return Math.round(n / 1000) + 'k';
  return String(n);
}
function modelIsFree(m) {
  if (m.src === 'cline' || m.src === 'zen') return m.cost === 'free';
  if (m.priced === false) return false;
  return (m.multiplier || m.price || 0) === 0;
}
function modelIsAvail(m) {
  if (m.src === 'cline') return m.status === 'active';
  if (m.src === 'zen') return true;
  return m.available !== false;
}
// 三态: true 支持 / false 不支持 / null 上游没给(显示 "?", 不等于不支持)
function triCell(v, tip) {
  if (v === true) return '<span class="tick" style="color:var(--accent2)">✓</span>';
  if (v === false) return '<span class="tick" style="color:var(--text3)">✗</span>';
  return '<span class="tick" style="color:var(--amber)" title="' + esc(tip || '上游未提供该信息') + '">?</span>';
}
function rateCell(m) {
  if (modelIsFree(m)) return '<span style="color:var(--accent2)">免费</span>';
  if (m.src === 'cline') return '<span style="color:var(--text2)">' + esc(COST_LABEL[m.cost] || m.cost || '—') + '</span>';
  if (m.priced === false) return '<span style="color:var(--text3)" title="上游目录没有给价（无 credits / priceFactor），实测仍按量扣积分">未标价</span>';
  return '<span style="color:var(--amber)">x' + (m.multiplier || m.price || 0) + '</span>';
}
function statusCell(m) {
  if (m.src === 'cline') {
    const st = MODEL_STYLE[m.status] || MODEL_STYLE.unknown;
    return '<span class="model-tag" style="' + st.css + '">' + st.label + '</span>';
  }
  if (m.src === 'zen') return '<span class="model-tag" style="color:var(--accent2);border:1px solid rgba(52,211,153,.5)">可用</span>';
  if (m.available === false) return '<span class="model-tag" style="color:var(--danger);border:1px solid rgba(248,113,113,.5)" title="' + esc(m.probeNote || '') + '">未接入</span>';
  if (m.available === true) return '<span class="model-tag" style="color:var(--accent2);border:1px solid rgba(52,211,153,.5)">可用</span>';
  return '<span class="model-tag" style="color:var(--text3)" title="尚未实调探测">未探测</span>';
}
function sortVal(m, key) {
  switch (key) {
    case 'name': return (m.label || '').toLowerCase();
    case 'group': return MODEL_GROUP_ORDER.indexOf(m.group);
    case 'rate': return modelIsFree(m) ? -1 : (m.multiplier || m.price || 999);
    case 'ctx': return m.context || 0;
    case 'image': return m.image === true ? 2 : (m.image === false ? 1 : 0);
    case 'video': return m.video === true ? 2 : (m.video === false ? 1 : 0);
    case 'status': return m.available === false ? 0 : (m.available === true ? 2 : 1);
    default: return MODEL_GROUP_ORDER.indexOf(m.group);
  }
}
function sortModels(key) {
  if (MODEL_SORT.key === key) MODEL_SORT.dir = -MODEL_SORT.dir;
  else MODEL_SORT = { key: key, dir: 1 };
  renderModelTable();
}

async function loadAllModels() {
  const box = _('allModelsList');
  try {
    const items = [];
    const seen = {};
    const push = o => {
      if (!o.id || seen[o.key]) return;
      seen[o.key] = 1;
      o.label = o.name && o.name !== o.id ? o.name : (o.baseId || o.id);
      o.tags = [];
      if (o.tools) o.tags.push('工具');
      if (o.reason) o.tags.push('推理');
      if (o.imageGen) o.tags.push('🎨 生图');
      items.push(o);
    };
    try {
      const d = await api('GET', '/models');
      (d.data.models || []).forEach(m => push({
        id: m.id, baseId: m.id, src: 'cline', group: MODEL_GROUP.cline,
        name: m.name || '', context: m.context || 0, image: m.image, video: m.video,
        cost: m.cost, status: m.status, usage: m.usage || null,
        multiplier: undefined, priced: true, key: 'cline:' + m.id
      }));
    } catch (_) {}
    try {
      const z = await api('GET', '/opencode/models');
      (z.data.models || []).forEach(m => push({
        id: m.id, baseId: m.id, src: 'zen', group: MODEL_GROUP.zen,
        name: '', context: m.context || 0, image: m.image, video: m.video,
        cost: 'free', status: 'active', usage: m.usage || null,
        multiplier: 0, priced: true, key: 'zen:' + m.id
      }));
    } catch (_) {}
    const provs = await fetchProviderModels();
    provs.forEach(m => push({
      id: m.id, baseId: m.baseId, src: m.src, group: MODEL_GROUP[m.src] || m.src,
      name: m.name, context: m.context || 0, image: m.image, video: m.video,
      cost: m.cost, status: '', usage: null,
      available: m.available, probeNote: m.probeNote,
      multiplier: m.multiplier, price: m.price, priced: m.priced,
      reason: m.reason, tools: m.tools,
      imageGen: m.imageGen,
      key: m.src + ':' + m.baseId
    }));
    let def = '';
    try { const c = await api('GET', '/config'); def = c.data.defaultModel || ''; } catch (_) {}
    MODEL_DEF = def;
    MODEL_ITEMS = items;
    _('curDefModel').textContent = def || '-';
    if (!items.length) { box.innerHTML = '<div class="empty">暂无模型</div>'; return; }
    renderModelTable();
    // Qoder 的模型目录是启动后延迟 3 秒异步同步的, 早于它打开页面会拿到空列表,
    // 之后也不会自己补 —— 这里发现某个 provider 一条都没有就自动重试两次。
    const missing = PROVIDER_GROUPS.filter(s => !provs.some(m => m.src === s));
    if (missing.length && MODEL_RETRY < 2) {
      MODEL_RETRY++;
      setTimeout(loadAllModels, 3000);
    } else {
      MODEL_RETRY = 0;
    }
  } catch (e) { box.textContent = '加载失败: ' + e.message; }
}

// 用量从表格里拿掉了(列太多), 挂在模型格子的 tooltip 上, 信息不丢。
function modelUsageTip(m) {
  const u = m.usage || {};
  if (!((u.todayRequests || 0) + (u.totalRequests || 0))) return '';
  const f = n => n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? (n / 1e3).toFixed(1) + 'k' : (n || 0);
  return '今日 ' + (u.todayRequests || 0) + ' 次 · ' + f((u.todayPromptTokens || 0) + (u.todayCompletionTokens || 0)) + ' tok\n' +
    '累计 ' + (u.totalRequests || 0) + ' 次 · ' + f((u.totalPromptTokens || 0) + (u.totalCompletionTokens || 0)) + ' tok';
}

function modelRow(m) {
  const isDef = m.id === MODEL_DEF;
  const usageTip = modelUsageTip(m);
  const nameHtml = '<div class="mname" title="' + esc(m.label) + '">' + esc(m.label) + '</div>' +
    (m.id !== m.label ? '<div class="mid" title="' + esc(m.id) + '">' + esc(m.id) + '</div>' : '') +
    (m.tags.length ? '<div style="margin-top:3px">' + m.tags.map(t => '<span class="model-tag">' + esc(t) + '</span>').join('') + '</div>' : '');
  const unknownImg = m.image === null || m.image === undefined;
  const acts = isDef
    ? '<span class="model-tag" style="color:var(--accent);border:1px solid rgba(34,211,238,.5)">默认</span>'
    : '<button class="btn btn-sm" data-id="' + esc(m.id) + '" onclick="setDefaultFromBtn(this)">设为默认</button>';
  return '<tr class="' + (isDef ? 'isdef' : '') + '">' +
    '<td' + (usageTip ? ' title="' + esc(usageTip) + '"' : '') + '>' + nameHtml + '</td>' +
    '<td><span class="model-tag">' + esc(m.group) + '</span></td>' +
    '<td>' + rateCell(m) + '</td>' +
    '<td style="color:var(--text2)">' + fmtCtx(m.context) + '</td>' +
    '<td>' + triCell(m.image, '上游目录未提供模态信息，可点「探测」实调验证') + '</td>' +
    '<td>' + triCell(m.video, '上游目录未提供模态信息；视频暂无自动探测') + '</td>' +
    '<td>' + statusCell(m) + '</td>' +
    '<td><div class="mact">' + acts +
    '<button class="btn btn-sm" data-id="' + esc(m.id) + '" onclick="copyModelText(this)">复制</button>' +
    (unknownImg ? '<button class="btn btn-sm" data-key="' + esc(m.key) + '" data-model="' + esc(m.id) + '" onclick="probeModality(this)">探测</button>' : '') +
    '</div></td></tr>';
}

function renderModelTable() {
  const box = _('allModelsList');
  if (!box) return;
  const q = (_('mFilterText').value || '').trim().toLowerCase();
  const g = _('mFilterGroup').value;
  const onlyFree = _('mFilterFree').checked;
  const onlyImg = _('mFilterImage').checked;
  const onlyAvail = _('mFilterAvail').checked;
  const rows = MODEL_ITEMS.filter(m => {
    if (g && m.group !== g) return false;
    if (onlyFree && !modelIsFree(m)) return false;
    if (onlyImg && m.image !== true) return false;
    if (onlyAvail && !modelIsAvail(m)) return false;
    if (q) {
      const hay = ((m.label || '') + ' ' + (m.id || '') + ' ' + (m.baseId || '')).toLowerCase();
      if (hay.indexOf(q) < 0) return false;
    }
    return true;
  });
  const dir = MODEL_SORT.dir;
  rows.sort((a, b) => {
    const va = sortVal(a, MODEL_SORT.key), vb = sortVal(b, MODEL_SORT.key);
    if (va < vb) return -dir;
    if (va > vb) return dir;
    return (a.group + a.id).localeCompare(b.group + b.id);
  });
  const cnt = {};
  MODEL_ITEMS.forEach(it => { cnt[it.group] = (cnt[it.group] || 0) + 1; });
  const cEl = _('modelCounts');
  if (cEl) cEl.textContent = '显示 ' + rows.length + ' / 共 ' + MODEL_ITEMS.length + ' 个 · ' +
    MODEL_GROUP_ORDER.map(k => k + ' ' + (cnt[k] || 0)).join(' · ');
  if (!rows.length) { box.innerHTML = '<div class="empty">没有匹配的模型</div>'; return; }
  const arrow = k => MODEL_SORT.key === k ? (MODEL_SORT.dir > 0 ? ' ↑' : ' ↓') : '';
  const th = (k, label, w) => '<th class="sortable" style="width:' + w + '" onclick="sortModels(\'' + k + '\')">' + label + arrow(k) + '</th>';
  box.innerHTML = '<div style="overflow-x:auto"><table class="mtbl"><thead><tr>' +
    th('name', '模型', '31%') + th('group', '分组', '14%') + th('rate', '费率', '10%') +
    th('ctx', '上下文', '9%') + th('image', '图片', '7%') + th('video', '视频', '7%') +
    th('status', '状态', '11%') + '<th style="width:11%">操作</th>' +
    '</tr></thead><tbody>' + rows.map(modelRow).join('') + '</tbody></table></div>';
}

function setDefaultFromBtn(btn) { setDefaultModelFromList(btn.getAttribute('data-id')); }

async function setDefaultModelFromList(id) {
  try {
    await api('POST', '/config/update', { defaultModel: id });
    toast('默认模型已设为: ' + id, 'success');
    loadAllModels();
  } catch (e) { toast('设置失败: ' + e.message, 'error'); }
}

function copyModelText(btn) {
  const id = btn.getAttribute('data-id');
  const done = () => toast('已复制: ' + id, 'success');
  const fallback = () => {
    const ta = document.createElement('textarea');
    ta.value = id;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); done(); } catch (e) { toast('复制失败', 'error'); }
    document.body.removeChild(ta);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(id).then(done).catch(fallback);
  } else { fallback(); }
}

// 实调一次带 1x1 PNG 的请求判断图片能力。目录给不出结论的模型才显示这个按钮,
// 每次探测都会真实消耗一次上游额度, 结果后端缓存 7 天。
async function probeModality(btn) {
  const key = btn.getAttribute('data-key');
  const model = btn.getAttribute('data-model');
  btn.disabled = true;
  btn.textContent = '探测中';
  try {
    const r = await api('POST', '/models/probe-modality', { model: model, key: key });
    const d = r.data || {};
    const it = MODEL_ITEMS.filter(x => x.key === key)[0];
    if (it) {
      if (d.image === true || d.image === false) it.image = d.image;
      if (d.video === true || d.video === false) it.video = d.video;
    }
    toast(d.note || '探测完成', d.image === true ? 'success' : (d.image === false ? 'error' : 'info'), 5000);
    renderModelTable();
  } catch (e) {
    btn.disabled = false;
    btn.textContent = '探测';
    toast('探测失败: ' + e.message, 'error');
  }
}
_('chatInput').addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendChat(); }
});

// ========== 初始化 ==========
loadStats();
loadModelStats();
loadAccounts();
loadKeys();
loadModels();
loadConfig();

// 深链: /admin/#models 直接打开对应 tab(便于分享/排查时定位到具体页面)
if (location.hash.length > 1) {
  const deep = location.hash.slice(1);
  // 兼容旧的 Cline 三个页面锚点
  if (deep === 'accounts' || deep === 'import' || deep === 'settings') {
    switchTab('cline');
    switchClineSub(deep);
  } else if (_('tab-' + deep)) switchTab(deep);
}
loadCredits();
setInterval(() => { loadStats(); }, 10000);
setInterval(() => { loadModelStats(); }, 30000);
setInterval(() => { loadOcStats(); }, 15000);
setInterval(() => { if (_('tab-logs').style.display !== 'none') loadLogs(); }, 8000);
</script>
</body>
</html>`
