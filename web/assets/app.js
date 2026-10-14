'use strict';

/* =============== 基础工具 =============== */
let ME = null;               // {user, csrf}
let LAST_STATUS = null;      // 仪表盘快照缓存(图表算内存百分比用)
let AI_HIST = [];            // AI 会话历史(前端内存)
let timers = [];
const $ = s => document.querySelector(s);
const mainEl = () => document.getElementById('main');

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
function fmtBytes(n) {
  n = +n || 0;
  if (!isFinite(n) || n < 0) return '-';
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + u[i];
}
const fmtBps = n => fmtBytes(n) + '/s';
function fmtUptime(sec) {
  sec = +sec || 0;
  const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  return (d ? d + ' 天 ' : '') + h + ' 小时 ' + m + ' 分';
}
function fmtTime(iso) {
  if (!iso || String(iso).startsWith('0001-')) return '-';
  const d = new Date(iso);
  return isNaN(d) ? '-' : d.toLocaleString('zh-CN', { hour12: false });
}
function toast(msg, type) {
  const box = document.getElementById('toasts');
  const t = document.createElement('div');
  t.className = 'toast ' + (type || '');
  t.textContent = msg;
  box.appendChild(t);
  setTimeout(() => t.remove(), type === 'err' ? 6000 : 3500);
}
function openModal(title, html) {
  document.getElementById('modal-title').textContent = title;
  document.getElementById('modal-content').innerHTML = html;
  document.getElementById('modal').classList.remove('hidden');
}
function closeModal() {
  document.getElementById('modal').classList.add('hidden');
  document.getElementById('modal-content').innerHTML = '';
}
function confirmGo(msg) { return window.confirm(msg); }

async function api(path, method, body, headers) {
  const opts = { method: method || 'GET', headers: {} };
  if (ME) opts.headers['X-Panel-CSRF'] = ME.csrf;
  if (body !== undefined && body !== null) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  Object.assign(opts.headers, headers || {});
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch (e) { /* 非 JSON */ }
  if (res.status === 401) { showLogin(); throw new Error('未登录'); }
  if (!res.ok || !data || data.ok === false) throw new Error((data && data.error) || ('HTTP ' + res.status));
  return data;
}

const loadedScripts = {};
function loadScript(src) {
  if (loadedScripts[src]) return loadedScripts[src];
  loadedScripts[src] = new Promise((ok, no) => {
    const s = document.createElement('script');
    s.src = src;
    s.onload = ok;
    s.onerror = () => no(new Error('加载失败: ' + src));
    document.head.appendChild(s);
  });
  return loadedScripts[src];
}

function every(ms, fn) { timers.push(setInterval(fn, ms)); fn(); }
function stopTimers() { timers.forEach(clearInterval); timers = []; }

/* =============== 登录 / 初始化 =============== */
function showLogin() {
  stopTimers();
  document.getElementById('login-layer').classList.remove('hidden');
  document.getElementById('nav').classList.add('hidden');
  mainEl().classList.add('hidden');
  document.getElementById('login-form').classList.remove('hidden');
  document.getElementById('setup-form').classList.add('hidden');
}
function showSetup() {
  document.getElementById('login-layer').classList.remove('hidden');
  document.getElementById('login-form').classList.add('hidden');
  document.getElementById('setup-form').classList.remove('hidden');
  document.getElementById('login-sub').textContent = '首次使用,请创建管理员账号';
}
function enterPanel() {
  document.getElementById('login-layer').classList.add('hidden');
  document.getElementById('nav').classList.remove('hidden');
  mainEl().classList.remove('hidden');
  document.getElementById('foot-user').textContent = '已登录: ' + ME.user;
  navigate();
}

/* =============== 路由 =============== */
const routes = {
  dashboard: pageDashboard, services: pageServices, monitors: pageMonitors,
  cron: pageCron, ports: pagePorts, sites: pageSites, containers: pageContainers,
  apps: pageApps, terminal: pageTerminal, ai: pageAI, settings: pageSettings,
};
function navigate() {
  const hash = (location.hash || '#dashboard').slice(1).split('?')[0];
  const fn = routes[hash] || pageDashboard;
  document.querySelectorAll('aside a').forEach(a => a.classList.toggle('active', a.getAttribute('href') === '#' + hash));
  disconnectTerm();
  stopTimers();
  mainEl().innerHTML = '<div class="muted">加载中…</div>';
  fn();
}

/* =============== 图表 =============== */
function drawChart(canvas, series, opts) {
  if (!canvas) return;
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth || 300, h = canvas.clientHeight || 110;
  if (canvas.width !== Math.round(w * dpr)) { canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr); }
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  let max = (opts && opts.max) || 0;
  if (!max) {
    for (const s of series) for (const v of s.data) if (v > max) max = v;
    max = max ? max * 1.15 : 1;
  }
  ctx.strokeStyle = 'rgba(255,255,255,.07)';
  ctx.fillStyle = '#5c6b8c';
  ctx.font = '10px sans-serif';
  ctx.lineWidth = 1;
  for (let i = 0; i <= 2; i++) {
    const y = 4 + i * (h - 22) / 2;
    ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke();
    const val = max * (1 - i / 2);
    ctx.fillText(opts && opts.fmt ? opts.fmt(val) : Math.round(val).toString(), 4, y > 14 ? y - 3 : y + 11);
  }
  for (const s of series) {
    let pts = s.data;
    const N = 240;
    if (pts.length > N) {
      const step = pts.length / N;
      pts = Array.from({ length: N }, (_, i) => s.data[Math.floor(i * step)]);
    }
    if (pts.length < 2) continue;
    ctx.beginPath();
    for (let i = 0; i < pts.length; i++) {
      const x = i / (pts.length - 1) * w;
      const y = h - 16 - Math.min(Math.max(pts[i], 0), max) / max * (h - 24);
      if (i) ctx.lineTo(x, y); else ctx.moveTo(x, y);
    }
    ctx.strokeStyle = s.color;
    ctx.lineWidth = 1.6;
    ctx.stroke();
    ctx.lineTo(w, h); ctx.lineTo(0, h); ctx.closePath();
    ctx.globalAlpha = 0.10; ctx.fillStyle = s.color; ctx.fill(); ctx.globalAlpha = 1;
  }
}

