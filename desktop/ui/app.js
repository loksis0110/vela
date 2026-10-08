"use strict";

const token = new URLSearchParams(location.hash.slice(1)).get("t") || sessionStorage.getItem("vela-t") || "";
try { sessionStorage.setItem("vela-t", token); } catch (_) {}
history.replaceState(null, "", "/");

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];

let snap = null;          // full state from backend
let status = { state: "disconnected" };
let pings = {};
let reasons = {};          // node id -> why it failed
let probe = null;          // {done,total} while servers are being checked
let sortByPing = false;
let viewProfile = null;   // profile shown in the servers sheet
let favorites = new Set();
const filters = { q: "", fav: false, ok: false, proto: "", country: "" };
let traffic = [];          // [{t, up, down}] for the graph
let editRules = [];        // routing rules being edited in settings
let editApps = [];         // split tunneling programs
let ipBusy = false;

// ------------------------------------------------------------------ api
async function api(path, body) {
  const res = await fetch("/api/" + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Vela-Token": token },
    body: JSON.stringify(body || {}),
  });
  let data = {};
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) throw new Error(data.error || "Ошибка " + res.status);
  return data;
}

function connectEvents() {
  const es = new EventSource("/api/events?t=" + encodeURIComponent(token));
  es.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "state") {
      snap = msg.data;
      status = snap.status;
      pings = Object.assign({}, snap.pings || {}, pings);
      reasons = Object.assign({}, snap.reasons || {}, reasons);
      favorites = new Set(snap.favorites || []);
      if (!traffic.length && snap.history) traffic = snap.history.slice();
      applyTheme(snap.theme);
      renderAll();
    } else if (msg.type === "status") {
      const was = status.state;
      status = msg.data;
      if (status.state === "connected") {
        traffic.push({ t: Date.now() / 1000, up: status.up || 0, down: status.down || 0 });
        if (traffic.length > 300) traffic.splice(0, traffic.length - 300);
      } else if (status.state === "connecting") {
        traffic = [];
      }
      if (was !== status.state && (status.state === "connected" || status.state === "disconnected")) {
        setTimeout(refreshIP, status.state === "connected" ? 1500 : 300);
      }
      renderGraph();
      renderStatus();
      renderCurrent();
      if (!$("#servers").hidden) renderNodes();
    } else if (msg.type === "ping") {
      const d = msg.data;
      pings[d.id] = d.ms;
      if (d.reason) reasons[d.id] = d.reason; else delete reasons[d.id];
      renderPings();
      renderCurrent();
    } else if (msg.type === "speed") {
      onSpeed(msg.data);
    } else if (msg.type === "probe") {
      const d = msg.data;
      probe = d.done < d.total ? d : null;
      if (d.error) toast("Проверка не удалась: " + d.error, true);
      renderProbe();
      if (!probe && d.total) {
        const p = snap && snap.profiles.find((x) => x.id === viewProfile);
        if (p) {
          const ok = p.nodes.filter((n) => pings[n.id] > 0).length;
          toast(`Работают ${ok} из ${p.nodes.length}. Отчёт сохранён`);
        }
        if (sortByPing) renderNodes();
      }
    }
  };
  es.onerror = () => { es.close(); setTimeout(connectEvents, 1500); };
}

// ------------------------------------------------------------------ helpers
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function splitFlag(name) {
  const m = /^\s*((?:\uD83C[\uDDE6-\uDDFF]){2})\s*/.exec(name || "");
  if (m) return { flag: m[1], name: name.slice(m[0].length) || name };
  return { flag: null, name: name || "" };
}

function flagHTML(name) {
  const { flag, name: rest } = splitFlag(name);
  if (flag) return esc(flag);
  const letter = (rest.trim()[0] || "✦").toUpperCase();
  return esc(letter);
}

function fmtBytes(n) {
  if (!n || n < 0) return "0 Б";
  const u = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(n < 10 ? 1 : 0)).toString().replace(".", ",") + " " + u[i];
}
const fmtSpeed = (n) => fmtBytes(n) + "/с";

function fmtDuration(sec) {
  sec = Math.max(0, sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  const p = (x) => String(x).padStart(2, "0");
  return (h ? h + ":" : "") + p(m) + ":" + p(s);
}

function fmtDate(unix) {
  return new Date(unix * 1000).toLocaleDateString("ru-RU", { day: "numeric", month: "long", year: "numeric" });
}

function pingInfo(ms) {
  if (ms === undefined) return { text: "", cls: "" };
  if (ms === 0) return { text: "…", cls: "" };
  if (ms < 0) return { text: "не работает", cls: "bad" };
  if (ms === -2) return { text: "после подключения", cls: "" };
  const cls = ms < 150 ? "good" : ms < 300 ? "ok" : ms < 600 ? "slow" : "bad";
  return { text: ms + " мс", cls };
}

let toastTimer;
function toast(text, isError) {
  const t = $("#toast");
  t.textContent = text;
  t.className = "toast" + (isError ? " error" : "");
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), isError ? 5000 : 2600);
}

const activeProfile = () => snap && snap.profiles.find((p) => p.id === snap.activeProfile);
const findNode = (p, id) => p && p.nodes.find((n) => n.id === id);

// ------------------------------------------------------------------ render
function renderAll() {
  renderStatus();
  renderCurrent();
  renderRouting();
  if (!$("#servers").hidden) renderServers();
  fillSettings();
  $("#version").textContent = snap.version;
}

const stateText = {
  disconnected: "Не подключено",
  connecting: "Подключение…",
  connected: "Защищено",
  disconnecting: "Отключение…",
};

function renderStatus() {
  const hero = $("#hero");
  hero.dataset.state = status.state;
  $("#statusText").textContent = stateText[status.state] || "";
  $("#toggleLabel").textContent =
    status.state === "connected" ? "Отключить" :
    status.state === "connecting" ? "Отмена" :
    status.state === "disconnecting" ? "Отключение" : "Подключить";
  $("#toggle").setAttribute("aria-label", $("#toggleLabel").textContent);
  const live = status.state === "connected";
  $("#speed").hidden = !live;
  if (live) {
    $("#down").textContent = fmtSpeed(status.down);
    $("#up").textContent = fmtSpeed(status.up);
  }
  tickTimer();

  const banner = $("#banner");
  if (status.error) {
    banner.hidden = false;
    $("#bannerText").textContent = status.error;
    const act = $("#bannerAction");
    if (status.errorCode === "need_admin") {
      act.hidden = false;
      act.textContent = "Перезапустить";
      act.onclick = elevate;
    } else if (status.errorCode === "server_unreachable" && window.innerWidth < 900) {
      act.hidden = false;
      act.textContent = "Выбрать сервер";
      act.onclick = () => openSheet("servers");
    } else {
      act.hidden = true;
    }
  } else if (status.notice) {
    banner.hidden = false;
    banner.classList.add("notice");
    $("#bannerText").textContent = status.notice;
    $("#bannerAction").hidden = true;
  } else {
    banner.hidden = true;
  }
  if (status.error) banner.classList.remove("notice");
}

function tickTimer() {
  const t = $("#timer");
  if (status.state === "connected" && status.since) {
    const total = fmtBytes((status.upTotal || 0) + (status.downTotal || 0));
    t.textContent = fmtDuration(Math.floor(Date.now() / 1000) - status.since) + ", передано " + total;
  } else if (status.state === "disconnected" && snap && snap.profiles.length === 0) {
    t.textContent = "Добавьте сервер, чтобы начать";
  } else {
    t.textContent = "";
  }
}
setInterval(tickTimer, 1000);

function renderCurrent() {
  if (!snap) return;
  const p = activeProfile();
  const name = $("#curName"), meta = $("#curMeta"), flag = $("#curFlag");
  if (!p || p.nodes.length === 0) {
    name.textContent = "Добавить сервер";
    meta.textContent = "Ссылка на подписку или vless://, hy2://…";
    flag.textContent = "+";
    return;
  }
  const sel = snap.selectedNode;
  if (sel === "auto" || !findNode(p, sel)) {
    const live = status.activeNode && findNode(p, status.activeNode);
    name.textContent = "Автовыбор";
    meta.textContent = live
      ? "Сейчас: " + splitFlag(live.name).name + (pings[live.id] > 0 ? ", " + pings[live.id] + " мс" : "")
      : p.name + ", серверов: " + p.nodes.length;
    flag.innerHTML = live ? flagHTML(live.name) : "✦";
  } else {
    const n = findNode(p, sel);
    name.textContent = splitFlag(n.name).name;
    const parts = [n.protocol, n.security, p.name].filter(Boolean);
    if (pings[n.id] > 0) parts.push(pings[n.id] + " мс");
    meta.textContent = parts.join(", ");
    flag.innerHTML = flagHTML(n.name);
  }
}

function renderRouting() {
  $$(".routing button").forEach((b) => {
    b.setAttribute("aria-checked", String(b.dataset.routing === snap.settings.routing));
  });
}

function renderServers() {
  if (!snap) return;
  const tabs = $("#profileTabs");
  if (!viewProfile || !snap.profiles.find((p) => p.id === viewProfile)) {
    viewProfile = snap.activeProfile || (snap.profiles[0] && snap.profiles[0].id);
  }
  tabs.innerHTML = snap.profiles.map((p) =>
    `<button role="tab" data-id="${esc(p.id)}" aria-selected="${p.id === viewProfile}">${p.id === snap.activeProfile ? '<i class="dot" aria-hidden="true"></i>' : ""}${esc(p.name)}<small>${p.nodes.length}</small></button>`
  ).join("") + `<button class="tab-add" data-act="add-sub" title="Добавить подписку">+ Подписка</button>`;
  tabs.hidden = snap.profiles.length === 0;
  renderProfileInfo();
  renderNodes();
}

function renderProfileInfo() {
  const box = $("#profileInfo");
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) { box.innerHTML = ""; return; }
  if (p.kind !== "subscription") {
    box.innerHTML = `<div class="usage"><div class="usage-row"><span>Серверы, добавленные ссылками</span><b>${p.nodes.length}</b></div>
      <div class="usage-actions"><span class="spacer"></span><button class="link-btn danger" data-act="delete">Удалить все</button></div></div>`;
    return;
  }
  const used = (p.upload || 0) + (p.download || 0);
  let html = `<div class="usage">`;
  if (p.total > 0) {
    const pct = Math.min(100, (used / p.total) * 100);
    html += `<div class="usage-row"><span>Трафик</span><b>${fmtBytes(used)} из ${fmtBytes(p.total)}</b></div>
      <div class="bar"><i class="${pct > 85 ? "warn" : ""}" style="width:${pct.toFixed(1)}%"></i></div>`;
  } else if (used > 0) {
    html += `<div class="usage-row"><span>Трафик</span><b>${fmtBytes(used)}, без лимита</b></div>`;
  }
  if (p.expire > 0) {
    const days = Math.ceil((p.expire * 1000 - Date.now()) / 86400000);
    html += `<div class="usage-row"><span>Действует до</span><b>${fmtDate(p.expire)}${days >= 0 && days <= 7 ? ` (осталось дней: ${days})` : ""}</b></div>`;
  }
  if (p.updatedAt) {
    html += `<div class="usage-row"><span>Обновлена</span><b>${new Date(p.updatedAt * 1000).toLocaleString("ru-RU", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}</b></div>`;
  }
  const subs = snap.profiles.filter((x) => x.kind === "subscription").length;
  html += `<div class="usage-actions">
      <button class="link-btn" data-act="update">Обновить</button>
      ${subs > 1 ? `<button class="link-btn" data-act="update-all">Обновить все</button>` : ""}
      <button class="link-btn" data-act="rename">Переименовать</button>
      <button class="link-btn" data-act="share">Поделиться</button>
      ${p.supportUrl ? `<button class="link-btn" data-act="support">Поддержка</button>` : ""}
      <span class="spacer"></span>
      <button class="link-btn danger" data-act="delete">Удалить</button>
    </div></div>`;
  box.innerHTML = html;
}