/* =============== 仪表盘 =============== */
function pageDashboard() {
  mainEl().innerHTML = `
    <header class="top"><h2>仪表盘</h2><div class="right muted" id="dash-host"></div></header>
    <div class="cards" id="dash-cards"></div>
    <div class="grid2" style="margin-top:14px">
      <div class="chartbox"><h4 id="t-cpu">CPU</h4><canvas class="chart" id="ch-cpu"></canvas></div>
      <div class="chartbox"><h4 id="t-mem">内存</h4><canvas class="chart" id="ch-mem"></canvas></div>
      <div class="chartbox"><h4 id="t-net">网络</h4><canvas class="chart" id="ch-net"></canvas></div>
      <div class="chartbox"><h4 id="t-io">磁盘 I/O</h4><canvas class="chart" id="ch-io"></canvas></div>
    </div>
    <div class="grid2" style="margin-top:14px">
      <div class="panel"><h4>磁盘挂载</h4><div id="dash-disks" class="tbl-wrap"></div></div>
      <div class="panel"><h4>资源占用 Top 进程</h4><div id="dash-procs" class="tbl-wrap"></div></div>
    </div>`;
  every(3000, refreshStatus);
  every(5000, refreshHistory);
}
async function refreshStatus() {
  try {
    const st = await api('/api/system/status');
    LAST_STATUS = st;
    const mem = st.mem || {};
    const memPct = mem.Total ? mem.Used / mem.Total * 100 : 0;
    const ld = st.load || {};
    const rd = st.root_disk || {};
    document.getElementById('dash-host').textContent =
      st.host ? `${st.host.hostname} · ${st.host.os} · ${st.host.kernel}` : '';
    document.getElementById('dash-cards').innerHTML = `
      <div class="card"><h3>CPU (${st.cpu_count || '-'} 核)</h3><div class="big">${(st.cpu || 0).toFixed(1)}%</div>
        <div class="sub">负载 ${(+ld.one || 0).toFixed(2)} / ${(+ld.five || 0).toFixed(2)} / ${(+ld.fifteen || 0).toFixed(2)}</div></div>
      <div class="card"><h3>内存</h3><div class="big">${memPct.toFixed(1)}%</div>
        <div class="sub">${fmtBytes(mem.Used)} / ${fmtBytes(mem.Total)}${mem.SwapUsed ? ' · Swap ' + fmtBytes(mem.SwapUsed) : ''}</div></div>
      <div class="card"><h3>根目录磁盘</h3><div class="big">${(rd.usedPercent || 0).toFixed(1)}%</div>
        <div class="sub">${fmtBytes(rd.used)} / ${fmtBytes(rd.total)}</div></div>
      <div class="card"><h3>运行时间</h3><div class="big" style="font-size:18px">${fmtUptime(st.uptime)}</div>
        <div class="sub">采样时间 ${fmtTime(st.time)}</div></div>`;
    const disks = st.disks || [];
    document.getElementById('dash-disks').innerHTML = `<table>
      <tr><th>挂载点</th><th>类型</th><th>容量</th><th>已用</th></tr>` +
      disks.map(d => `<tr><td>${esc(d.mount)}</td><td>${esc(d.fstype)}</td>
        <td>${fmtBytes(d.total)}</td>
        <td>${(d.used_pct || 0).toFixed(1)}% <span class="muted">(${fmtBytes(d.used)})</span></td></tr>`).join('') +
      `</table>`;
    const procs = st.top_procs || [];
    document.getElementById('dash-procs').innerHTML = `<table>
      <tr><th>PID</th><th>进程</th><th>CPU</th><th>内存</th></tr>` +
      (procs.length ? procs.map(p => `<tr><td>${p.pid}</td><td>${esc(p.name)}</td>
        <td>${(p.cpu || 0).toFixed(1)}%</td><td>${(p.mem_mb || 0).toFixed(0)} MB</td></tr>`).join('')
        : '<tr><td colspan="4" class="muted">采集中…</td></tr>') + `</table>`;
  } catch (e) { /* 401 已处理 */ }
}
async function refreshHistory() {
  try {
    const h = await api('/api/system/history');
    const total = (LAST_STATUS && LAST_STATUS.mem && LAST_STATUS.mem.Total) || 0;
    const memPct = total ? h.mem_used.map(v => v / total * 100) : [];
    const cur = arr => arr.length ? arr[arr.length - 1] : 0;
    document.getElementById('t-cpu').textContent = 'CPU  ' + cur(h.cpu).toFixed(1) + '%';
    document.getElementById('t-mem').textContent = '内存  ' + (total ? (cur(h.mem_used) / total * 100).toFixed(1) + '%' : '');
    document.getElementById('t-net').textContent = '网络  ↓' + fmtBps(cur(h.net_rx)) + '  ↑' + fmtBps(cur(h.net_tx));
    document.getElementById('t-io').textContent = '磁盘 I/O  读' + fmtBps(cur(h.disk_r)) + '  写' + fmtBps(cur(h.disk_w));
    drawChart(document.getElementById('ch-cpu'), [{ data: h.cpu, color: '#4f8cff' }], { max: 100 });
    drawChart(document.getElementById('ch-mem'), [{ data: memPct, color: '#9a6bff' }], { max: 100 });
    drawChart(document.getElementById('ch-net'), [
      { data: h.net_rx, color: '#34c77b' }, { data: h.net_tx, color: '#f0a132' }], { fmt: fmtBps });
    drawChart(document.getElementById('ch-io'), [
      { data: h.disk_r, color: '#4f8cff' }, { data: h.disk_w, color: '#f05a5a' }], { fmt: fmtBps });
  } catch (e) { /* 忽略 */ }
}