function renderNodes() {
  const list = $("#nodeList");
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) {
    list.innerHTML = `<li class="empty"><b>Серверов пока нет</b>Вставьте ссылку на подписку от вашего провайдера или ссылку на сервер.<br><button class="btn" data-act="add">Добавить серверы</button></li>`;
    return;
  }
  const isActiveProfile = p.id === snap.activeProfile;
  const sel = isActiveProfile ? snap.selectedNode : null;
  const check = `<span class="check"><svg viewBox="0 0 24 24"><path d="m5 12 5 5 9-10"/></svg></span>`;
  let html = "";
  const autoSelected = sel === "auto" || (isActiveProfile && sel && !findNode(p, sel));
  const live = autoSelected && status.state === "connected" && findNode(p, status.activeNode);
  html += `<li class="node" data-id="auto" aria-selected="${!!autoSelected}">
      <span class="flag">✦</span>
      <span class="body"><span class="name">Автовыбор</span>
      <span class="meta">${live ? "Сейчас: " + esc(splitFlag(live.name).name) : "Самый быстрый сервер, переключается при сбое"}</span></span>
      ${autoSelected ? check : ""}
    </li>`;
  fillFilterOptions(p);
  const visible = p.nodes.filter(matchesFilters);
  const ordered = sortByPing
    ? [...visible].sort((a, b) => rank(pings[a.id]) - rank(pings[b.id]))
    : (filters.fav ? visible : [...visible].sort((a, b) => (favorites.has(b.id) ? 1 : 0) - (favorites.has(a.id) ? 1 : 0)));
  const filtered = visible.length !== p.nodes.length;
  $("#listCount").textContent = filtered ? `Показано ${visible.length} из ${p.nodes.length}` : "";
  if (!visible.length) {
    const elsewhere = filters.q ? snap.profiles.filter((o) => o.id !== p.id && o.nodes.some(matchesFilters)) : [];
    html += `<li class="empty"><b>Ничего не найдено</b>${
      elsewhere.length
        ? "Есть совпадения в других подписках:<br>" + elsewhere.map((o) => `<button class="btn ghost" data-goto="${esc(o.id)}">${esc(o.name)} · ${o.nodes.filter(matchesFilters).length}</button>`).join(" ")
        : "Измените поиск или фильтры."
    }</li>`;
  }
  for (const n of ordered) {
    const selected = sel === n.id;
    const pi = pingInfo(pings[n.id]);
    const tags = [n.protocol, n.security, n.network && n.network !== "tcp" ? n.network.toUpperCase() : ""].filter(Boolean).join(" ");
    html += `<li class="node" data-id="${esc(n.id)}" aria-selected="${selected}">
        <span class="flag">${flagHTML(n.name)}</span>
        <span class="body"><span class="name">${esc(splitFlag(n.name).name)}</span><span class="meta">${esc(tags)}${
          status.state === "connected" && status.activeNode === n.id && isActiveProfile ? ' <span class="live">● используется</span>' : ""
        }</span><span class="reason" data-reason="${esc(n.id)}">${esc(reasons[n.id] && pings[n.id] === -1 ? reasons[n.id] : "")}</span></span>
        <span class="ping ${pi.cls}" data-ping="${esc(n.id)}" title="${esc(reasons[n.id] || "")}">${pi.text}</span>
        <button class="icon-btn act ${favorites.has(n.id) ? "on" : ""}" data-fav="${esc(n.id)}" aria-pressed="${favorites.has(n.id)}" aria-label="Избранное" title="${favorites.has(n.id) ? "Убрать из избранного" : "В избранное"}"><svg viewBox="0 0 24 24"><path d="m12 3.5 2.6 5.4 5.9.8-4.3 4.1 1 5.8L12 16.8l-5.2 2.8 1-5.8-4.3-4.1 5.9-.8z"/></svg></button>
        <button class="icon-btn act" data-share="${esc(n.id)}" aria-label="Поделиться" title="Поделиться: QR-код и ссылка"><svg viewBox="0 0 24 24"><path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h2v2h-2zM18 18h2v2h-2zM18 14h2M14 18h2"/></svg></button>
        ${p.kind === "manual" ? `<button class="icon-btn del" data-del="${esc(n.id)}" aria-label="Удалить сервер" title="Удалить"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>` : ""}
        ${selected ? check : ""}
      </li>`;
  }
  list.innerHTML = html;
}

function rank(ms) {
  if (ms > 0) return ms;
  if (ms === 0 || ms === undefined) return 1e6;
  return 1e7; // failed
}

function renderPings() {
  $$("[data-ping]").forEach((el) => {
    const id = el.dataset.ping;
    const pi = pingInfo(pings[id]);
    el.textContent = pi.text;
    el.className = "ping " + pi.cls;
    el.title = reasons[id] || "";
  });
  $$("[data-reason]").forEach((el) => {
    const id = el.dataset.reason;
    el.textContent = pings[id] === -1 && reasons[id] ? reasons[id] : "";
  });
}

function renderProbe() {
  const bar = $("#probeBar");
  if (!bar) return;
  bar.hidden = !probe;
  if (probe) {
    $("#probeText").textContent = `Проверяем серверы: ${probe.done} из ${probe.total}`;
    $("#probeFill").style.width = ((probe.done / Math.max(1, probe.total)) * 100).toFixed(1) + "%";
  }
  $("#pingAll").disabled = !!probe;
}

// ------------------------------------------------------------------ filters
const regionNames = (() => { try { return new Intl.DisplayNames(["ru"], { type: "region" }); } catch (_) { return null; } })();

function countryOf(name) {
  const { flag } = splitFlag(name);
  if (!flag) return "";
  const cps = [...flag].map((c) => c.codePointAt(0) - 0x1F1E6 + 65);
  return String.fromCharCode(...cps);
}

function countryName(code) {
  try { return (regionNames && regionNames.of(code)) || code; } catch (_) { return code; }
}

function fillFilterOptions(p) {
  const protos = [...new Set(p.nodes.map((n) => n.protocol).filter(Boolean))].sort();
  const countries = [...new Set(p.nodes.map((n) => countryOf(n.name)).filter(Boolean))]
    .sort((a, b) => countryName(a).localeCompare(countryName(b), "ru"));
  const ps = $("#protoFilter"), cs = $("#countryFilter");
  const protoHTML = `<option value="">Все протоколы</option>` + protos.map((x) => `<option value="${esc(x)}">${esc(x)}</option>`).join("");
  const countryHTML = `<option value="">Все страны</option>` + countries.map((c) => {
    const flag = String.fromCodePoint(...[...c].map((ch) => 0x1F1E6 + ch.charCodeAt(0) - 65));
    return `<option value="${c}">${flag} ${esc(countryName(c))}</option>`;
  }).join("");
  if (ps.dataset.html !== protoHTML) { ps.innerHTML = protoHTML; ps.dataset.html = protoHTML; }
  if (cs.dataset.html !== countryHTML) { cs.innerHTML = countryHTML; cs.dataset.html = countryHTML; }
  if (!protos.includes(filters.proto)) filters.proto = "";
  if (!countries.includes(filters.country)) filters.country = "";
  ps.value = filters.proto;
  cs.value = filters.country;
  ps.hidden = protos.length < 2;
  cs.hidden = countries.length < 2;
}

function matchesFilters(n) {
  if (filters.fav && !favorites.has(n.id)) return false;
  if (filters.ok && !(pings[n.id] > 0)) return false;
  if (filters.proto && n.protocol !== filters.proto) return false;
  if (filters.country && countryOf(n.name) !== filters.country) return false;
  if (filters.q) {
    const code = countryOf(n.name);
    const hay = [n.name, n.protocol, n.security, n.network, n.server, code ? countryName(code) : ""].join(" ").toLowerCase();
    if (!filters.q.split(/\s+/).every((w) => hay.includes(w))) return false;
  }
  return true;
}

// ------------------------------------------------------------------ theme
const darkQuery = window.matchMedia("(prefers-color-scheme: light)");
function applyTheme(theme) {
  const t = theme === "light" || theme === "dark" ? theme : (darkQuery.matches ? "light" : "dark");
  document.documentElement.dataset.theme = t;
  document.documentElement.dataset.choice = theme || "system";
  renderGraph();
}
darkQuery.addEventListener("change", () => applyTheme(snap && snap.theme));

// ------------------------------------------------------------------ graph
function renderGraph() {
  const box = $("#graphBox");
  const live = status.state === "connected";
  box.hidden = !live;
  if (!live) return;
  const c = $("#graph");
  const dpr = window.devicePixelRatio || 1;
  const w = c.clientWidth || 300, h = 64;
  if (c.width !== Math.round(w * dpr)) { c.width = Math.round(w * dpr); c.height = Math.round(h * dpr); }
  const ctx = c.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const css = getComputedStyle(document.documentElement);
  const sea = css.getPropertyValue("--sea").trim(), star = css.getPropertyValue("--star").trim(), line = css.getPropertyValue("--hull").trim();
  const pts = traffic.slice(-120);
  const peak = Math.max(1, ...pts.map((p) => Math.max(p.down, p.up)));
  ctx.strokeStyle = line; ctx.lineWidth = 1;
  [0.5, 1].forEach((f) => { ctx.beginPath(); ctx.moveTo(0, h - (h - 4) * f + 0.5); ctx.lineTo(w, h - (h - 4) * f + 0.5); ctx.stroke(); });
  if (pts.length < 2) { $("#graphPeak").textContent = ""; return; }
  const x = (i) => (i / (120 - 1)) * w + (w - ((pts.length - 1) / (120 - 1)) * w);
  const y = (v) => h - 2 - (v / peak) * (h - 6);
  const path = (key) => { ctx.beginPath(); pts.forEach((p, i) => (i ? ctx.lineTo(x(i), y(p[key])) : ctx.moveTo(x(i), y(p[key])))); };
  path("down");
  ctx.lineTo(x(pts.length - 1), h); ctx.lineTo(x(0), h); ctx.closePath();
  ctx.globalAlpha = 0.18; ctx.fillStyle = sea; ctx.fill(); ctx.globalAlpha = 1;
  path("down"); ctx.strokeStyle = sea; ctx.lineWidth = 1.8; ctx.stroke();
  path("up"); ctx.strokeStyle = star; ctx.lineWidth = 1.4; ctx.stroke();
  const last = pts[pts.length - 1];
  ctx.fillStyle = sea; ctx.beginPath(); ctx.arc(x(pts.length - 1), y(last.down), 2.6, 0, 7); ctx.fill();
  $("#graphPeak").textContent = "пик " + fmtSpeed(peak);
}
window.addEventListener("resize", renderGraph);