/* =============== 服务 =============== */
function pageServices() {
  mainEl().innerHTML = `
    <header class="top"><h2>服务</h2><div class="right">
      <button class="ghost small" data-act="svc-refresh">刷新</button></div></header>
    <div class="panel"><h4>Homebrew 服务</h4><div id="svc-brew" class="tbl-wrap"><div class="muted">加载中…</div></div></div>
    <div class="panel"><h4>launchd 服务(LaunchAgents / LaunchDaemons)</h4>
      <div class="muted" style="margin-bottom:8px">系统级 LaunchDaemons 需要 root,面板仅可操作用户级服务</div>
      <div id="svc-launchd" class="tbl-wrap"><div class="muted">加载中…</div></div></div>`;
  every(10000, () => { loadBrewServices(); loadLaunchd(); });
}
async function loadBrewServices() {
  const box = document.getElementById('svc-brew');
  if (!box) return;
  try {
    const d = await api('/api/services/brew');
    const list = d.services || [];
    box.innerHTML = `<table><tr><th>名称</th><th>状态</th><th>用户</th><th>文件</th><th>操作</th></tr>` +
      (list.length ? list.map(sv => `<tr>
        <td><b>${esc(sv.name)}</b></td>
        <td><span class="badge ${sv.running ? 'ok' : (sv.status === 'error' ? 'err' : '')}">${esc(sv.status)}</span></td>
        <td>${esc(sv.user)}</td><td>${esc(sv.file)}</td>
        <td class="actions">
          ${sv.running
            ? `<button class="small ghost" data-act="brewsvc" data-arg="restart" data-name="${esc(sv.name)}">重启</button>
               <button class="small ghost" data-act="brewsvc" data-arg="stop" data-name="${esc(sv.name)}">停止</button>`
            : `<button class="small" data-act="brewsvc" data-arg="start" data-name="${esc(sv.name)}">启动</button>`}
          <button class="small ghost" data-act="brewsvc" data-arg="run" data-name="${esc(sv.name)}" title="前台运行,不注册开机自启">前台跑</button>
        </td></tr>`).join('') : '<tr><td colspan="5" class="muted">无 brew 服务</td></tr>') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}
async function loadLaunchd() {
  const box = document.getElementById('svc-launchd');
  if (!box) return;
  try {
    const d = await api('/api/services/launchd');
    const list = (d.services || []).sort((a, b) => a.label.localeCompare(b.label));
    box.innerHTML = `<table><tr><th>Label</th><th>域</th><th>状态</th><th>程序</th><th>属性</th><th>操作</th></tr>` +
      list.map(sv => {
        const manageable = sv.domain === 'user' || sv.domain === 'agent';
        return `<tr>
        <td><b>${esc(sv.label)}</b></td>
        <td><span class="badge">${sv.domain === 'user' ? '用户' : sv.domain === 'agent' ? '系统Agent' : '系统Daemon'}</span></td>
        <td>${sv.running ? `<span class="badge ok">运行 ${sv.pid}</span>` : (sv.last_exit ? `<span class="badge err">退出码 ${sv.last_exit}</span>` : '<span class="badge">未运行</span>')}</td>
        <td><code>${esc(sv.program)}</code></td>
        <td class="muted">${sv.keep_alive ? 'KeepAlive ' : ''}${sv.run_at_load ? 'RunAtLoad ' : ''}${sv.disabled ? 'Disabled' : ''}</td>
        <td class="actions">
          ${manageable ? `
            <button class="small ghost" data-act="launchd" data-arg="restart" data-name="${esc(sv.label)}">重启</button>
            ${sv.running
              ? `<button class="small ghost" data-act="launchd" data-arg="stop" data-name="${esc(sv.label)}">停止</button>`
              : `<button class="small" data-act="launchd" data-arg="start" data-name="${esc(sv.label)}">启动</button>`}`
            : '<span class="muted">只读</span>'}
        </td></tr>`;
      }).join('') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}

/* =============== 监控项 =============== */
function pageMonitors() {
  mainEl().innerHTML = `
    <header class="top"><h2>监控项</h2><div class="right"><button class="ghost small" data-act="mon-refresh">刷新</button></div></header>
    <div class="panel"><h4>添加监控</h4>
      <form data-form="mon-add">
        <div class="form-row">
          <div><label>名称</label><input name="name" required placeholder="如 Nextcloud"></div>
          <div><label>类型</label>
            <select name="type" id="mon-type">
              <option value="port">端口 (host:port)</option>
              <option value="http">HTTP(S) URL</option>
              <option value="launchd">launchd 服务 Label</option>
              <option value="process">进程名 (pgrep -f)</option>
            </select></div>
          <div><label>目标</label><input name="target" required placeholder="192.168.64.4:80"></div>
          <div style="flex:0;align-self:end"><label>&nbsp;</label><button type="submit">添加</button></div>
        </div>
      </form></div>
    <div id="mon-list" class="tbl-wrap"><div class="muted">加载中…</div></div>`;
  document.getElementById('mon-type').addEventListener('change', e => {
    const ph = { port: '192.168.64.4:80', http: 'http://127.0.0.1:8083', launchd: 'com.example.app', process: 'rclone' };
    e.target.closest('form').querySelector('input[name=target]').placeholder = ph[e.target.value] || '';
  });
  every(15000, loadMonitors);
  loadMonitors();
}
async function loadMonitors() {
  const box = document.getElementById('mon-list');
  if (!box) return;
  try {
    const d = await api('/api/monitors');
    const list = d.monitors || [];
    box.innerHTML = `<table><tr><th>名称</th><th>类型</th><th>目标</th><th>状态</th><th>延迟</th><th>最近检查</th><th>操作</th></tr>` +
      (list.length ? list.map(m => `<tr>
        <td><b>${esc(m.name)}</b></td>
        <td><span class="badge">${esc(m.type)}</span></td>
        <td><code>${esc(m.target)}</code></td>
        <td>${m.last_check && !String(m.last_check).startsWith('0001')
          ? (m.last_ok ? '<span class="badge ok">正常</span>' : `<span class="badge err" title="${esc(m.last_error)}">异常</span>`)
          : '<span class="badge">未检查</span>'}</td>
        <td>${m.latency_ms ? m.latency_ms.toFixed(0) + ' ms' : '-'}</td>
        <td class="muted">${m.last_check && !String(m.last_check).startsWith('0001') ? fmtTime(m.last_check) : '-'}</td>
        <td class="actions">
          <button class="small ghost" data-act="mon-check" data-arg="${m.id}">检查</button>
          ${m.type === 'launchd' ? `
            <button class="small ghost" data-act="mon-act" data-arg="${m.id}" data-a="restart" data-name="${esc(m.target)}">重启</button>
            <button class="small ghost" data-act="mon-act" data-arg="${m.id}" data-a="start" data-name="${esc(m.target)}">启动</button>
            <button class="small ghost" data-act="mon-act" data-arg="${m.id}" data-a="stop" data-name="${esc(m.target)}">停止</button>` : ''}
          <button class="small ghost" data-act="mon-toggle" data-arg="${m.id}" data-on="${m.enabled ? '1' : ''}">${m.enabled ? '停用' : '启用'}</button>
          <button class="small danger" data-act="mon-del" data-arg="${m.id}" data-name="${esc(m.name)}">删除</button>
        </td></tr>`).join('') : '<tr><td colspan="7" class="muted">还没有监控项,后台每 60 秒自动检查已启用的项</td></tr>') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}

/* =============== 计划任务 =============== */
function pageCron() {
  mainEl().innerHTML = `
    <header class="top"><h2>计划任务</h2><div class="right">
      <span class="muted">写回前自动备份到 data/cron-backup/</span>
      <button class="ghost small" data-act="cron-refresh">刷新</button></div></header>
    <div class="panel"><h4 id="cron-form-title">新增任务</h4>
      <form data-form="cron-add">
        <input type="hidden" name="index" value="-1">
        <div class="form-row">
          <div><label>调度</label><input name="schedule" required placeholder="*/5 * * * *">
            <select id="cron-preset" class="muted" style="margin-top:4px">
              <option value="">常用模板…</option>
              <option value="* * * * *">每分钟</option>
              <option value="*/5 * * * *">每 5 分钟</option>
              <option value="0 * * * *">每小时</option>
              <option value="0 3 * * *">每天 03:00</option>
              <option value="0 3 * * 0">每周日 03:00</option>
            </select></div>
          <div style="flex:2"><label>命令</label><input name="command" required placeholder="/usr/local/bin/rclone sync ..."></div>
          <div><label>备注名(可选)</label><input name="name" placeholder="如 tvbox-bisync"></div>
          <div style="flex:0;align-self:end"><button type="submit" id="cron-submit">添加</button></div>
        </div>
      </form></div>
    <div id="cron-list" class="tbl-wrap"><div class="muted">加载中…</div></div>`;
  document.getElementById('cron-preset').addEventListener('change', e => {
    if (e.target.value) {
      const f = e.target.closest('form');
      f.querySelector('input[name=schedule]').value = e.target.value;
      e.target.value = '';
    }
  });
  loadCron();
}
async function loadCron() {
  const box = document.getElementById('cron-list');
  if (!box) return;
  try {
    const d = await api('/api/cron');
    const list = d.jobs || [];
    box.innerHTML = `<table><tr><th>调度</th><th>命令</th><th>备注</th><th>状态</th><th>操作</th></tr>` +
      (list.length ? list.map(j => `<tr>
        <td><code>${esc(j.schedule)}</code></td>
        <td><code>${esc(j.command)}</code></td>
        <td>${esc(j.name)}</td>
        <td>${j.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge warn">已禁用</span>'}</td>
        <td class="actions">
          <button class="small ghost" data-act="cron-toggle" data-arg="${j.index}">${j.enabled ? '禁用' : '启用'}</button>
          <button class="small ghost" data-act="cron-edit" data-arg="${j.index}"
            data-sched="${esc(j.schedule)}" data-cmd="${esc(j.command)}" data-name="${esc(j.name)}">编辑</button>
          <button class="small danger" data-act="cron-del" data-arg="${j.index}">删除</button>
        </td></tr>`).join('') : '<tr><td colspan="5" class="muted">crontab 为空</td></tr>') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}

/* =============== 端口 / 入口 =============== */
function pagePorts() {
  mainEl().innerHTML = `
    <header class="top"><h2>端口 / 服务入口</h2><div class="right">
      <button class="ghost small" data-act="ports-refresh">刷新</button></div></header>
    <div class="muted" style="margin-bottom:10px">"打开"按住 ⌘ 可新窗口;已知服务在 设置 → 已知服务 里配置名称与 URL</div>
    <div id="ports-list" class="tbl-wrap"><div class="muted">加载中…</div></div>`;
  every(8000, loadPorts);
  loadPorts();
}
async function loadPorts() {
  const box = document.getElementById('ports-list');
  if (!box) return;
  try {
    const d = await api('/api/system/ports');
    const list = d.ports || [];
    const host = location.hostname || '127.0.0.1';
    box.innerHTML = `<table><tr><th></th><th>端口</th><th>监听</th><th>进程</th><th>标注</th><th>操作</th></tr>` +
      list.map(p => {
        const url = 'http://' + host + ':' + p.port;
        return `<tr>
        <td><button class="small ghost" data-act="port-fav" data-arg="${p.port}" data-on="${p.fav ? '1' : ''}"
          title="收藏常用端口">${p.fav ? '★' : '☆'}</button></td>
        <td><b>${p.port}</b></td><td>${esc(p.addr)}</td>
        <td>${esc(p.process)} <span class="muted">(${p.pid})</span></td>
        <td>${p.known ? `<span class="badge ok">${esc(p.known)}</span>` : ''}</td>
        <td class="actions">
          <a class="btn small" href="${esc(url)}" target="_blank" rel="noopener">打开</a>
          <button class="small ghost" data-act="port-probe" data-arg="${p.port}">探测</button>
        </td></tr>`;
      }).join('') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}

/* =============== 网站 (Caddy) =============== */
function pageSites() {
  mainEl().innerHTML = `
    <header class="top"><h2>网站管理</h2><div class="right">
      <span id="caddy-badge"></span>
      <button class="small" data-act="caddy-act" data-arg="install">安装 Caddy</button>
      <button class="small ghost" data-act="caddy-act" data-arg="start">启动</button>
      <button class="small ghost" data-act="caddy-act" data-arg="restart">重启</button>
      <button class="small ghost" data-act="caddy-act" data-arg="stop">停止</button>
      <button class="ghost small" data-act="sites-refresh">刷新</button></div></header>
    <div class="panel"><h4 id="site-form-title">添加站点</h4>
      <form data-form="site-add">
        <div class="form-row">
          <div><label>域名</label><input name="domain" required placeholder="app.yonghao.org 或 *:8443"></div>
          <div><label>类型</label>
            <select name="type" id="site-type">
              <option value="proxy">反向代理</option>
              <option value="static">静态站点</option>
            </select></div>
          <div><label id="site-target-label">目标 (127.0.0.1:端口)</label><input name="target" required placeholder="127.0.0.1:8083"></div>
        </div>
        <div class="form-row">
          <div><label>TLS</label>
            <select name="tls" id="site-tls">
              <option value="none">无 (仅 HTTP)</option>
              <option value="internal">internal (Caddy 自签 CA,仅内网可信)</option>
              <option value="files">证书文件 (acme.sh 签发)</option>
            </select></div>
          <div class="hidden site-tls-files"><label>证书路径</label><input name="cert" placeholder="~/.acme.sh/xx/fullchain.cer"></div>
          <div class="hidden site-tls-files"><label>私钥路径</label><input name="key" placeholder="~/.acme.sh/xx/xx.key"></div>
          <div style="flex:0;align-self:end"><label style="display:inline;vertical-align:middle">
            <input type="checkbox" name="enabled" checked style="width:auto"> 启用</label></div>
          <div style="flex:0;align-self:end"><button type="submit" id="site-submit">保存并同步 Caddy</button></div>
        </div>
      </form></div>
    <div id="sites-list" class="tbl-wrap"><div class="muted">加载中…</div></div>`;
  document.getElementById('site-type').addEventListener('change', e => {
    const proxy = e.target.value === 'proxy';
    document.getElementById('site-target-label').textContent = proxy ? '目标 (127.0.0.1:端口)' : '静态目录 (绝对路径)';
    e.target.closest('form').querySelector('input[name=target]').placeholder = proxy ? '127.0.0.1:8083' : '/Users/zyh/Sites/blog';
  });
  document.getElementById('site-tls').addEventListener('change', e => {
    document.querySelectorAll('.site-tls-files').forEach(el => el.classList.toggle('hidden', e.target.value !== 'files'));
  });
  loadSites();
}
async function loadSites() {
  const box = document.getElementById('sites-list');
  const badge = document.getElementById('caddy-badge');
  if (!box) return;
  try {
    const d = await api('/api/sites');
    if (badge) badge.innerHTML = d.caddy_installed
      ? (d.caddy_running ? '<span class="badge ok">Caddy 运行中</span>' : '<span class="badge warn">Caddy 已安装未启动</span>')
      : '<span class="badge">未安装 Caddy</span>';
    const list = d.sites || [];
    box.innerHTML = `<table><tr><th>域名</th><th>类型</th><th>目标</th><th>TLS</th><th>状态</th><th>操作</th></tr>` +
      (list.length ? list.map(s => `<tr>
        <td><b>${esc(s.domain)}</b></td>
        <td>${s.type === 'proxy' ? '反代' : '静态'}</td>
        <td><code>${esc(s.target)}</code></td>
        <td>${esc(s.tls || 'none')}</td>
        <td>${s.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge warn">停用</span>'}</td>
        <td class="actions">
          <button class="small ghost" data-act="site-edit" data-domain="${esc(s.domain)}" data-type="${esc(s.type)}"
            data-target="${esc(s.target)}" data-tls="${esc(s.tls || 'none')}" data-cert="${esc(s.cert || '')}"
            data-key="${esc(s.key || '')}" data-enabled="${s.enabled ? '1' : ''}">编辑</button>
          <button class="small danger" data-act="site-del" data-arg="${esc(s.domain)}">删除</button>
        </td></tr>`).join('') : '<tr><td colspan="6" class="muted">还没有站点;保存站点后面板会生成 Caddyfile 并热加载</td></tr>') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}

/* =============== 容器 =============== */
function pageContainers() {
  mainEl().innerHTML = `
    <header class="top"><h2>容器 (Apple container)</h2><div class="right">
      <button class="ghost small" data-act="ct-refresh">刷新</button></div></header>
    <div class="panel"><h4>创建容器</h4>
      <form data-form="ct-run">
        <div class="form-row">
          <div><label>名称</label><input name="name" required placeholder="my-redis"></div>
          <div style="flex:2"><label>镜像</label><input name="image" required placeholder="docker.m.daocloud.io/library/redis:7"></div>
        </div>
        <div class="form-row">
          <div><label>端口映射 (逗号分隔 host:container)</label><input name="ports" placeholder="6379:6379"></div>
          <div><label>环境变量 K=V (逗号分隔)</label><input name="env" placeholder="TZ=Asia/Shanghai"></div>
          <div><label>挂载 /host:/cont (逗号分隔)</label><input name="mounts" placeholder="/Users/zyh/data:/data"></div>
          <div style="flex:0;align-self:end"><button type="submit">创建</button></div>
        </div>
      </form></div>
    <div id="ct-list" class="tbl-wrap"><div class="muted">加载中…</div></div>
    <div class="panel" style="margin-top:14px"><h4>镜像列表</h4><div id="ct-images" class="muted">-</div></div>`;
  every(8000, () => { loadContainers(); });
  loadContainers(); loadImages();
}
async function loadContainers() {
  const box = document.getElementById('ct-list');
  if (!box) return;
  try {
    const d = await api('/api/container/list');
    const list = d.containers || [];
    box.innerHTML = `<table><tr><th>名称</th><th>镜像</th><th>状态</th><th>IP</th><th>端口</th><th>资源</th><th>操作</th></tr>` +
      (list.length ? list.map(c => {
        const running = c.state === 'running';
        return `<tr>
        <td><b>${esc(c.id)}</b></td><td><code>${esc(c.image)}</code></td>
        <td><span class="badge ${running ? 'ok' : ''}">${esc(c.state)}</span></td>
        <td>${esc(c.ip || '-')}</td>
        <td>${(c.ports || []).map(esc).join('<br>') || '-'}</td>
        <td class="muted">${c.cpus || '?'} CPU / ${c.mem_mb || '?'} MB</td>
        <td class="actions">
          ${running
            ? `<button class="small ghost" data-act="ct" data-arg="stop" data-name="${esc(c.id)}">停止</button>
               <button class="small ghost" data-act="ct" data-arg="restart" data-name="${esc(c.id)}">重启</button>`
            : `<button class="small" data-act="ct" data-arg="start" data-name="${esc(c.id)}">启动</button>`}
          <button class="small ghost" data-act="ct-logs" data-arg="${esc(c.id)}">日志</button>
          <button class="small danger" data-act="ct-del" data-arg="${esc(c.id)}">删除</button>
        </td></tr>`;
      }).join('') : '<tr><td colspan="7" class="muted">无容器</td></tr>') + `</table>`;
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}
async function loadImages() {
  const box = document.getElementById('ct-images');
  if (!box) return;
  try {
    const d = await api('/api/container/images');
    const imgs = d.images || [];
    box.innerHTML = imgs.length
      ? imgs.map(i => `<span class="badge">${esc(i.reference || JSON.stringify(i))}</span>`).join(' ')
      : '无镜像';
  } catch (e) { box.textContent = '镜像列表获取失败: ' + e.message; }
}
function watchLogs(id) {
  window._ctLogId = id;
  openModal('容器日志: ' + id, '<pre class="log" id="ct-log-pre">加载中…</pre>' +
    '<div style="margin-top:8px"><button class="small ghost" data-act="ct-logs-refresh">刷新</button></div>');
  refreshLogs();
}
async function refreshLogs() {
  const id = window._ctLogId;
  const pre = document.getElementById('ct-log-pre');
  if (!id || !pre) return;
  try {
    const d = await api('/api/container/logs?id=' + encodeURIComponent(id));
    pre.textContent = (d.logs || []).join('\n') || '(无日志)';
    pre.scrollTop = pre.scrollHeight;
  } catch (e) { pre.textContent = e.message; }
}

/* =============== 应用商店 =============== */
function pageApps() {
  mainEl().innerHTML = `
    <header class="top"><h2>应用商店</h2><div class="right">
      <button class="ghost small" data-act="apps-refresh">刷新</button></div></header>
    <div class="panel"><h4>容器应用</h4><div class="cards" id="apps-ctr"><div class="muted">加载中…</div></div></div>
    <div class="panel"><h4>Homebrew 应用</h4><div class="cards" id="apps-brew"><div class="muted">加载中…</div></div></div>
    <div class="panel"><h4>Homebrew 软件包管理</h4>
      <form data-form="brew-search"><div class="form-row">
        <div><input name="q" placeholder="搜索 brew 软件包,如 redis" required></div>
        <div style="flex:0;align-self:end"><button type="submit">搜索</button></div>
      </div></form>
      <div id="brew-results" style="margin-top:10px"></div>
      <hr class="hr">
      <div class="flex" style="margin-bottom:8px">
        <h4 style="margin:0">已安装 (<span id="brew-count">-</span>)</h4>
        <button class="small ghost" data-act="brew-installed-refresh">刷新列表</button>
      </div>
      <div id="brew-installed" class="muted">加载中…</div>
    </div>`;
  loadApps(); loadBrewInstalled();
}
async function loadApps() {
  const brewBox = document.getElementById('apps-brew');
  const ctrBox = document.getElementById('apps-ctr');
  if (!brewBox) return;
  try {
    const d = await api('/api/apps');
    ctrBox.innerHTML = (d.container || []).map(a => `
      <div class="card"><h3>${esc(a.name)}</h3>
        <div class="muted" style="min-height:34px">${esc(a.desc)}</div>
        <div class="muted" style="margin:6px 0">端口: ${(a.ports || []).join(', ') || '-'}</div>
        ${a.installed
          ? '<span class="badge ok">已安装</span>'
          : `<button class="small" data-act="app-install-ctr" data-arg="${esc(a.id)}" data-name="${esc(a.name)}">安装</button>`}
      </div>`).join('') || '<div class="muted">无</div>';
    brewBox.innerHTML = (d.brew || []).map(a => `
      <div class="card"><h3>${esc(a.name)}</h3>
        <div class="muted" style="min-height:34px">${esc(a.desc)}</div>
        <div class="muted" style="margin:6px 0">${a.service ? 'brew 服务: ' + esc(a.service) : '工具类,无服务'}</div>
        ${a.installed
          ? '<span class="badge ok">已安装</span>'
          : `<button class="small" data-act="app-install-brew" data-arg="${esc(a.id)}" data-name="${esc(a.name)}">安装</button>`}
      </div>`).join('') || '<div class="muted">无</div>';
  } catch (e) {
    brewBox.innerHTML = ctrBox.innerHTML = `<div class="muted">${esc(e.message)}</div>`;
  }
}
async function loadBrewInstalled() {
  const box = document.getElementById('brew-installed');
  const cnt = document.getElementById('brew-count');
  if (!box) return;
  try {
    const d = await api('/api/brew/installed');
    const pkgs = d.packages || [];
    if (cnt) cnt.textContent = pkgs.length;
    box.innerHTML = `<table><tr><th>包名</th><th>版本</th><th>操作</th></tr>` +
      pkgs.map(p => `<tr><td><b>${esc(p.name)}</b></td><td>${esc(p.version)}</td>
        <td class="actions">
          <button class="small ghost" data-act="brew-info" data-arg="${esc(p.name)}">详情</button>
          <button class="small ghost" data-act="brew-upgrade" data-arg="${esc(p.name)}">升级</button>
          <button class="small danger" data-act="brew-uninstall" data-arg="${esc(p.name)}">卸载</button>
        </td></tr>`).join('') + `</table>`;
  } catch (e) { box.innerHTML = esc(e.message); }
}
async function brewSearch(q) {
  const box = document.getElementById('brew-results');
  box.innerHTML = '<div class="muted">搜索中…</div>';
  try {
    const d = await api('/api/brew/search?q=' + encodeURIComponent(q));
    const rs = d.results || [];
    box.innerHTML = rs.length
      ? `<table><tr><th>包名</th><th>操作</th></tr>` + rs.map(n => `<tr><td><b>${esc(n)}</b></td>
          <td class="actions">
            <button class="small ghost" data-act="brew-info" data-arg="${esc(n)}">详情</button>
            <button class="small" data-act="brew-install" data-arg="${esc(n)}">安装</button>
          </td></tr>`).join('') + `</table>`
      : '<div class="muted">无结果</div>';
  } catch (e) { box.innerHTML = `<div class="muted">${esc(e.message)}</div>`; }
}
function brewInfo(name) {
  api('/api/brew/info?name=' + encodeURIComponent(name))
    .then(d => openModal('brew info: ' + name, '<pre class="log">' + esc(d.info) + '</pre>'))
    .catch(e => toast(e.message, 'err'));
}

/* =============== 终端 =============== */
let term = null, termWS = null;
const termRO = new ResizeObserver(() => fitTerm());
function disconnectTerm() {
  if (termWS) { try { termWS.close(); } catch (e) {} termWS = null; }
  if (term) { try { term.dispose(); } catch (e) {} term = null; }
}
async function pageTerminal() {
  mainEl().innerHTML = `
    <header class="top"><h2>终端</h2><div class="right">
      <select id="term-preset">
        <option value="">— 快捷命令 —</option>
        <option value="ssh root@192.168.31.1">SSH OpenWrt 路由器</option>
        <option value="crontab -l">查看 crontab</option>
        <option value="container ls --all">列出容器</option>
        <option value="brew services list">brew 服务</option>
        <option value="launchctl list | head -30">launchd 列表</option>
      </select>
      <button class="ghost small" data-act="term-reconnect">重连</button>
    </div></header>
    <div class="termbox" id="termbox"></div>`;
  document.getElementById('term-preset').addEventListener('change', e => {
    const cmd = e.target.value;
    if (!cmd) return;
    e.target.value = '';
    if (termWS && termWS.readyState === 1) { termWS.send(cmd); termWS.send('\r'); }
    else toast('终端未连接', 'err');
  });
  try {
    await loadScript('/assets/xterm.min.js');
  } catch (e) {
    document.getElementById('termbox').innerHTML = '<div class="muted" style="padding:20px">xterm.js 加载失败: ' + esc(e.message) + '</div>';
    return;
  }
  connectTerm();
}
function connectTerm() {
  const box = document.getElementById('termbox');
  if (!box || typeof Terminal === 'undefined') return;
  disconnectTerm();
  term = new Terminal({
    cursorBlink: true, fontSize: 13, fontFamily: 'Menlo, Monaco, monospace',
    theme: { background: '#0a0e18', foreground: '#c9d6f2', cursor: '#4f8cff' },
    scrollback: 5000,
  });
  term.open(box);
  termRO.observe(box);
  const proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
  termWS = new WebSocket(proto + location.host + '/api/terminal');
  termWS.binaryType = 'arraybuffer';
  term.onData(d => { if (termWS && termWS.readyState === 1) termWS.send(d); });
  term.onResize(({ cols, rows }) => {
    if (termWS && termWS.readyState === 1) termWS.send(JSON.stringify({ type: 'resize', cols, rows }));
  });
  termWS.onopen = () => { term.focus(); fitTerm(); };
  termWS.onmessage = ev => {
    if (term) term.write(typeof ev.data === 'string' ? ev.data : new Uint8Array(ev.data));
  };
  termWS.onclose = () => { if (term) term.write('\r\n\x1b[33m[连接已断开,点"重连"]\x1b[0m\r\n'); };
}
function fitTerm() {
  const box = document.getElementById('termbox');
  if (!box || !term) return;
  const cols = Math.floor(box.clientWidth / 9);
  const rows = Math.floor(box.clientHeight / 17);
  if (cols > 4 && rows > 2 && (term.cols !== cols || term.rows !== rows)) {
    try { term.resize(cols, rows); } catch (e) {}
  }
}

/* =============== AI =============== */
function pageAI() {
  mainEl().innerHTML = `
    <header class="top"><h2>AI 助手</h2><div class="right">
      <button class="ghost small" data-act="ai-clear">清空会话</button></div></header>
    <details class="panel" id="ai-settings-panel">
      <summary style="cursor:pointer;font-size:13px">AI 配置 (DeepSeek / OpenAI 兼容)</summary>
      <form data-form="ai-settings" style="margin-top:10px">
        <div class="form-row">
          <div><label>Base URL</label><input name="base_url" id="ai-base" placeholder="https://api.deepseek.com"></div>
          <div><label>Model</label><input name="model" id="ai-model" placeholder="deepseek-chat"></div>
          <div><label>API Key <span class="muted" id="ai-key-hint"></span></label><input name="key" type="password" placeholder="留空则不修改"></div>
          <div style="flex:0;align-self:end"><button type="submit">保存配置</button></div>
        </div>
      </form>
    </details>
    <div class="chat">
      <div class="msgs" id="ai-msgs"><div class="msg ai">你好!我是接入在面板里的 AI 运维助手。勾选下方"附带系统快照"可以让我基于服务器实时状态回答。</div></div>
      <form class="input" data-form="ai-chat">
        <label style="display:inline;white-space:nowrap;align-self:center">
          <input type="checkbox" id="ai-diag" checked style="width:auto"> 附带系统快照</label>
        <textarea id="ai-input" placeholder="问点什么,比如:内存占用正常吗?哪些服务在跑?"></textarea>
        <button type="submit" id="ai-send" style="align-self:flex-end">发送</button>
      </form>
    </div>`;
  api('/api/ai/settings').then(d => {
    document.getElementById('ai-base').value = d.base_url || '';
    document.getElementById('ai-model').value = d.model || '';
    document.getElementById('ai-key-hint').textContent = d.key_set ? ('已设置 (' + d.key_masked + ')') : '未设置';
  }).catch(() => {});
}
function addMsg(role, text) {
  const box = document.getElementById('ai-msgs');
  const el = document.createElement('div');
  el.className = 'msg ' + role;
  el.textContent = text;
  box.appendChild(el);
  box.scrollTop = box.scrollHeight;
  return el;
}
async function aiSend(text) {
  const el = addMsg('ai', '…');
  const diagnose = document.getElementById('ai-diag').checked;
  try {
    const res = await fetch('/api/ai/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Panel-CSRF': ME.csrf },
      body: JSON.stringify({ messages: AI_HIST.slice(-12), diagnose }),
    });
    if (!res.ok) {
      let d = null;
      try { d = await res.json(); } catch (e) {}
      throw new Error((d && d.error) || ('HTTP ' + res.status));
    }
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = '', acc = '';
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split('\n');
      buf = lines.pop();
      for (const line of lines) {
        const t = line.trim();
        if (!t.startsWith('data:')) continue;
        const payload = t.slice(5).trim();
        if (payload === '[DONE]') continue;
        try {
          const j = JSON.parse(payload);
          const delta = (j.choices && j.choices[0] && j.choices[0].delta && j.choices[0].delta.content) || '';
          if (delta) { acc += delta; el.textContent = acc; }
        } catch (e) { /* 忽略心跳 */ }
      }
    }
    if (!acc) el.textContent = '(空回复)';
    else AI_HIST.push({ role: 'assistant', content: acc });
  } catch (e) { el.textContent = '出错: ' + e.message; }
}

/* =============== 设置 =============== */
function pageSettings() {
  mainEl().innerHTML = `
    <header class="top"><h2>设置</h2></header>
    <div class="panel"><h4>修改密码</h4>
      <form data-form="pass-change"><div class="form-row">
        <div><label>旧密码</label><input name="old" type="password" required></div>
        <div><label>新密码 (至少 10 位)</label><input name="new" type="password" required minlength="10"></div>
        <div style="flex:0;align-self:end"><button type="submit">修改</button></div>
      </div></form></div>
    <div class="panel"><h4>安全 / 网络</h4>
      <div class="muted" style="margin-bottom:8px" id="sec-entrance-hint"></div>
      <form data-form="security"><div class="form-row">
        <div><label>入口路径 (secret 前缀,防扫描)</label><input name="entrance" id="sec-entrance"></div>
        <div><label>监听地址 (重启生效)</label><input name="listen" id="sec-listen" placeholder=":9000"></div>
        <div><label>会话时长(小时)</label><input name="session_hours" id="sec-hours" type="number" min="1" max="720"></div>
      </div>
      <div class="form-row" style="margin-top:8px">
        <div><label>TLS 证书路径 (与私钥都填才启用 HTTPS)</label><input name="tls_cert" id="sec-cert" placeholder="/Users/zyh/.acme.sh/xx/fullchain.cer"></div>
        <div><label>TLS 私钥路径</label><input name="tls_key" id="sec-key"></div>
        <div><label>HTTP 跳转端口 (可选)</label><input name="http_port" id="sec-httpport" placeholder=":8000"></div>
      </div>
      <div style="margin-top:8px"><label>IP 白名单 (每行一个,支持前缀如 192.168.31. ;留空=不限制)</label>
        <textarea name="ip_allowlist" id="sec-allow" rows="3" style="font-family:ui-monospace,monospace"></textarea></div>
      <div style="margin-top:10px"><button type="submit">保存安全设置</button></div>
      </form></div>
    <div class="panel"><h4>已知服务 (端口页快捷打开标注)</h4>
      <div class="muted" style="margin-bottom:6px">每行: <code>端口 名称 URL</code>,如 <code>8083 Nextcloud http://192.168.64.4:8083</code></div>
      <textarea id="known-editor" rows="5" style="font-family:ui-monospace,monospace" placeholder="8083 Nextcloud http://192.168.64.4:8083
8080 rclone http://127.0.0.1:8080"></textarea>
      <div style="margin-top:8px"><button data-act="known-save">保存已知服务</button></div></div>
    <div class="panel"><h4>审计日志</h4>
      <div class="muted" style="margin-bottom:8px">记录登录、终端打开与所有运维操作,文件 data/audit.log</div>
      <button class="ghost small" data-act="audit-view">查看最近 500 条</button></div>`;
  api('/api/settings').then(d => {
    document.getElementById('sec-entrance').value = d.entrance || '';
    document.getElementById('sec-listen').value = d.listen || ':9000';
    document.getElementById('sec-hours').value = d.session_hours || 24;
    document.getElementById('sec-cert').value = d.tls_cert || '';
    document.getElementById('sec-key').value = d.tls_key || '';
    document.getElementById('sec-httpport').value = d.http_port || '';
    document.getElementById('sec-allow').value = (d.ip_allowlist || []).join('\n');
    document.getElementById('sec-entrance-hint').innerHTML =
      '当前入口: <code>' + esc(location.origin + '/' + (d.entrance || '') + '/</code>') +
      ' — 修改入口后请用新地址访问;其他路径会 302 跳转到入口';
    const ed = document.getElementById('known-editor');
    ed.value = (d.known_services || []).map(k => [k.port, k.name, k.url].join(' ')).join('\n');
  }).catch(e => toast(e.message, 'err'));
}
function viewAudit() {
  api('/api/audit').then(d => {
    const rows = (d.entries || []).map(l => {
      try { const j = JSON.parse(l); return j.time + '  ' + j.event + '  ' + j.detail; }
      catch (e) { return l; }
    });
    openModal('审计日志 (最近 ' + rows.length + ' 条)', '<pre class="log" style="max-height:60vh">' + esc(rows.join('\n')) + '</pre>');
  }).catch(e => toast(e.message, 'err'));
}

/* =============== 事件委托 =============== */
function wireEvents() {
  document.addEventListener('click', async e => {
    const el = e.target.closest('[data-act]');
    if (!el) return;
    const act = el.dataset.act, arg = el.dataset.arg;
    const name = el.dataset.name || '';
    try {
      switch (act) {
        case 'close-modal': closeModal(); break;
        case 'logout': await api('/api/logout', 'POST', {}); location.hash = ''; location.reload(); break;
        /* 服务 */
        case 'svc-refresh': loadBrewServices(); loadLaunchd(); break;
        case 'brewsvc': {
          const d = await api('/api/services/brew/action', 'POST', { name, action: arg });
          toast((d.output || '完成').split('\n')[0], 'ok'); loadBrewServices(); break;
        }
        case 'launchd': {
          const d = await api('/api/services/launchd/action', 'POST', { label: name, action: arg });
          toast((d.output || '完成').split('\n')[0], 'ok'); loadLaunchd(); break;
        }
        /* 监控 */
        case 'mon-refresh': loadMonitors(); break;
        case 'mon-check': {
          const d = await api('/api/monitors/check', 'POST', { id: arg });
          toast(d.monitor.last_ok ? nameOrId(d.monitor) + ' 正常 (' + d.monitor.latency_ms.toFixed(0) + 'ms)'
            : nameOrId(d.monitor) + ' 异常: ' + d.monitor.last_error, d.monitor.last_ok ? 'ok' : 'err');
          loadMonitors(); break;
        }
        case 'mon-toggle': await api('/api/monitors/toggle', 'POST', { id: arg, enabled: !el.dataset.on }); loadMonitors(); break;
        case 'mon-del':
          if (!confirmGo('删除监控项 "' + name + '"?')) return;
          await api('/api/monitors/delete', 'POST', { id: arg }); loadMonitors(); break;
        case 'mon-act': await api('/api/monitors/action', 'POST', { id: arg, action: el.dataset.a }); toast('已执行 ' + el.dataset.a, 'ok'); loadMonitors(); break;
        /* cron */
        case 'cron-refresh': loadCron(); break;
        case 'cron-toggle': await api('/api/cron/toggle', 'POST', { index: +arg }); loadCron(); break;
        case 'cron-edit': {
          const f = document.querySelector('form[data-form=cron-add]');
          f.querySelector('input[name=index]').value = arg;
          f.querySelector('input[name=schedule]').value = el.dataset.sched;
          f.querySelector('input[name=command]').value = el.dataset.cmd;
          f.querySelector('input[name=name]').value = el.dataset.name;
          document.getElementById('cron-form-title').textContent = '编辑任务 # ' + arg;
          document.getElementById('cron-submit').textContent = '保存修改';
          window.scrollTo({ top: 0, behavior: 'smooth' });
          break;
        }
        case 'cron-del':
          if (!confirmGo('删除该计划任务行?')) return;
          await api('/api/cron/delete', 'POST', { index: +arg }); loadCron(); break;
        /* 端口 */
        case 'ports-refresh': loadPorts(); break;
        case 'port-fav': await api('/api/system/ports/favorite', 'POST', { port: +arg, fav: !el.dataset.on }); loadPorts(); break;
        case 'port-probe': {
          toast('探测 ' + arg + ' …');
          const d = await api('/api/system/probe?port=' + arg);
          toast(d.http_ok ? '端口 ' + arg + ' HTTP ' + d.status + ' (' + d.ms.toFixed(0) + 'ms)'
            : '端口 ' + arg + ' 探测失败: ' + d.detail, d.http_ok ? 'ok' : 'err');
          break;
        }
        /* 网站 */
        case 'sites-refresh': loadSites(); break;
        case 'caddy-act': {
          const d = await api('/api/sites/caddy', 'POST', { action: arg });
          if (d.job_id) { watchJob(d.job_id, 'brew install caddy'); }
          else toast((d.output || '完成').split('\n')[0], 'ok');
          setTimeout(loadSites, 1500); break;
        }
        case 'site-edit': {
          const f = document.querySelector('form[data-form=site-add]');
          f.querySelector('input[name=domain]').value = el.dataset.domain;
          f.querySelector('select[name=type]').value = el.dataset.type;
          f.querySelector('input[name=target]').value = el.dataset.target;
          f.querySelector('select[name=tls]').value = el.dataset.tls;
          f.querySelector('select[name=tls]').dispatchEvent(new Event('change'));
          f.querySelector('input[name=cert]').value = el.dataset.cert;
          f.querySelector('input[name=key]').value = el.dataset.key;
          f.querySelector('input[name=enabled]').checked = !!el.dataset.enabled;
          document.getElementById('site-form-title').textContent = '编辑站点 ' + el.dataset.domain;
          document.getElementById('site-submit').textContent = '保存修改';
          window.scrollTo({ top: 0, behavior: 'smooth' });
          break;
        }
        case 'site-del':
          if (!confirmGo('删除站点 "' + arg + '"? 将同步 Caddy 配置。')) return;
          await api('/api/sites/delete', 'POST', { domain: arg }); loadSites(); break;
        /* 容器 */
        case 'ct-refresh': loadContainers(); loadImages(); break;
        case 'ct': {
          const d = await api('/api/container/action', 'POST', { id: name, action: arg });
          toast((d.output || '完成').split('\n')[0], 'ok'); setTimeout(loadContainers, 800); break;
        }
        case 'ct-logs': watchLogs(name); break;
        case 'ct-logs-refresh': refreshLogs(); break;
        case 'ct-del':
          if (!confirmGo('强制删除容器 "' + name + '"?数据卷需手动清理。')) return;
          await api('/api/container/action', 'POST', { id: name, action: 'delete' }, { 'X-Panel-Confirm': 'yes' });
          toast('已删除', 'ok'); setTimeout(loadContainers, 800); break;
        /* 应用商店 */
        case 'apps-refresh': loadApps(); loadBrewInstalled(); break;
        case 'app-install-brew': {
          const d = await api('/api/apps/install-brew', 'POST', { id: arg });
          watchJob(d.job_id, '安装 ' + name); break;
        }
        case 'app-install-ctr': {
          const d = await api('/api/apps/install-container', 'POST', { id: arg });
          watchJob(d.job_id, '创建容器 ' + name); break;
        }
        case 'brew-installed-refresh': loadBrewInstalled(); break;
        case 'brew-install': {
          const d = await api('/api/brew/action', 'POST', { name: arg, action: 'install' });
          watchJob(d.job_id, 'brew install ' + arg); break;
        }
        case 'brew-upgrade': {
          const d = await api('/api/brew/action', 'POST', { name: arg, action: 'upgrade' });
          watchJob(d.job_id, 'brew upgrade ' + arg); break;
        }
        case 'brew-uninstall': {
          if (!confirmGo('卸载 ' + arg + '?')) return;
          const d = await api('/api/brew/action', 'POST', { name: arg, action: 'uninstall' }, { 'X-Panel-Confirm': 'yes' });
          watchJob(d.job_id, 'brew uninstall ' + arg); break;
        }
        case 'brew-info': brewInfo(arg); break;
        /* 终端 */
        case 'term-reconnect': connectTerm(); break;
        /* AI */
        case 'ai-clear': AI_HIST = [];
          document.getElementById('ai-msgs').innerHTML = '<div class="msg ai">会话已清空。</div>';
          break;
        /* 设置 */
        case 'known-save': await saveKnown(); break;
        case 'audit-view': viewAudit(); break;
      }
    } catch (err) { toast(err.message, 'err'); }
  });

  document.addEventListener('submit', async e => {
    const f = e.target.closest('form[data-form]');
    if (!f) return;
    e.preventDefault();
    const kind = f.dataset.form;
    const fd = new FormData(f);
    const val = n => (fd.get(n) || '').toString().trim();
    try {
      switch (kind) {
        case 'login': {
          const d = await api('/api/login', 'POST', { username: val('username'), password: fd.get('password') });
          ME = { user: d.user, csrf: d.csrf };
          enterPanel();
          break;
        }
        case 'setup': {
          const creds = { username: val('username'), password: String(fd.get('password')) };
          const d = await api('/api/setup', 'POST', creds);
          toast('初始化完成!入口路径 /' + d.entrance + '/ ,请收藏', 'ok');
          const d2 = await api('/api/login', 'POST', creds);
          ME = { user: d2.user, csrf: d2.csrf };
          enterPanel();
          break;
        }
        case 'mon-add':
          await api('/api/monitors/add', 'POST', {
            name: val('name'), type: val('type'), target: val('target'), enabled: true,
          });
          toast('已添加', 'ok'); f.reset(); loadMonitors();
          break;
        case 'cron-add': {
          const idx = parseInt(val('index'), 10);
          await api('/api/cron/save', 'POST', {
            index: isNaN(idx) ? -1 : idx, schedule: val('schedule'), command: val('command'), name: val('name'),
          });
          toast('已写回 crontab', 'ok');
          f.reset();
          f.querySelector('input[name=index]').value = -1;
          document.getElementById('cron-form-title').textContent = '新增任务';
          document.getElementById('cron-submit').textContent = '添加';
          loadCron();
          break;
        }
        case 'site-add': {
          const body = {
            domain: val('domain'), type: val('type'), target: val('target'),
            tls: val('tls'), cert: val('cert'), key: val('key'), enabled: f.querySelector('input[name=enabled]').checked,
          };
          await api('/api/sites/save', 'POST', body);
          toast('站点已保存并同步 Caddy', 'ok');
          f.reset();
          document.getElementById('site-form-title').textContent = '添加站点';
          document.getElementById('site-submit').textContent = '保存并同步 Caddy';
          document.querySelectorAll('.site-tls-files').forEach(el => el.classList.add('hidden'));
          loadSites();
          break;
        }
        case 'ct-run': {
          const split = s => s.split(',').map(x => x.trim()).filter(Boolean);
          const d = await api('/api/container/run', 'POST', {
            name: val('name'), image: val('image'),
            ports: split(val('ports')), env: split(val('env')), mounts: split(val('mounts')),
          });
          toast('创建中…', 'ok');
          f.reset();
          watchJob(d.job_id, 'container run ' + val('name'));
          setTimeout(loadContainers, 3000);
          break;
        }
        case 'brew-search': brewSearch(val('q')); break;
        case 'ai-chat': {
          const ta = document.getElementById('ai-input');
          const text = ta.value.trim();
          if (!text) return;
          ta.value = '';
          addMsg('user', text);
          AI_HIST.push({ role: 'user', content: text });
          document.getElementById('ai-send').disabled = true;
          await aiSend(text);
          document.getElementById('ai-send').disabled = false;
          break;
        }
        case 'ai-settings':
          await api('/api/ai/settings/save', 'POST', {
            base_url: val('base_url'), model: val('model'), key: val('key'),
          });
          toast('AI 配置已保存', 'ok');
          f.querySelector('input[name=key]').value = '';
          api('/api/ai/settings').then(d => {
            document.getElementById('ai-key-hint').textContent = d.key_set ? ('已设置 (' + d.key_masked + ')') : '未设置';
          }).catch(() => {});
          break;
        case 'pass-change':
          await api('/api/password', 'POST', { old: val('old'), new: val('new') });
          toast('密码已修改', 'ok');
          f.reset();
          break;
        case 'security': {
          const allow = val('ip_allowlist').split('\n').map(x => x.trim()).filter(Boolean);
          const hours = parseInt(val('session_hours'), 10) || 24;
          await api('/api/settings/save', 'POST', {
            entrance: val('entrance'), listen: val('listen'),
            tls_cert: val('tls_cert'), tls_key: val('tls_key'), http_port: val('http_port'),
            ip_allowlist: allow, session_hours: hours,
          });
          toast('已保存 (监听/TLS/会话需重启面板生效)', 'ok');
          break;
        }
      }
    } catch (err) {
      if (kind === 'login') document.getElementById('login-err').textContent = err.message;
      else if (kind === 'setup') document.getElementById('setup-err').textContent = err.message;
      else toast(err.message, 'err');
    }
  });
}
function nameOrId(m) { return m.name || m.id; }

/* =============== Job 进度弹窗 =============== */
function watchJob(id, name) {
  openModal('任务: ' + esc(name), '<pre class="log" id="job-log">…</pre><div class="muted" id="job-status" style="margin-top:8px">运行中…</div>');
  let since = 0;
  const t = setInterval(async () => {
    try {
      const d = await api('/api/jobs/' + id + '?since=' + since);
      const pre = document.getElementById('job-log');
      if (!pre) { clearInterval(t); return; }
      if (d.log && d.log.length) {
        pre.textContent += (pre.textContent ? '\n' : '') + d.log.join('\n');
        pre.scrollTop = pre.scrollHeight;
      }
      since = d.total || since;
      const st = document.getElementById('job-status');
      if (d.status === 'running') st.textContent = '运行中…';
      else {
        st.textContent = d.status === 'done' ? '✓ 完成 (exit 0)' : '✗ 失败 (exit ' + d.exit + ')';
        clearInterval(t);
        toast('任务 ' + name + ' ' + (d.status === 'done' ? '完成' : '失败'), d.status === 'done' ? 'ok' : 'err');
      }
    } catch (e) { clearInterval(t); }
  }, 1000);
}

/* =============== 已知服务保存 =============== */
async function saveKnown() {
  const lines = document.getElementById('known-editor').value.split('\n')
    .map(l => l.trim()).filter(Boolean);
  const list = [];
  for (const l of lines) {
    const t = l.split(/\s+/);
    const port = parseInt(t[0], 10);
    if (!port || port < 1 || port > 65535) { toast('端口无效: ' + l, 'err'); return; }
    list.push({ port, name: t[1] || ('端口 ' + t[0]), url: t.slice(2).join(' ') });
  }
  await api('/api/settings/save', 'POST', { known_services: list });
  toast('已知服务已保存 (' + list.length + ' 条)', 'ok');
}

/* =============== 启动 =============== */
(async function init() {
  wireEvents();
  window.addEventListener('hashchange', navigate);
  try {
    const d = await api('/api/me');
    ME = { user: d.user, csrf: d.csrf };
    enterPanel();
  } catch (e) {
    try {
      const st = await (await fetch('/api/setup-status')).json();
      if (st.needs_setup) showSetup(); else showLogin();
    } catch (e2) { showLogin(); }
  }
})();