// ------------------------------------------------------------------ my ip & speed
async function refreshIP() {
  if (ipBusy) return;
  ipBusy = true;
  $("#ipValue").textContent = "Проверяем…";
  try {
    const r = await api("myip");
    $("#ipValue").textContent = (r.flag ? r.flag + " " : "") + r.ip;
    $("#ipMeta").textContent = [r.country, r.city].filter(Boolean).join(", ") || r.isp || "";
    $("#ipCard").title = [r.isp, "Нажмите, чтобы обновить"].filter(Boolean).join(". ");
  } catch (e) {
    $("#ipValue").textContent = "Не удалось";
    $("#ipMeta").textContent = e.message;
  } finally {
    ipBusy = false;
  }
}

const mbps = (v) => (v >= 100 ? v.toFixed(0) : v.toFixed(1)).replace(".", ",") + " Мбит/с";
function onSpeed(d) {
  const v = $("#speedValue"), m = $("#speedMeta");
  if (d.phase === "ping") { v.textContent = "Пинг " + Math.round(d.value) + " мс"; m.textContent = "Измеряем загрузку…"; }
  else if (d.phase === "download") { v.textContent = "↓ " + mbps(d.value); m.textContent = "Загрузка…"; }
  else if (d.phase === "upload") { m.textContent = "↓ " + v.textContent.replace("↓ ", "") + ", ↑ " + mbps(d.value); }
  else if (d.phase === "result") {
    $("#speedCard").disabled = false;
    const r = d.result || {};
    if (r.error) { v.textContent = "Не удалось"; m.textContent = r.error; return; }
    v.textContent = "↓ " + mbps(r.downloadMbps) + "  ↑ " + mbps(r.uploadMbps);
    m.textContent = "Пинг " + r.pingMs + " мс, " + (d.viaVPN ? "через VPN" : "без VPN");
  }
}

async function runSpeed() {
  $("#speedCard").disabled = true;
  $("#speedValue").textContent = "Начинаем…";
  $("#speedMeta").textContent = status.state === "connected" ? "Через выбранный сервер" : "VPN выключен: скорость без VPN";
  try { await api("speedtest"); }
  catch (e) { $("#speedCard").disabled = false; $("#speedValue").textContent = "Не удалось"; $("#speedMeta").textContent = e.message; }
}

// ------------------------------------------------------------------ share & QR
async function share(title, text, hint) {
  $("#shareTitle").textContent = title;
  $("#shareHint").textContent = hint || "Отсканируйте QR-код в Vela или другом клиенте на другом устройстве.";
  $("#shareText").value = text;
  const img = $("#shareQR");
  img.hidden = true;
  $("#shareDialog").showModal();
  try {
    const r = await api("qr", { text });
    img.src = r.image;
    img.hidden = false;
  } catch (e) {
    $("#shareHint").textContent = e.message + ". Скопируйте текст.";
  }
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast("Скопировано"); }
  catch (_) { $("#shareText").select(); document.execCommand("copy"); toast("Скопировано"); }
}

function readImage(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result);
    r.onerror = () => reject(new Error("не удалось прочитать файл"));
    r.readAsDataURL(file);
  });
}

async function decodeQRFile(file) {
  if (!file || !file.type.startsWith("image/")) return;
  const err = $("#addError");
  err.hidden = true;
  try {
    const image = await readImage(file);
    const r = await api("qr/decode", { image });
    const ta = $("#addText");
    ta.value = (ta.value.trim() ? ta.value.trim() + "\n" : "") + r.text;
    toast("QR-код распознан");
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
  }
}

// ------------------------------------------------------------------ settings
const listFields = ["directDomains", "proxyDomains"];
let fillingSettings = false;

function fillSettings() {
  const f = $("#settingsForm");
  if (f.contains(document.activeElement) && document.activeElement.matches("input:not([type=checkbox]):not([type=radio]), textarea")) return;
  fillingSettings = true;
  const s = snap.settings;
  $$("input[name=mode]", f).forEach((r) => (r.checked = r.value === s.mode));
  f.connectOnLaunch.checked = !!snap.connectOnLaunch;
  f.blockAds.checked = !!s.blockAds;
  f.ipv6.checked = !!s.ipv6;
  f.allowLan.checked = !!s.allowLan;
  f.remoteDns.value = s.remoteDns || "";
  f.directDns.value = s.directDns || "";
  f.stack.value = s.stack || "mixed";
  f.proxyPort.value = s.proxyPort || 2080;
  for (const k of listFields) f[k].value = (s[k] || []).join(", ");
  f.failover.checked = !s.disableFailover;
  f.tlsFragment.value = s.tlsFragment || "";
  f.appMode.value = s.appMode || "";
  $$("input[name=theme]", f).forEach((r) => (r.checked = r.value === (snap.theme || "system")));
  if (!f.contains(document.activeElement) || !document.activeElement.closest("#rulesList")) {
    editRules = (s.rules || []).map((r) => ({ match: r.match, action: r.action || "direct" }));
    renderRules();
  }
  editApps = (s.apps || []).slice();
  renderApps();
  fillingSettings = false;
}

let saveTimer;
function saveSettings() {
  if (fillingSettings || !snap) return;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    const f = $("#settingsForm");
    const s = Object.assign({}, snap.settings, {
      mode: (f.querySelector("input[name=mode]:checked") || {}).value || "tun",
      blockAds: f.blockAds.checked,
      ipv6: f.ipv6.checked,
      allowLan: f.allowLan.checked,
      remoteDns: f.remoteDns.value.trim(),
      directDns: f.directDns.value.trim(),
      stack: f.stack.value,
      proxyPort: parseInt(f.proxyPort.value, 10) || 2080,
      disableFailover: !f.failover.checked,
      tlsFragment: f.tlsFragment.value,
      appMode: f.appMode.value,
      apps: editApps.slice(),
      rules: editRules.filter((r) => r.match.trim()).map((r) => ({ match: r.match.trim(), action: r.action })),
    });
    for (const k of listFields) {
      s[k] = f[k].value.split(/[\s,;]+/).map((x) => x.trim().replace(/^https?:\/\//, "").replace(/\/.*$/, "")).filter(Boolean);
    }
    try {
      await api("settings", { settings: s, connectOnLaunch: f.connectOnLaunch.checked });
      if (status.state === "connected") toast("Настройки применены, переподключаемся");
    } catch (e) { toast(e.message, true); }
  }, 450);
}

// ------------------------------------------------------------------ actions
async function toggle() {
  if (!snap) return;
  if (!activeProfile() && snap.profiles.length === 0) return openAdd();
  try {
    if (status.state === "disconnected") await api("connect");
    else await api("disconnect");
  } catch (e) { toast(e.message, true); }
}

async function elevate() {
  try { await api("elevate"); }
  catch (e) { toast(e.message, true); }
}

function openSheet(id) {
  const el = $("#" + id);
  if (id === "servers" && el.classList.contains("docked")) return;
  el.hidden = false;
  if (id === "servers") renderServers();
  const focusTarget = el.querySelector("[data-close]");
  if (focusTarget) focusTarget.focus();
}
function closeSheets() {
  $$(".sheet").forEach((s) => { if (!s.classList.contains("docked")) s.hidden = true; });
}

function openAdd() {
  $("#addError").hidden = true;
  $("#addText").value = "";
  $("#addText").placeholder = "https://… или vless://…";
  $("#addDialog").showModal();
  $("#addText").focus();
}

async function submitAdd(e) {
  e.preventDefault();
  const text = $("#addText").value.trim();
  const btn = $("#addSubmit");
  if (!text) { $("#addText").focus(); return; }
  btn.disabled = true;
  btn.textContent = "Загружаем…";
  try {
    const r = await api("import", { text });
    $("#addDialog").close();
    toast(r.message);
    viewProfile = null;
    if (!$("#servers").hidden || $("#servers").classList.contains("docked")) renderServers();

  } catch (err) {
    $("#addError").textContent = err.message;
    $("#addError").hidden = false;
  } finally {
    btn.disabled = false;
    btn.textContent = "Добавить";
  }
}

async function pasteFromClipboard() {
  try {
    $("#addText").value = await navigator.clipboard.readText();
  } catch (_) {
    toast("Нет доступа к буферу. Нажмите Ctrl+V в поле", true);
    $("#addText").focus();
  }
}

// ------------------------------------------------------------------ wiring
$("#toggle").addEventListener("click", toggle);
$("#openSettings").addEventListener("click", () => openSheet("settings"));
$("#currentServer").addEventListener("click", () => {
  if (snap && snap.profiles.length === 0) return openAdd();
  if (window.innerWidth >= 900) return;
  openSheet("servers");
});
$$("[data-close]").forEach((b) => b.addEventListener("click", closeSheets));
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !document.querySelector("dialog[open]")) closeSheets(); });

$$(".routing button").forEach((b) => b.addEventListener("click", () => {
  if (!snap || snap.settings.routing === b.dataset.routing) return;
  snap.settings.routing = b.dataset.routing;
  renderRouting();
  api("settings", { settings: snap.settings, connectOnLaunch: snap.connectOnLaunch }).catch((e) => toast(e.message, true));
}));

$("#profileTabs").addEventListener("click", (e) => {
  if (e.target.closest("[data-act=add-sub]")) return openAdd();
  const b = e.target.closest("button[data-id]");
  if (!b) return;
  viewProfile = b.dataset.id;
  renderServers();
});

$("#profileInfo").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-act]");
  if (!b) return;
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) return;
  if (b.dataset.act === "update") {
    b.disabled = true;
    b.textContent = "Обновляем…";
    try { toast((await api("profile/update", { id: p.id })).message); }
    catch (err) { toast(err.message, true); }
    finally { b.disabled = false; b.textContent = "Обновить"; }
  } else if (b.dataset.act === "update-all") {
    b.disabled = true;
    b.textContent = "Обновляем…";
    try { toast((await api("profile/update-all")).message); }
    catch (err) { toast(err.message, true); }
    finally { b.disabled = false; b.textContent = "Обновить все"; }
  } else if (b.dataset.act === "rename") {
    openRename(p);
  } else if (b.dataset.act === "share") {
    share("Подписка «" + p.name + "»", p.url, "Отсканируйте QR-код на другом устройстве: подписка добавится целиком.");
  } else if (b.dataset.act === "support") {
    api("open", { target: p.supportUrl });
  } else if (b.dataset.act === "delete") {
    if (confirm(p.kind === "manual" ? "Удалить все серверы, добавленные ссылками?" : `Удалить подписку «${p.name}»?`)) {
      try { await api("profile/delete", { id: p.id }); viewProfile = null; toast("Подписка удалена"); }
      catch (err) { toast(err.message, true); }
    }
  }
});

$("#nodeList").addEventListener("click", async (e) => {
  if (e.target.closest("[data-act=add]")) return openAdd();
  const go = e.target.closest("[data-goto]");
  if (go) { viewProfile = go.dataset.goto; renderServers(); return; }
  const fav = e.target.closest("[data-fav]");
  if (fav) {
    e.stopPropagation();
    const id = fav.dataset.fav, on = !favorites.has(id);
    if (on) favorites.add(id); else favorites.delete(id);
    renderNodes();
    api("favorite", { node: id, on }).catch((err) => toast(err.message, true));
    return;
  }
  const sh = e.target.closest("[data-share]");
  if (sh) {
    e.stopPropagation();
    const p = snap.profiles.find((x) => x.id === viewProfile);
    const n = p && findNode(p, sh.dataset.share);
    if (n) share(splitFlag(n.name).name, n.link);
    return;
  }
  const del = e.target.closest("[data-del]");
  if (del) {
    e.stopPropagation();
    try { await api("node/delete", { profile: viewProfile, node: del.dataset.del }); }
    catch (err) { toast(err.message, true); }
    return;
  }
  const row = e.target.closest(".node");
  if (!row) return;
  try {
    await api("select", { profile: viewProfile, node: row.dataset.id });
    if (window.innerWidth < 900) setTimeout(closeSheets, 160);
  } catch (err) { toast(err.message, true); }
});

$("#pingAll").addEventListener("click", () => {
  if (!viewProfile || probe) return;
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) return;
  p.nodes.forEach((n) => { pings[n.id] = 0; delete reasons[n.id]; });
  probe = { done: 0, total: p.nodes.length };
  renderProbe();
  renderPings();
  api("ping", { profile: viewProfile }).catch((e) => toast(e.message, true));
});

$("#sortToggle").addEventListener("click", () => {
  sortByPing = !sortByPing;
  $("#sortToggle").setAttribute("aria-pressed", String(sortByPing));
  renderNodes();
});

$("#openReport").addEventListener("click", () => api("open", { target: "report" }));

function openRename(p) {
  const d = $("#renameDialog");
  $("#renameText").value = p.customName || p.name;
  d.dataset.id = p.id;
  d.showModal();
  $("#renameText").select();
}
$("#renameForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const d = $("#renameDialog");
  try {
    await api("profile/rename", { id: d.dataset.id, name: $("#renameText").value });
    d.close();
  } catch (err) { toast(err.message, true); }
});
$("#renameCancel").addEventListener("click", () => $("#renameDialog").close());

$("#openAdd").addEventListener("click", openAdd);
$("#addForm").addEventListener("submit", submitAdd);
$("#addCancel").addEventListener("click", () => $("#addDialog").close());
$("#pasteBtn").addEventListener("click", pasteFromClipboard);

$("#settingsForm").addEventListener("input", saveSettings);
$("#settingsForm").addEventListener("change", saveSettings);
$("#settingsForm").addEventListener("submit", (e) => e.preventDefault());
$$("[data-open]").forEach((b) => b.addEventListener("click", () => {
  if (b.dataset.open === "privacy") return $("#privacyDialog").showModal();
  api("open", { target: b.dataset.open });
}));
$("#quit").addEventListener("click", async () => {
  try { await api("quit"); } catch (_) {}
  window.close();
});

// paste a link anywhere on the home screen to import it
document.addEventListener("paste", (e) => {
  if (document.querySelector("dialog[open]") || e.target.closest("input, textarea")) return;
  const img = [...(e.clipboardData || {}).items || []].find((i) => i.type.startsWith("image/"));
  if (img) { e.preventDefault(); openAdd(); decodeQRFile(img.getAsFile()); return; }
  const text = (e.clipboardData || window.clipboardData).getData("text");
  if (/^(https?|vless|vmess|trojan|ss|hysteria2|hy2|tuic):\/\//i.test(text.trim()) || text.trim().startsWith("vela-backup:")) {
    openAdd();
    $("#addText").value = text.trim();
  }
});

// ------------------------------------------------------------------ new wiring
function renderRules() {
  const ul = $("#rulesList");
  ul.innerHTML = editRules.map((r, i) => `<li data-i="${i}">
      <input data-k="match" value="${esc(r.match)}" placeholder="example.com" spellcheck="false" aria-label="Сайт, слово или IP">
      <select data-k="action" aria-label="Действие">
        <option value="direct"${r.action === "direct" ? " selected" : ""}>Напрямую</option>
        <option value="proxy"${r.action === "proxy" ? " selected" : ""}>Через VPN</option>
        <option value="block"${r.action === "block" ? " selected" : ""}>Блокировать</option>
      </select>
      <button type="button" class="icon-btn" data-rm="${i}" aria-label="Удалить правило"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>
    </li>`).join("");
}

function renderApps() {
  const f = $("#settingsForm");
  $("#appsBox").hidden = !f.appMode.value;
  $("#appChips").innerHTML = editApps.map((a, i) =>
    `<span class="app-chip">${esc(a)}<button type="button" data-rm-app="${i}" aria-label="Убрать ${esc(a)}">×</button></span>`).join("");
}

$("#rulesList").addEventListener("input", (e) => {
  const li = e.target.closest("li");
  if (!li) return;
  editRules[+li.dataset.i][e.target.dataset.k] = e.target.value;
  e.stopPropagation();
  saveSettings();
});
$("#rulesList").addEventListener("change", (e) => e.stopPropagation());
$("#rulesList").addEventListener("click", (e) => {
  const b = e.target.closest("[data-rm]");
  if (!b) return;
  editRules.splice(+b.dataset.rm, 1);
  renderRules();
  saveSettings();
});
$("#addRule").addEventListener("click", () => {
  editRules.push({ match: "", action: "proxy" });
  renderRules();
  $("#rulesList li:last-child input").focus();
});

$("#appChips").addEventListener("click", (e) => {
  const b = e.target.closest("[data-rm-app]");
  if (!b) return;
  editApps.splice(+b.dataset.rmApp, 1);
  renderApps();
  saveSettings();
});
function addApps(names) {
  for (const n of names) {
    const v = n.trim();
    if (v && !editApps.some((a) => a.toLowerCase() === v.toLowerCase())) editApps.push(v);
  }
  renderApps();
  saveSettings();
}
$("#addApp").addEventListener("click", () => {
  const inp = $("#appInput");
  let v = inp.value.trim();
  if (v && !/\.[a-z0-9]+$/i.test(v)) v += ".exe";
  if (v) addApps([v]);
  inp.value = "";
});
$("#appInput").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("#addApp").click(); } });
$("#appInput").addEventListener("input", (e) => e.stopPropagation());
$("#settingsForm").appMode.addEventListener("change", renderApps);

let runningApps = [];
$("#pickApps").addEventListener("click", async () => {
  try {
    runningApps = (await api("processes")).processes || [];
  } catch (e) { return toast(e.message, true); }
  $("#appsFilter").value = "";
  renderPick();
  $("#appsDialog").showModal();
});
function renderPick() {
  const q = $("#appsFilter").value.trim().toLowerCase();
  const chosen = new Set(editApps.map((a) => a.toLowerCase()));
  $("#appsList").innerHTML = runningApps.filter((a) => !q || a.toLowerCase().includes(q)).map((a) =>
    `<li><label><input type="checkbox" value="${esc(a)}"${chosen.has(a.toLowerCase()) ? " checked" : ""}>${esc(a)}</label></li>`).join("")
    || `<li class="hint">Ничего не найдено</li>`;
}
$("#appsFilter").addEventListener("input", renderPick);
$("#appsCancel").addEventListener("click", () => $("#appsDialog").close());
$("#appsForm").addEventListener("submit", (e) => {
  e.preventDefault();
  const boxes = $$("#appsList input");
  const on = boxes.filter((b) => b.checked).map((b) => b.value);
  const off = new Set(boxes.filter((b) => !b.checked).map((b) => b.value.toLowerCase()));
  editApps = editApps.filter((a) => !off.has(a.toLowerCase()));
  $("#appsDialog").close();
  addApps(on);
});

$$("input[name=theme]").forEach((r) => r.addEventListener("change", (e) => {
  e.stopPropagation();
  applyTheme(r.value);
  api("theme", { theme: r.value }).catch((err) => toast(err.message, true));
}));

$("#exportBtn").addEventListener("click", async () => {
  try {
    const r = await api("export");
    $("#shareTitle").textContent = "Резервная копия";
    $("#shareHint").textContent = r.image
      ? "Отсканируйте QR-код в Vela на другом устройстве или скопируйте текст и вставьте его в окно «Добавить»."
      : "Данных много для QR-кода. Скопируйте текст и вставьте его в окно «Добавить» на другом устройстве.";
    $("#shareText").value = r.text;
    const img = $("#shareQR");
    img.hidden = !r.image;
    if (r.image) img.src = r.image;
    $("#shareDialog").showModal();
  } catch (e) { toast(e.message, true); }
});
$("#importBtn").addEventListener("click", () => {
  openAdd();
  $("#addText").placeholder = "Вставьте резервную копию: vela-backup:…";
});
$("#shareCopy").addEventListener("click", () => copyText($("#shareText").value));

async function loadLogs() {
  try {
    const r = await api("logs");
    const pre = $("#logsText");
    pre.textContent = (r.lines || []).join("\n") || "Журнал пуст. Он появится после подключения.";
    pre.scrollTop = pre.scrollHeight;
  } catch (e) { toast(e.message, true); }
}
$("#showLogs").addEventListener("click", () => { $("#logsDialog").showModal(); loadLogs(); });
$("#logsRefresh").addEventListener("click", loadLogs);

$("#ipCard").addEventListener("click", refreshIP);
$("#speedCard").addEventListener("click", runSpeed);

$("#search").addEventListener("input", (e) => { filters.q = e.target.value.trim().toLowerCase(); renderNodes(); });
$("#filters").addEventListener("click", (e) => {
  const b = e.target.closest("[data-filter]");
  if (!b) return;
  filters[b.dataset.filter] = !filters[b.dataset.filter];
  b.setAttribute("aria-pressed", String(filters[b.dataset.filter]));
  renderNodes();
});
$("#protoFilter").addEventListener("change", (e) => { filters.proto = e.target.value; renderNodes(); });
$("#countryFilter").addEventListener("change", (e) => { filters.country = e.target.value; renderNodes(); });

$("#qrPick").addEventListener("click", () => $("#qrFile").click());
$("#qrFile").addEventListener("change", (e) => { decodeQRFile(e.target.files[0]); e.target.value = ""; });
const drop = $("#qrDrop");
["dragenter", "dragover"].forEach((ev) => $("#addDialog").addEventListener(ev, (e) => { e.preventDefault(); drop.classList.add("over"); }));
["dragleave", "drop"].forEach((ev) => $("#addDialog").addEventListener(ev, (e) => { e.preventDefault(); drop.classList.remove("over"); }));
$("#addDialog").addEventListener("drop", (e) => decodeQRFile(e.dataTransfer.files[0]));
$("#addDialog").addEventListener("paste", (e) => {
  const item = [...(e.clipboardData || {}).items || []].find((i) => i.type.startsWith("image/"));
  if (item) { e.preventDefault(); decodeQRFile(item.getAsFile()); }
});

function applyLayout() {
  const servers = $("#servers");
  if (window.innerWidth >= 900) {
    servers.classList.add("docked");
    servers.hidden = false;
    if (snap) renderServers();
  } else if (servers.classList.contains("docked")) {
    servers.classList.remove("docked");
    servers.hidden = true;
  }
}
window.addEventListener("resize", applyLayout);
applyLayout();
connectEvents();
