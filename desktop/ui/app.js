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
let sortByPing = (() => { try { return localStorage.getItem("vela-sort") === "ping"; } catch (_) { return false; } })();
let viewProfile = null;   // profile shown in the servers sheet
let favorites = new Set();
const filters = { q: "", fav: false, ok: false, proto: "", country: "" };
let traffic = [];          // [{t, up, down}] for the graph
let editRules = [];        // routing rules being edited in settings
let editApps = [];         // split tunneling programs
let ipBusy = false;
let firstVersion = null;   // a different version after reconnecting = Vela was updated
let clipOffer = null;      // subscription found in the clipboard
let shownOffer = null;     // id of the app link shown in the dialog

setLang((navigator.language || "ru").toLowerCase().startsWith("ru") ? "ru" : "en");

// ------------------------------------------------------------------ api
async function api(path, body) {
  const res = await fetch("/api/" + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Vela-Token": token },
    body: JSON.stringify(body || {}),
  });
  let data = {};
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) throw new Error(data.error || t("common.error", { code: res.status }));
  return data;
}

function connectEvents() {
  const es = new EventSource("/api/events?t=" + encodeURIComponent(token));
  es.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "state") {
      if (firstVersion && msg.data.version !== firstVersion) {
        // updated: load the window of the new version
        location.reload();
        return;
      }
      firstVersion = msg.data.version;
      $("#restartOverlay").hidden = true;
      snap = msg.data;
      status = snap.status;
      // the backend has the latest check results
      pings = Object.assign({}, snap.pings || {});
      reasons = Object.assign({}, snap.reasons || {});
      favorites = new Set(snap.favorites || []);
      if (!traffic.length && snap.history) traffic = snap.history.slice();
      const langChanged = setLang(snap.lang);
      applyTheme(snap.theme);
      renderAll();
      if (langChanged) onLanguageChanged();
      syncOfferDialog();
      if (!clipChecked) { clipChecked = true; checkClipboard(); }
      if (snap.updatedFrom && !updatedShown) {
        updatedShown = true;
        toast(t("upd.updated", { v: snap.version }));
      }
    } else if (msg.type === "status") {
      const was = status.state;
      const wasActive = status.activeNode;
      status = msg.data;
      if (status.state === "connected") {
        traffic.push({ t: Date.now() / 1000, up: status.up || 0, down: status.down || 0 });
        if (traffic.length > 300) traffic.splice(0, traffic.length - 300);
      } else if (status.state === "connecting") {
        traffic = [];
      }
      if (was !== status.state && (status.state === "connected" || status.state === "disconnected")) {
        setTimeout(refreshIP, status.state === "connected" ? 1500 : 300);
        if (!$("#connections").hidden) loadConnections();
      }
      renderGraph();
      renderStatus();
      renderCurrent();
      if (!$("#settings").hidden && was !== status.state) renderPrefs();
      // the list is rebuilt only when something in it changes, not every second:
      // rebuilding under the cursor swallows clicks and is slow with 500+ servers
      if (!$("#servers").hidden && (was !== status.state || wasActive !== status.activeNode)) renderNodes();
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
      if (d.stopped) {
        for (const id of Object.keys(pings)) if (pings[id] === 0) delete pings[id];
        renderPings();
        toast(t("probe.stopped"));
      } else if (d.error) {
        toast(t("probe.failed", { error: d.error }), true);
        for (const id of Object.keys(pings)) if (pings[id] === 0) delete pings[id];
        renderPings();
      }
      renderProbe();
      if (!probe && d.total && !d.error && !d.stopped) {
        const p = snap && snap.profiles.find((x) => x.id === viewProfile);
        if (p) {
          const ok = p.nodes.filter((n) => pings[n.id] > 0).length;
          toast(t("probe.done", { ok, total: p.nodes.length }));
        }
        if (sortByPing) renderNodes();
      }
    } else if (msg.type === "update") {
      if (snap) {
        snap.update = Object.assign({}, snap.update, { installing: true, progress: msg.data.progress });
        const el = $("#updProgress");
        if (el) {
          const pct = Math.round((msg.data.progress || 0) * 100);
          el.style.width = pct + "%";
          $("#updProgressText").textContent = t("upd.downloading", { pct });
        } else renderNotices();
      }
    } else if (msg.type === "restart") {
      $("#restartOverlay").hidden = false;
    }
  };
  es.onerror = () => {
    es.close();
    setTimeout(connectEvents, $("#restartOverlay").hidden ? 1500 : 500);
  };
}
let updatedShown = false, clipChecked = false;

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
  // first letter or digit: names often start with emoji like 🎮⚡
  const m = /[\p{L}\p{N}]/u.exec(rest);
  return esc(m ? m[0].toUpperCase() : "✦");
}

function fmtNum(n, digits) {
  return n.toLocaleString(locale(), { maximumFractionDigits: digits, minimumFractionDigits: 0 });
}

function fmtBytes(n) {
  const u = t("units").split(" ");
  if (!n || n < 0) return "0 " + u[0];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? fmtNum(n, 0) : fmtNum(n, n < 10 ? 1 : 0)) + " " + u[i];
}
const fmtSpeed = (n) => fmtBytes(n) + t("units.perSec");

function fmtDuration(sec) {
  sec = Math.max(0, sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  const p = (x) => String(x).padStart(2, "0");
  return (h ? h + ":" : "") + p(m) + ":" + p(s);
}

function fmtDate(unix) {
  return new Date(unix * 1000).toLocaleDateString(locale(), { day: "numeric", month: "long", year: "numeric" });
}

function pingInfo(ms) {
  if (ms === undefined) return { text: "", cls: "" };
  if (ms === 0) return { text: "…", cls: "" };
  if (ms === -2) return { text: t("ping.after"), cls: "" };
  if (ms < 0) return { text: t("ping.dead"), cls: "bad" };
  const cls = ms < 150 ? "good" : ms < 300 ? "ok" : ms < 600 ? "slow" : "bad";
  return { text: t("units.ms", { n: ms }), cls };
}

// short hash for banner keys (dismissed announcements)
function hashText(s) {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 0x01000193); }
  return (h >>> 0).toString(36);
}

// linkify turns links in provider text into buttons that open the browser
function linkify(text) {
  const re = /\bhttps?:\/\/[^\s<>"']+[^\s<>"'.,;:!?)»]/g;
  let out = "", last = 0, m;
  while ((m = re.exec(text))) {
    out += esc(text.slice(last, m.index)) + `<a href="#" data-url="${esc(m[0])}">${esc(m[0])}</a>`;
    last = m.index + m[0].length;
  }
  return out + esc(text.slice(last));
}

let toastTimer;
function toast(text, isError) {
  const el = $("#toast");
  el.textContent = text;
  el.className = "toast" + (isError ? " error" : "");
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.hidden = true), isError ? 5000 : 2600);
}

const activeProfile = () => snap && snap.profiles.find((p) => p.id === snap.activeProfile);
const findNode = (p, id) => p && p.nodes.find((n) => n.id === id);
const dismissed = (key) => !!snap && (snap.dismissed || []).includes(key);
const announceKey = (p) => "announce:" + p.id + ":" + hashText(p.announce || "");

// ------------------------------------------------------------------ render
function renderAll() {
  renderStatus();
  renderCurrent();
  renderRouting();
  renderNotices();
  if (!$("#servers").hidden) renderServers();
  fillSettings();
  $("#aboutText").textContent = t("about.text", { v: snap.version });
}

// texts built in JS follow the new language
function onLanguageChanged() {
  if (ipShown) refreshIP();
  else { $("#ipValue").textContent = t("ip.find"); $("#ipMeta").textContent = t("ip.meta"); }
  if (!speedShown) { $("#speedValue").textContent = t("speed.measure"); $("#speedMeta").textContent = t("speed.meta"); }
  $("#protoFilter").dataset.html = "";
  $("#countryFilter").dataset.html = "";
  if (!$("#servers").hidden) renderServers();
  renderProbe();
  renderGraph();
  if (!$("#stats").hidden && statsData) renderStats(statsData);
  if (!$("#connections").hidden) renderConnections();
}

const stateKey = { disconnected: "state.disconnected", connecting: "state.connecting", connected: "state.connected", disconnecting: "state.disconnecting" };

let ksTimer;
function renderStatus() {
  const hero = $("#hero");
  hero.dataset.state = status.state;
  $("#statusText").textContent = t(stateKey[status.state] || "state.disconnected");
  $("#toggleLabel").textContent =
    status.state === "connected" ? t("toggle.disconnect") :
    status.state === "connecting" ? t("toggle.cancel") :
    status.state === "disconnecting" ? t("toggle.disconnecting") : t("toggle.connect");
  $("#toggle").setAttribute("aria-label", $("#toggleLabel").textContent);
  const live = status.state === "connected";
  $("#speed").hidden = !live;
  if (live) {
    $("#down").textContent = fmtSpeed(status.down);
    $("#up").textContent = fmtSpeed(status.up);
  }
  const mark = $("#ksMark");
  mark.hidden = !(status.killSwitch && live);
  mark.title = t("ks.on");
  tickTimer();

  // the internet is blocked by the kill switch; during a quick reconnect the
  // banner would only flash, so it waits a moment
  const blocked = status.blocked && status.state === "disconnected";
  clearTimeout(ksTimer);
  if (!blocked) $("#ksBanner").hidden = true;
  else if ($("#ksBanner").hidden) ksTimer = setTimeout(() => { $("#ksBanner").hidden = !(status.blocked && status.state === "disconnected"); updateCrowded(); }, 1200);

  const banner = $("#banner");
  const error = status.error || status.killSwitchError;
  if (error) {
    banner.hidden = false;
    banner.classList.remove("notice");
    $("#bannerText").textContent = error;
    const act = $("#bannerAction");
    if (status.errorCode === "need_admin") {
      act.hidden = false;
      act.textContent = t("banner.restart");
      act.onclick = elevate;
    } else if (status.errorCode === "server_unreachable" && window.innerWidth < 900) {
      act.hidden = false;
      act.textContent = t("banner.pickServer");
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
  updateCrowded();
}

function tickTimer() {
  const el = $("#timer");
  if (status.state === "connected" && status.since) {
    const total = fmtBytes((status.upTotal || 0) + (status.downTotal || 0));
    el.textContent = t("home.timer", { time: fmtDuration(Math.floor(Date.now() / 1000) - status.since), bytes: total });
  } else if (status.state === "disconnected" && snap && snap.profiles.length === 0) {
    el.textContent = t("home.addFirst");
  } else {
    el.textContent = "";
  }
}
setInterval(tickTimer, 1000);

function renderCurrent() {
  if (!snap) return;
  const p = activeProfile();
  const name = $("#curName"), meta = $("#curMeta"), flag = $("#curFlag");
  if (!p || p.nodes.length === 0) {
    name.textContent = t("current.add");
    meta.textContent = t("current.addMeta");
    flag.textContent = "+";
    return;
  }
  const sel = snap.selectedNode;
  if (sel === "auto" || !findNode(p, sel)) {
    const live = status.activeNode && findNode(p, status.activeNode);
    name.textContent = t("current.auto");
    meta.textContent = live
      ? t("current.now", { name: splitFlag(live.name).name }) + (pings[live.id] > 0 ? ", " + t("units.ms", { n: pings[live.id] }) : "")
      : p.name + ", " + t("count.servers", { n: p.nodes.length });
    flag.innerHTML = live ? flagHTML(live.name) : "✦";
  } else {
    const n = findNode(p, sel);
    name.textContent = splitFlag(n.name).name;
    const parts = [n.protocol, n.engine === "xray" ? "Xray" : "", n.security, p.name].filter(Boolean);
    if (pings[n.id] > 0) parts.push(t("units.ms", { n: pings[n.id] }));
    meta.textContent = parts.join(", ");
    flag.innerHTML = flagHTML(n.name);
  }
}

function renderRouting() {
  const r = snap.settings.routing || "smart";
  $$(".routing button").forEach((b) => b.setAttribute("aria-checked", String(b.dataset.routing === r)));
  $("#routingHint").textContent = t("routing." + r + ".hint");
}

function renderServers() {
  if (!snap) return;
  const tabs = $("#profileTabs");
  if (!viewProfile || !snap.profiles.find((p) => p.id === viewProfile)) {
    viewProfile = snap.activeProfile || (snap.profiles[0] && snap.profiles[0].id);
  }
  tabs.innerHTML = snap.profiles.map((p) =>
    `<button role="tab" data-id="${esc(p.id)}" aria-selected="${p.id === viewProfile}">${p.id === snap.activeProfile ? '<i class="dot" aria-hidden="true"></i>' : ""}${esc(p.name)}<small>${p.nodes.length}</small></button>`
  ).join("") + `<button class="tab-add" data-act="add-sub" title="${esc(t("servers.addSubTitle"))}">${esc(t("servers.addSub"))}</button>`;
  tabs.hidden = snap.profiles.length === 0;
  $("#refreshSub").hidden = !snap.profiles.some((p) => p.kind === "subscription");
  renderProfileInfo();
  renderNodes();
  renderNotices();
}

function renderProfileInfo() {
  const box = $("#profileInfo");
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) { box.innerHTML = ""; return; }
  if (p.kind !== "subscription") {
    box.innerHTML = `<div class="usage"><div class="usage-row"><span>${esc(t("profile.manual"))}</span><b>${p.nodes.length}</b></div>
      <div class="usage-actions"><span class="spacer"></span><button class="link-btn danger" data-act="delete">${esc(t("profile.deleteAll"))}</button></div></div>`;
    return;
  }
  const used = (p.upload || 0) + (p.download || 0);
  let html = `<div class="usage">`;
  if (p.announce && !dismissed(announceKey(p))) {
    html += `<div class="usage-announce"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 10v4h3l6 4V6L7 10H4Z"/><path d="M17 9a4 4 0 0 1 0 6"/></svg><span>${linkify(p.announce)}</span>
      <button class="icon-btn notice-x" data-dismiss="${esc(announceKey(p))}" aria-label="${esc(t("common.dismiss"))}" title="${esc(t("common.dismiss"))}"><svg viewBox="0 0 24 24"><path d="M7 7l10 10M17 7 7 17"/></svg></button></div>`;
  }
  if (p.total > 0) {
    const pct = Math.min(100, (used / p.total) * 100);
    html += `<div class="usage-row"><span>${esc(t("profile.traffic"))}</span><b>${esc(t("profile.trafficOf", { used: fmtBytes(used), total: fmtBytes(p.total) }))}</b></div>
      <div class="bar"><i class="${pct >= 90 ? "danger" : pct > 85 ? "warn" : ""}" style="width:${pct.toFixed(1)}%"></i></div>`;
  } else if (used > 0) {
    html += `<div class="usage-row"><span>${esc(t("profile.traffic"))}</span><b>${esc(t("profile.unlimited", { used: fmtBytes(used) }))}</b></div>`;
  }
  if (p.expire > 0) {
    const days = Math.ceil((p.expire * 1000 - Date.now()) / 86400000);
    const left = days >= 0 && days <= 7 ? ` (${t("profile.left", { days: t("count.days", { n: days }) })})` : "";
    html += `<div class="usage-row"><span>${esc(t("profile.until"))}</span><b class="${days <= 0 ? "bad" : days <= 3 ? "soon" : ""}">${esc(fmtDate(p.expire) + left)}</b></div>`;
  }
  if (p.updatedAt) {
    html += `<div class="usage-row"><span>${esc(t("profile.updated"))}</span><b>${esc(new Date(p.updatedAt * 1000).toLocaleString(locale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }))}</b></div>`;
  }
  const subs = snap.profiles.filter((x) => x.kind === "subscription").length;
  html += `<div class="usage-actions">
      <button class="link-btn" data-act="update">${esc(t("profile.update"))}</button>
      ${subs > 1 ? `<button class="link-btn" data-act="update-all">${esc(t("profile.updateAll"))}</button>` : ""}
      <button class="link-btn" data-act="rename">${esc(t("profile.rename"))}</button>
      <button class="link-btn" data-act="share">${esc(t("profile.share"))}</button>
      ${p.supportUrl ? `<button class="link-btn" data-act="support">${esc(t("profile.support"))}</button>` : ""}
      <span class="spacer"></span>
      <button class="link-btn danger" data-act="delete">${esc(t("profile.delete"))}</button>
    </div></div>`;
  box.innerHTML = html;
}

function renderNodes() {
  const list = $("#nodeList");
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) {
    list.innerHTML = `<li class="empty"><b>${esc(t("servers.emptyTitle"))}</b>${esc(t("servers.emptyText"))}<br><button class="btn" data-act="add">${esc(t("servers.addServers"))}</button></li>`;
    $("#listCount").textContent = "";
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
      <span class="body"><span class="name">${esc(t("current.auto"))}</span>
      <span class="meta">${live ? esc(t("current.now", { name: splitFlag(live.name).name })) : esc(t("servers.autoMeta"))}</span></span>
      ${autoSelected ? check : ""}
    </li>`;
  fillFilterOptions(p);
  const visible = p.nodes.filter(matchesFilters);
  const ordered = sortByPing
    ? [...visible].sort((a, b) => rank(pings[a.id]) - rank(pings[b.id]))
    : (filters.fav ? visible : [...visible].sort((a, b) => (favorites.has(b.id) ? 1 : 0) - (favorites.has(a.id) ? 1 : 0)));
  const filtered = visible.length !== p.nodes.length;
  $("#listCount").textContent = filtered ? t("servers.shown", { n: visible.length, total: p.nodes.length }) : "";
  if (!visible.length) {
    const elsewhere = filters.q ? snap.profiles.filter((o) => o.id !== p.id && o.nodes.some(matchesFilters)) : [];
    html += `<li class="empty"><b>${esc(t("servers.nothing"))}</b>${
      elsewhere.length
        ? esc(t("servers.elsewhere")) + "<br>" + elsewhere.map((o) => `<button class="btn ghost" data-goto="${esc(o.id)}">${esc(o.name)} · ${o.nodes.filter(matchesFilters).length}</button>`).join(" ")
        : esc(t("servers.changeFilters"))
    }</li>`;
  }
  const xray = `<span class="badge" title="${esc(t("servers.xray"))}">Xray</span>`;
  const favLabel = esc(t("servers.favAria")), pingLabel = esc(t("servers.pingOneAria")), shareLabel = esc(t("servers.shareAria"));
  for (const n of ordered) {
    const selected = sel === n.id;
    const pi = pingInfo(pings[n.id]);
    const tags = [n.protocol, n.security, n.network && n.network !== "tcp" ? n.network.toUpperCase() : ""].filter(Boolean).join(" ");
    const isFav = favorites.has(n.id);
    html += `<li class="node" data-id="${esc(n.id)}" aria-selected="${selected}">
        <span class="flag">${flagHTML(n.name)}</span>
        <span class="body"><span class="name">${esc(splitFlag(n.name).name)}</span><span class="meta">${esc(tags)}${n.engine === "xray" ? " " + xray : ""}${
          status.state === "connected" && status.activeNode === n.id && isActiveProfile ? ` <span class="live">${esc(t("servers.inUse"))}</span>` : ""
        }</span><span class="reason" data-reason="${esc(n.id)}">${esc(reasons[n.id] && pings[n.id] === -1 ? reasons[n.id] : "")}</span></span>
        <span class="ping ${pi.cls}" data-ping="${esc(n.id)}" title="${esc(reasons[n.id] || "")}">${esc(pi.text)}</span>
        <button class="icon-btn act ${isFav ? "on" : ""}" data-fav="${esc(n.id)}" aria-pressed="${isFav}" aria-label="${favLabel}" title="${esc(t(isFav ? "servers.favRemove" : "servers.favAdd"))}"><svg viewBox="0 0 24 24"><path d="m12 3.5 2.6 5.4 5.9.8-4.3 4.1 1 5.8L12 16.8l-5.2 2.8 1-5.8-4.3-4.1 5.9-.8z"/></svg></button>
        <button class="icon-btn act" data-pingone="${esc(n.id)}" aria-label="${pingLabel}" title="${esc(t("servers.pingOne"))}"><svg viewBox="0 0 24 24"><path d="M4 16a8 8 0 1 1 16 0"/><path d="m12 16 4-6"/></svg></button>
        <button class="icon-btn act" data-share="${esc(n.id)}" aria-label="${shareLabel}" title="${esc(t("servers.share"))}"><svg viewBox="0 0 24 24"><path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h2v2h-2zM18 18h2v2h-2zM18 14h2M14 18h2"/></svg></button>
        ${p.kind === "manual" ? `<button class="icon-btn del" data-del="${esc(n.id)}" aria-label="${esc(t("servers.deleteAria"))}" title="${esc(t("servers.delete"))}"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>` : ""}
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
    $("#probeText").textContent = t("servers.probing", { done: probe.done, total: probe.total });
    $("#probeFill").style.width = ((probe.done / Math.max(1, probe.total)) * 100).toFixed(1) + "%";
  }
  // while checking, the button stops the check
  const b = $("#pingAll");
  b.classList.toggle("stop", !!probe);
  b.title = probe ? t("servers.pingStop") : t("servers.pingAll");
  b.setAttribute("aria-label", b.title);
  b.innerHTML = probe
    ? '<svg viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" rx="1.5"/></svg>'
    : '<svg viewBox="0 0 24 24"><path d="M4 16a8 8 0 1 1 16 0"/><path d="m12 16 4-6"/></svg>';
}

function stopProbe() {
  api("ping/stop").catch((e) => toast(e.message, true));
}

// ------------------------------------------------------------------ notices
// Banners on the home screen: an update, a subscription in the clipboard,
// subscription alerts and messages from providers.
const xIcon = `<svg viewBox="0 0 24 24"><path d="M7 7l10 10M17 7 7 17"/></svg>`;

function noticeHTML(n) {
  return `<div class="notice ${n.tone || ""}" data-key="${esc(n.key)}">
    <div class="notice-body">
      ${n.label ? `<span class="notice-label">${esc(n.label)}</span>` : ""}
      <b>${esc(n.title)}</b>
      ${n.html ? `<span class="notice-text">${n.html}</span>` : n.text ? `<span class="notice-text">${esc(n.text)}</span>` : ""}
      ${n.actions ? `<div class="notice-actions">${n.actions}</div>` : ""}
    </div>
    ${n.dismiss ? `<button class="icon-btn notice-x" data-dismiss="${esc(n.dismiss)}" aria-label="${esc(t("common.dismiss"))}" title="${esc(t("common.dismiss"))}">${xIcon}</button>` : ""}
  </div>`;
}

const alertOrder = { expired: 0, "1d": 1, traffic: 2, "3d": 3 };

function renderNotices() {
  if (!snap) return;
  const items = [];
  const u = snap.update || {};
  if (u.installing) {
    const pct = Math.round((u.progress || 0) * 100);
    items.push({
      key: "update", tone: "info", title: t("upd.available", { v: u.available || "" }),
      html: `<span id="updProgressText">${esc(t("upd.downloading", { pct }))}</span><span class="bar"><i id="updProgress" style="width:${pct}%"></i></span>`,
    });
  } else if (u.available && !dismissed("update:" + u.available)) {
    items.push({
      key: "update", tone: "info", title: t("upd.available", { v: u.available }),
      text: (u.notes ? u.notes.trim() + " " : "") + t("upd.availableText"),
      actions: `<button class="btn small" data-act="update-install">${esc(t("upd.install"))}</button>`,
      dismiss: "update:" + u.available,
    });
  }
  if (clipOffer) {
    const subs = clipOffer.subscriptions || [];
    const title = subs.length === 1 ? t("clip.sub", { host: clipOffer.hosts[0] })
      : subs.length > 1 ? t("clip.subs", { subs: t("count.subs", { n: subs.length }) })
      : t("clip.servers", { servers: t("count.servers", { n: clipOffer.linkCount }) });
    items.push({
      key: "clip", tone: "info", title, text: t("clip.ask"),
      actions: `<button class="btn small" data-act="clip-add">${esc(t("common.add"))}</button><button class="btn ghost small" data-act="clip-skip">${esc(t("common.notNow"))}</button>`,
    });
  }
  // the home screen warns about the subscription in use, the most urgent
  // thing only; other subscriptions show it in their card
  const p = activeProfile();
  const alerts = (snap.alerts || []).filter((al) => p && al.profile === p.id)
    .sort((a, b) => alertOrder[a.kind] - alertOrder[b.kind]).slice(0, 1);
  for (const al of alerts) {
    const vars = { name: al.name, pct: al.percent, days: t("count.days", { n: al.daysLeft || 0 }) };
    items.push({
      key: al.key, tone: al.kind === "expired" || al.kind === "1d" ? "danger" : "warn",
      title: t("alert." + al.kind, vars),
      text: t(al.kind === "expired" ? "alert.expiredText" : al.kind === "traffic" ? "alert.trafficText" : "alert.soonText"),
      actions: al.supportUrl ? `<button class="btn ghost small" data-url="${esc(al.supportUrl)}">${esc(t(/^(tg:|https?:\/\/t\.me\/)/.test(al.supportUrl) ? "profile.support" : "alert.renew"))}</button>` : "",
      dismiss: al.key,
    });
  }
  // in a wide window the subscription card next to it shows the message already
  const inCard = window.innerWidth >= 900 && viewProfile === (p && p.id);
  if (p && p.announce && !dismissed(announceKey(p)) && !inCard) {
    items.push({ key: "announce", tone: "note", label: t("announce.label", { name: p.name }), title: "", html: linkify(p.announce), dismiss: announceKey(p) });
  }
  const html = items.map(noticeHTML).join("");
  const box = $("#notices");
  if (box.dataset.html !== html) {
    box.innerHTML = html;
    box.dataset.html = html;
  }
  updateCrowded();
}

// with banners on screen the constellation gets smaller, so the server and
// routing controls stay in view
function updateCrowded() {
  const n = $("#notices").children.length + ($("#banner").hidden ? 0 : 1) + ($("#ksBanner").hidden ? 0 : 1);
  $("#home").classList.toggle("crowded", n > 0 && window.innerHeight < 1000);
}

async function dismissKey(key) {
  if (!snap) return;
  snap.dismissed = (snap.dismissed || []).concat(key);
  renderNotices();
  if ($("#servers").hidden === false) renderProfileInfo();
  try { await api("dismiss", { key }); } catch (e) { toast(e.message, true); }
}

function onNoticeClick(e) {
  const link = e.target.closest("[data-url]");
  if (link) { e.preventDefault(); api("open", { target: link.dataset.url }); return; }
  const x = e.target.closest("[data-dismiss]");
  if (x) { e.stopPropagation(); dismissKey(x.dataset.dismiss); return; }
  const b = e.target.closest("[data-act]");
  if (!b) return;
  if (b.dataset.act === "update-install") {
    b.disabled = true;
    api("update/install").catch((err) => { b.disabled = false; toast(err.message, true); });
  } else if (b.dataset.act === "clip-add") {
    const offer = clipOffer;
    clipOffer = null;
    renderNotices();
    if (!offer) return;
    toast(t("offer.adding"));
    api("offer/accept", { id: offer.id })
      .then((r) => { toast(r.message); viewProfile = null; if (!$("#servers").hidden) renderServers(); })
      .catch((err) => toast(err.message, true));
  } else if (b.dataset.act === "clip-skip") {
    const offer = clipOffer;
    clipOffer = null;
    renderNotices();
    if (offer) api("offer/dismiss", { id: offer.id }).catch(() => {});
  }
}
$("#notices").addEventListener("click", onNoticeClick);
$("#profileInfo").addEventListener("click", (e) => {
  const link = e.target.closest("[data-url]");
  if (link) { e.preventDefault(); e.stopPropagation(); api("open", { target: link.dataset.url }); return; }
  const x = e.target.closest("[data-dismiss]");
  if (x) { e.stopPropagation(); dismissKey(x.dataset.dismiss); }
}, true);

// ------------------------------------------------------------------ clipboard
// When the window comes to the front, Vela looks at the clipboard once.
let clipTimer;
function checkClipboard() {
  clearTimeout(clipTimer);
  clipTimer = setTimeout(async () => {
    if (!snap || (snap.prefs && snap.prefs.noClipboard) || document.hidden) return;
    try {
      const r = await api("clipboard");
      if (r.offer) { clipOffer = r.offer; renderNotices(); }
    } catch (_) {}
  }, 350);
}
window.addEventListener("focus", checkClipboard);
document.addEventListener("visibilitychange", () => { if (!document.hidden) checkClipboard(); });

// ------------------------------------------------------------------ app links
// vela://, happ://, ... opened in the browser: confirm before adding.
function syncOfferDialog() {
  const d = $("#offerDialog");
  const o = snap.offer;
  if (!o) {
    if (shownOffer && d.open) d.close();
    shownOffer = null;
    return;
  }
  if (shownOffer === o.id) return;
  shownOffer = o.id;
  const err = $("#offerError");
  err.hidden = true;
  const add = $("#offerAdd");
  add.disabled = false;
  add.textContent = t("common.add");
  const body = $("#offerBody");
  if (o.error) {
    $("#offerTitle").textContent = t("offer.errorTitle");
    body.innerHTML = `<p class="offer-error">${esc(o.error)}</p>`;
    add.hidden = true;
    $("#offerCancel").textContent = t("common.close");
  } else {
    const subs = o.subscriptions || [];
    $("#offerTitle").textContent = t(subs.length > 1 ? "offer.titleSubs" : subs.length === 1 ? "offer.titleSub" : "offer.titleServers");
    let rows = "";
    if (o.name) rows += `<dt>${esc(t("offer.name"))}</dt><dd>${esc(o.name)}</dd>`;
    if (subs.length) rows += `<dt>${esc(t("offer.host"))}</dt><dd>${(o.hosts || []).map(esc).join("<br>")}</dd>`;
    rows += `<dt>${esc(t("offer.servers"))}</dt><dd>${esc(o.linkCount ? t("count.servers", { n: o.linkCount }) : t("offer.serversLater"))}</dd>`;
    body.innerHTML = `<dl>${rows}</dl>` + (o.existing ? `<p class="hint">${esc(t("offer.exists"))}</p>` : "");
    add.hidden = false;
    $("#offerCancel").textContent = t("common.cancel");
  }
  for (const other of $$("dialog[open]")) if (other !== d) other.close();
  if (!d.open) d.showModal();
  (add.hidden ? $("#offerCancel") : add).focus();
}

$("#offerForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = snap && snap.offer;
  if (!o) return $("#offerDialog").close();
  const add = $("#offerAdd");
  add.disabled = true;
  add.textContent = t("offer.adding");
  try {
    const r = await api("offer/accept", { id: o.id });
    if ($("#offerDialog").open) $("#offerDialog").close();
    toast(r.message);
    viewProfile = null;
    if (!$("#servers").hidden) renderServers();
  } catch (err) {
    $("#offerError").textContent = err.message;
    $("#offerError").hidden = false;
    add.disabled = false;
    add.textContent = t("common.add");
  }
});
function cancelOffer() {
  const o = snap && snap.offer;
  $("#offerDialog").close();
  if (o) api("offer/dismiss", { id: o.id }).catch(() => {});
}
$("#offerCancel").addEventListener("click", cancelOffer);
$("#offerDialog").addEventListener("cancel", (e) => { e.preventDefault(); cancelOffer(); });

// ------------------------------------------------------------------ filters
let regionNames = null, regionLang = "";
function countryName(code) {
  if (regionLang !== LANG) {
    try { regionNames = new Intl.DisplayNames([LANG], { type: "region" }); } catch (_) { regionNames = null; }
    regionLang = LANG;
  }
  try { return (regionNames && regionNames.of(code)) || code; } catch (_) { return code; }
}

function countryOf(name) {
  const { flag } = splitFlag(name);
  if (!flag) return "";
  const cps = [...flag].map((c) => c.codePointAt(0) - 0x1F1E6 + 65);
  return String.fromCharCode(...cps);
}

function fillFilterOptions(p) {
  const protos = [...new Set(p.nodes.map((n) => n.protocol).filter(Boolean))].sort();
  const countries = [...new Set(p.nodes.map((n) => countryOf(n.name)).filter(Boolean))]
    .sort((a, b) => countryName(a).localeCompare(countryName(b), LANG));
  const ps = $("#protoFilter"), cs = $("#countryFilter");
  const protoHTML = `<option value="">${esc(t("servers.allProtocols"))}</option>` + protos.map((x) => `<option value="${esc(x)}">${esc(x)}</option>`).join("");
  const countryHTML = `<option value="">${esc(t("servers.allCountries"))}</option>` + countries.map((c) => {
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
    const hay = [n.name, n.protocol, n.security, n.network, n.server, n.engine, code ? countryName(code) : ""].join(" ").toLowerCase();
    if (!filters.q.split(/\s+/).every((w) => hay.includes(w))) return false;
  }
  return true;
}

// ------------------------------------------------------------------ theme
const darkQuery = window.matchMedia("(prefers-color-scheme: light)");
function applyTheme(theme) {
  const th = theme === "light" || theme === "dark" ? theme : (darkQuery.matches ? "light" : "dark");
  const changed = document.documentElement.dataset.theme !== th;
  document.documentElement.dataset.theme = th;
  document.documentElement.dataset.choice = theme || "system";
  renderGraph();
  if (changed && !$("#stats").hidden && statsData) renderChart(statsData);
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
  $("#graphPeak").textContent = t("home.peak", { speed: fmtSpeed(peak) });
}
window.addEventListener("resize", () => {
  renderGraph();
  if (snap) renderNotices();
  if (!$("#stats").hidden && statsData) renderChart(statsData);
});

// ------------------------------------------------------------------ my ip & speed
let ipAgain = false;
let ipShown = false, speedShown = false;
async function refreshIP() {
  // a check that started before (dis)connecting would show the old address
  if (ipBusy) { ipAgain = true; return; }
  ipBusy = true;
  $("#ipValue").textContent = t("ip.checking");
  try {
    const r = await api("myip");
    $("#ipValue").textContent = (r.flag ? r.flag + " " : "") + r.ip;
    $("#ipMeta").textContent = [r.country, r.city].filter(Boolean).join(", ") || r.isp || "";
    $("#ipCard").title = [r.isp, t("ip.refresh")].filter(Boolean).join(". ");
    ipShown = true;
  } catch (e) {
    $("#ipValue").textContent = t("ip.failed");
    $("#ipMeta").textContent = e.message;
  } finally {
    ipBusy = false;
    if (ipAgain) { ipAgain = false; refreshIP(); }
  }
}

const mbps = (v) => fmtNum(v, v >= 100 ? 0 : 1) + " " + t("units.mbps");
function onSpeed(d) {
  const v = $("#speedValue"), m = $("#speedMeta");
  if (d.phase === "ping") { v.textContent = t("speed.ping", { n: Math.round(d.value) }); m.textContent = t("speed.measuringDown"); }
  else if (d.phase === "download") { v.textContent = "↓ " + mbps(d.value); m.textContent = t("speed.downloading"); }
  else if (d.phase === "upload") { m.textContent = "↓ " + v.textContent.replace("↓ ", "") + ", ↑ " + mbps(d.value); }
  else if (d.phase === "result") {
    $("#speedCard").disabled = false;
    const r = d.result || {};
    if (r.error) { v.textContent = t("speed.failed"); m.textContent = r.error; return; }
    v.textContent = "↓ " + mbps(r.downloadMbps) + "  ↑ " + mbps(r.uploadMbps);
    m.textContent = t("speed.result", { n: r.pingMs, via: t(d.viaVPN ? "speed.viaVpn" : "speed.direct") });
  }
}

async function runSpeed() {
  speedShown = true;
  $("#speedCard").disabled = true;
  $("#speedValue").textContent = t("speed.start");
  $("#speedMeta").textContent = status.state === "connected" ? t("speed.meta") : t("speed.noVpn");
  try { await api("speedtest"); }
  catch (e) { $("#speedCard").disabled = false; $("#speedValue").textContent = t("speed.failed"); $("#speedMeta").textContent = e.message; }
}

// ------------------------------------------------------------------ share & QR
async function share(title, text, hint) {
  $("#shareTitle").textContent = title;
  $("#shareHint").textContent = hint || t("share.hint");
  $("#shareText").value = text;
  const img = $("#shareQR");
  img.hidden = true;
  $("#shareDialog").showModal();
  try {
    const r = await api("qr", { text });
    img.src = r.image;
    img.hidden = false;
  } catch (e) {
    $("#shareHint").textContent = t("share.copyHint", { error: e.message });
  }
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast(t("common.copied")); }
  catch (_) { $("#shareText").select(); document.execCommand("copy"); toast(t("common.copied")); }
}

function readImage(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result);
    r.onerror = () => reject(new Error(t("add.fileError")));
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
    toast(t("add.qrRead"));
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
  }
}

// ------------------------------------------------------------------ settings
const listFields = ["directDomains", "proxyDomains"];
let fillingSettings = false;

function fillSubPrefs() {
  const box = $("#subPrefs");
  if (!box || !snap || box.contains(document.activeElement) && document.activeElement.type === "text") return;
  const sp = snap.sub || {};
  box.querySelector("[data-sub=autoUpdate]").checked = !sp.noAutoUpdate;
  box.querySelector("[data-sub=updateHours]").value = String(sp.updateHours || 0);
  box.querySelector("[data-sub=updateHours]").disabled = !!sp.noAutoUpdate;
  box.querySelector("[data-sub=hwid]").checked = !sp.noHwid;
  box.querySelector("[data-sub=userAgent]").value = sp.userAgent || "";
}

function saveSubPrefs() {
  const box = $("#subPrefs");
  const v = (k) => box.querySelector(`[data-sub=${k}]`);
  const prefs = {
    noAutoUpdate: !v("autoUpdate").checked,
    updateHours: +v("updateHours").value || 0,
    noHwid: !v("hwid").checked,
    userAgent: v("userAgent").value.trim(),
  };
  v("updateHours").disabled = prefs.noAutoUpdate;
  api("subprefs", prefs).catch((e) => toast(e.message, true));
}

// subscription settings have their own endpoint and don't touch the VPN
$("#subPrefs").addEventListener("input", (e) => e.stopPropagation());
$("#subPrefs").addEventListener("change", (e) => { e.stopPropagation(); saveSubPrefs(); });

$("#resetSettings").addEventListener("click", async () => {
  if (!confirm(t("settings.resetConfirm"))) return;
  try { toast((await api("settings/reset")).message || t("settings.resetDone")); } catch (e) { toast(e.message, true); }
});

// App preferences: kill switch, updates, clipboard, language, hotkey.
function renderPrefs() {
  if (!snap) return;
  const pr = snap.prefs || {};
  const box = $("#ksPrefs");
  const ks = box.querySelector("[data-pref=killSwitch]");
  const lan = box.querySelector("[data-pref=killSwitchLan]");
  const tun = snap.settings.mode === "tun";
  let hint = "";
  if (snap.platform !== "windows") hint = t("ks.windowsOnly");
  else if (!snap.killSwitchAvailable) hint = t("ks.needAdmin");
  else if (!tun) hint = t("ks.needTun");
  ks.checked = !!pr.killSwitch;
  // when it can't work the switch can still be turned off
  ks.disabled = !!hint && !pr.killSwitch;
  $("#ksRow").classList.toggle("disabled", !!hint);
  lan.checked = !pr.killSwitchNoLan;
  lan.disabled = !pr.killSwitch || !!hint;
  $("#ksLanRow").classList.toggle("disabled", lan.disabled);
  $("#ksHint").hidden = !hint;
  $("#ksHintText").textContent = hint;
  $("#ksElevate").hidden = !(snap.platform === "windows" && !snap.killSwitchAvailable);

  const app = $("#appPrefs");
  app.querySelector("[data-pref=updates]").checked = !pr.noUpdateCheck;
  app.querySelector("[data-pref=clipboard]").checked = !pr.noClipboard;
  $$("input[name=language]").forEach((r) => (r.checked = r.value === (pr.language || "")));

  const u = snap.update || {};
  let line = t("upd.current", { v: snap.version });
  if (u.available) line = t("upd.newer", { v: snap.version, nv: u.available });
  else if (u.checkedAt) line = t("upd.currentChecked", { v: snap.version, time: new Date(u.checkedAt * 1000).toLocaleString(locale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) });
  if (u.error) line = u.error;
  $("#updStatus").textContent = line;
  $("#updStatus").classList.toggle("bad", !!u.error);
  const cb = $("#checkUpdate");
  cb.disabled = !!u.checking || !!u.installing;
  cb.textContent = u.checking ? t("upd.checking") : t("upd.check");

  if (!capturing) {
    $("#hotkeyBtn").textContent = snap.hotkey || t("hotkey.none");
    $("#hotkeyBtn").classList.toggle("off", !snap.hotkey);
    $("#hotkeyHint").textContent = t("hotkey.hint");
  }
  $("#hotkeyOff").textContent = snap.hotkey ? t("hotkey.off") : t("hotkey.on");
  $("#hotkeyError").hidden = !snap.hotkeyError;
  $("#hotkeyError").textContent = snap.hotkeyError || "";
}

function savePrefs(patch) {
  if (!snap) return;
  snap.prefs = Object.assign({}, snap.prefs, patch);
  renderPrefs();
  api("prefs", snap.prefs).catch((e) => toast(e.message, true));
}

for (const box of [$("#ksPrefs"), $("#appPrefs")]) {
  box.addEventListener("input", (e) => e.stopPropagation());
  box.addEventListener("change", (e) => {
    e.stopPropagation();
    const k = e.target.dataset.pref;
    const on = e.target.checked;
    if (k === "killSwitch") { savePrefs({ killSwitch: on }); toast(t(on ? "ks.on" : "ks.off")); }
    else if (k === "killSwitchLan") savePrefs({ killSwitchNoLan: !on });
    else if (k === "updates") savePrefs({ noUpdateCheck: !on });
    else if (k === "clipboard") savePrefs({ noClipboard: !on });
  });
}
$$("input[name=language]").forEach((r) => r.addEventListener("change", (e) => {
  e.stopPropagation();
  savePrefs({ language: r.value });
}));
$("#ksElevate").addEventListener("click", elevate);
$("#checkUpdate").addEventListener("click", async () => {
  const b = $("#checkUpdate");
  b.disabled = true;
  b.textContent = t("upd.checking");
  try { toast((await api("update/check")).message); }
  catch (e) { toast(e.message, true); }
  finally { renderPrefs(); }
});

// hotkey: click the key button and press a combination
let capturing = false;
function stopCapture() {
  capturing = false;
  $("#hotkeyBtn").classList.remove("capturing");
  renderPrefs();
}
$("#hotkeyBtn").addEventListener("click", () => {
  if (capturing) return stopCapture();
  capturing = true;
  const b = $("#hotkeyBtn");
  b.classList.add("capturing");
  b.textContent = t("hotkey.press");
  $("#hotkeyHint").textContent = t("hotkey.pressHint");
});
$("#hotkeyBtn").addEventListener("blur", () => { if (capturing) stopCapture(); });
window.addEventListener("keydown", async (e) => {
  if (!capturing) return;
  e.preventDefault();
  e.stopPropagation();
  if (e.key === "Escape") return stopCapture();
  let key = "";
  let m;
  if ((m = /^Key([A-Z])$/.exec(e.code))) key = m[1];
  else if ((m = /^Digit(\d)$/.exec(e.code))) key = m[1];
  else if ((m = /^F(\d{1,2})$/.exec(e.code)) && +m[1] >= 1 && +m[1] <= 24) key = "F" + m[1];
  if (!key) return; // a modifier alone: wait for the key
  if (!e.ctrlKey && !e.altKey && !e.metaKey) { $("#hotkeyHint").textContent = t("hotkey.needMod"); return; }
  const combo = [e.ctrlKey && "Ctrl", e.altKey && "Alt", e.shiftKey && "Shift", e.metaKey && "Win", key].filter(Boolean).join("+");
  capturing = false;
  $("#hotkeyBtn").classList.remove("capturing");
  $("#hotkeyBtn").textContent = combo;
  try { await api("hotkey", { hotkey: combo }); toast(t("hotkey.saved", { key: combo })); }
  catch (err) { toast(err.message, true); }
  renderPrefs();
}, true);
$("#hotkeyOff").addEventListener("click", async () => {
  const off = !!(snap && snap.hotkey);
  try {
    await api("hotkey", { hotkey: off ? "off" : "Ctrl+Alt+V" });
    toast(off ? t("hotkey.disabled") : t("hotkey.saved", { key: "Ctrl+Alt+V" }));
  } catch (err) { toast(err.message, true); }
});

function fillSettings() {
  fillSubPrefs();
  renderPrefs();
  $("#appChips").dataset.empty = t("split.none");
  const f = $("#settingsForm");
  if (f.contains(document.activeElement) && document.activeElement.matches("input:not([type=checkbox]):not([type=radio]), textarea")) return;
  fillingSettings = true;
  const s = snap.settings;
  $$("input[name=mode]", f).forEach((r) => (r.checked = r.value === s.mode));
  $$("input[name=routing]", f).forEach((r) => (r.checked = r.value === (s.routing || "smart")));
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
      routing: (f.querySelector("input[name=routing]:checked") || {}).value || snap.settings.routing || "smart",
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
      if (status.state === "connected") toast(t("settings.applied"));
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
  // one overlay sheet at a time
  $$(".sheet").forEach((s) => { if (s !== el && !s.classList.contains("docked")) s.hidden = true; });
  el.hidden = false;
  if (id === "servers") renderServers();
  if (id === "stats") loadStats();
  if (id === "connections") startConnections();
  if (id === "settings") renderPrefs();
  const focusTarget = el.querySelector("[data-close]");
  if (focusTarget) focusTarget.focus();
}
function closeSheets() {
  $$(".sheet").forEach((s) => { if (!s.classList.contains("docked")) s.hidden = true; });
  stopConnections();
}

function openAdd() {
  $("#addError").hidden = true;
  $("#addText").value = "";
  $("#addText").placeholder = t("add.placeholder");
  $("#addDialog").showModal();
  $("#addText").focus();
}

async function submitAdd(e) {
  e.preventDefault();
  const text = $("#addText").value.trim();
  const btn = $("#addSubmit");
  if (!text) { $("#addText").focus(); return; }
  btn.disabled = true;
  btn.textContent = t("add.loading");
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
    btn.textContent = t("common.add");
  }
}

async function pasteFromClipboard() {
  try {
    $("#addText").value = await navigator.clipboard.readText();
  } catch (_) {
    toast(t("add.noClipboard"), true);
    $("#addText").focus();
  }
}

// ------------------------------------------------------------------ statistics
let statsData = null;
let statsTimer = null;

async function loadStats() {
  clearInterval(statsTimer);
  // the numbers are saved every 30 seconds while connected
  statsTimer = setInterval(() => { if ($("#stats").hidden) clearInterval(statsTimer); else loadStats(); }, 30000);
  try {
    statsData = await api("traffic");
    renderStats(statsData);
  } catch (e) {
    toast(t("stats.failed") + ": " + e.message, true);
  }
}

function renderStats(d) {
  const card = (id, v) => {
    $("#" + id).textContent = fmtBytes((v.up || 0) + (v.down || 0));
    $("#" + id + "Sub").innerHTML = `<span>↓ ${esc(fmtBytes(v.down))}</span><span>↑ ${esc(fmtBytes(v.up))}</span>`;
  };
  card("stToday", d.today || {});
  card("stMonth", d.thisMonth || {});
  card("stTotal", d.total || {});
  renderChart(d);
  const months = (d.months || []).slice().reverse();
  const max = Math.max(1, ...months.map((m) => m.up + m.down));
  $("#monthList").innerHTML = months.map((m) => {
    const [y, mo] = m.day.split("-").map(Number);
    const name = new Date(y, mo - 1, 1).toLocaleDateString(locale(), { month: "long" });
    const label = name.charAt(0).toUpperCase() + name.slice(1) + " " + y;
    const total = m.up + m.down;
    return `<li class="${total ? "" : "zero"}"><span class="m-name">${esc(label)}</span>
      <span class="m-bar"><i style="width:${((total / max) * 100).toFixed(1)}%"></i></span>
      <span class="m-val" title="${esc("↓ " + fmtBytes(m.down) + " · ↑ " + fmtBytes(m.up))}">${esc(total ? fmtBytes(total) : "—")}</span></li>`;
  }).join("");
}

// clean axis steps: 1, 2, 5 × 1024^k bytes
function niceMax(v) {
  if (v <= 0) return 1024 * 1024;
  let unit = 1;
  while (v / unit >= 1024) unit *= 1024;
  const x = v / unit;
  const step = [1, 2, 2.5, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 1024].find((s) => s >= x) || 1024;
  return step * unit;
}

function renderChart(d) {
  const box = $("#dayChart");
  const days = d.days || [];
  const css = getComputedStyle(document.documentElement);
  const sea = css.getPropertyValue("--sea").trim(), star = css.getPropertyValue("--star").trim();
  const W = Math.max(280, box.clientWidth || 360), H = 190;
  const left = 46, right = 6, top = 10, bottom = 24;
  const plotW = W - left - right, plotH = H - top - bottom;
  const peak = Math.max(0, ...days.map((x) => x.up + x.down));
  if (!peak) {
    box.innerHTML = `<p class="chart-empty">${esc(t("stats.empty"))}</p>`;
    return;
  }
  const max = niceMax(peak);
  const slot = plotW / days.length;
  const bw = Math.min(24, Math.max(3, slot - Math.max(2, slot * 0.28)));
  const y = (v) => top + plotH - (v / max) * plotH;
  let svg = `<svg viewBox="0 0 ${W} ${H}" width="${W}" height="${H}" role="img" aria-label="${esc(t("stats.days"))}">`;
  for (const f of [0, 0.5, 1]) {
    const gy = Math.round(y(max * f)) + 0.5;
    svg += `<line class="grid" x1="${left}" x2="${W - right}" y1="${gy}" y2="${gy}"/>`;
    svg += `<text class="axis" x="${left - 6}" y="${gy + 4}" text-anchor="end">${esc(f ? fmtBytes(max * f) : "0")}</text>`;
  }
  // a gap in the surface color between the two parts of a column
  const r = Math.min(4, bw / 2);
  const col = (x, y0, y1, color, roundTop) => {
    const h = y0 - y1;
    if (h <= 0.5) return "";
    if (!roundTop || h < r) return `<rect x="${x}" y="${y1}" width="${bw}" height="${h}" fill="${color}"/>`;
    return `<path d="M${x} ${y0}V${y1 + r}a${r} ${r} 0 0 1 ${r} ${-r}h${bw - 2 * r}a${r} ${r} 0 0 1 ${r} ${r}V${y0}Z" fill="${color}"/>`;
  };
  days.forEach((dd, i) => {
    const x = left + i * slot + (slot - bw) / 2;
    const base = top + plotH;
    const yDown = y(dd.down);
    const yTop = y(dd.down + dd.up);
    const upVisible = base - yTop - (base - yDown) > 2.5;
    svg += `<g class="col" data-i="${i}">`;
    svg += `<rect class="hit" x="${left + i * slot}" y="${top}" width="${slot}" height="${plotH}"/>`;
    svg += col(x, base, yDown, sea, !upVisible);
    if (upVisible) svg += col(x, yDown - 2, yTop, star, true);
    svg += `</g>`;
    // a date every week, counted back from today
    if ((days.length - 1 - i) % 7 === 0) {
      const date = new Date(dd.day + "T12:00:00");
      const label = date.toLocaleDateString(locale(), { day: "numeric", month: "short" }).replace(".", "");
      const anchor = i === days.length - 1 ? "end" : "middle";
      const tx = anchor === "end" ? left + (i + 1) * slot : left + (i + 0.5) * slot;
      svg += `<text class="axis" x="${tx}" y="${H - 6}" text-anchor="${anchor}">${esc(label)}</text>`;
    }
  });
  svg += `</svg>`;
  box.innerHTML = svg;
}

function showChartTip(e) {
  const g = e.target.closest && e.target.closest(".col");
  const tip = $("#chartTip");
  $$("#dayChart .col.on").forEach((x) => x !== g && x.classList.remove("on"));
  if (!g || !statsData) { tip.hidden = true; return; }
  g.classList.add("on");
  const dd = statsData.days[+g.dataset.i];
  const date = new Date(dd.day + "T12:00:00").toLocaleDateString(locale(), { weekday: "short", day: "numeric", month: "long" });
  tip.innerHTML = `<b>${esc(fmtBytes(dd.up + dd.down))}</b><span class="tip-date">${esc(date)}</span>
    <span class="tip-row"><i class="k down"></i>${esc(fmtBytes(dd.down))}<em>${esc(t("common.download"))}</em></span>
    <span class="tip-row"><i class="k up"></i>${esc(fmtBytes(dd.up))}<em>${esc(t("common.upload"))}</em></span>`;
  tip.hidden = false;
  // beside the column, not over it
  const card = $("#dayChart").getBoundingClientRect();
  const r = g.getBoundingClientRect();
  const tw = tip.offsetWidth;
  const mid = r.left - card.left + r.width / 2;
  let x = mid > card.width / 2 ? mid - r.width / 2 - tw - 4 : mid + r.width / 2 + 4;
  x = Math.max(0, Math.min(card.width - tw, x));
  tip.style.left = (x + 14) + "px";
}
$("#dayChart").addEventListener("pointermove", showChartTip);
$("#dayChart").addEventListener("pointerleave", () => { $("#chartTip").hidden = true; $$("#dayChart .col.on").forEach((x) => x.classList.remove("on")); });

// ------------------------------------------------------------------ connections
let connTimer = null;
let connData = { connected: false, connections: [] };
let connQuery = "";

function startConnections() {
  stopConnections();
  loadConnections();
  connTimer = setInterval(() => {
    if ($("#connections").hidden || document.hidden) return;
    loadConnections();
  }, 1500);
}
function stopConnections() {
  clearInterval(connTimer);
  connTimer = null;
}

let connBusy = false;
async function loadConnections() {
  if (connBusy) return;
  connBusy = true;
  try {
    connData = await api("connections");
    renderConnections();
  } catch (e) {
    connData = { connected: status.state === "connected", connections: [], error: e.message };
    renderConnections();
  } finally {
    connBusy = false;
  }
}

function ruleLabel(rule) {
  const r = rule || "";
  if (!r || r === "final") return t("conn.rule.final");
  if (/hijack-dns|protocol=dns/.test(r)) return t("conn.rule.dns");
  if (/ip_is_private/.test(r)) return t("conn.rule.private");
  if (/rule_set=.*\bads\b/.test(r)) return t("conn.rule.ads");
  if (/ru-blocked|ru-inside/.test(r)) return t("conn.rule.blocked");
  if (/geosite-ru|geoip-ru/.test(r)) return t("conn.rule.ru");
  if (/process_/.test(r)) return t("conn.rule.app");
  return t("conn.rule.custom");
}

function serverLabel(c) {
  if (c.server) return splitFlag(c.server).name || c.server;
  const o = c.outbound || "";
  if (o === "direct" || o === "") return t("conn.direct");
  if (o === "block" || /reject/.test(c.rule || "")) return t("conn.blocked");
  return o;
}

function fmtAge(ms) {
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return t("conn.age.s", { n: s });
  if (s < 3600) return t("conn.age.m", { n: Math.floor(s / 60) });
  return t("conn.age.h", { n: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60) });
}

function connMatches(c, q) {
  if (!q) return true;
  const hay = [c.host, c.destination, c.process, serverLabel(c), ruleLabel(c.rule), c.network].join(" ").toLowerCase();
  return q.split(/\s+/).every((w) => hay.includes(w));
}

// rows are updated in place, so a click on «Close» is never lost to a re-render
function renderConnections() {
  const list = $("#connList");
  const connected = connData.connected;
  $("#connTools").hidden = !connected;
  $("#closeAll").hidden = !connected || !connData.connections.length;
  if (!connected) {
    list.innerHTML = `<li class="empty"><b>${esc(t("conn.offTitle"))}</b>${esc(t("conn.offText"))}</li>`;
    list.dataset.mode = "off";
    return;
  }
  const all = connData.connections || [];
  const shown = all.filter((c) => connMatches(c, connQuery));
  let up = 0, down = 0;
  for (const c of all) { up += c.upload; down += c.download; }
  $("#connSummary").textContent = (shown.length !== all.length ? t("conn.shown", { n: shown.length, total: all.length }) + " · " : "") +
    t("conn.summary", { count: t("count.conns", { n: all.length }), down: fmtBytes(down), up: fmtBytes(up) });
  if (connData.error && !all.length) {
    list.innerHTML = `<li class="empty"><b>${esc(t("conn.failed"))}</b>${esc(connData.error)}</li>`;
    list.dataset.mode = "msg";
    return;
  }
  if (!shown.length) {
    list.innerHTML = `<li class="empty"><b>${esc(t(all.length ? "conn.nothing" : "conn.none"))}</b></li>`;
    list.dataset.mode = "msg";
    return;
  }
  if (list.dataset.mode !== "rows") { list.innerHTML = ""; list.dataset.mode = "rows"; }
  const existing = new Map($$(":scope > li[data-id]", list).map((li) => [li.dataset.id, li]));
  let prev = null;
  for (const c of shown) {
    let li = existing.get(c.id);
    if (li) existing.delete(c.id);
    else {
      li = document.createElement("li");
      li.className = "conn";
      li.dataset.id = c.id;
      li.innerHTML = `<span class="conn-net"></span>
        <span class="conn-body"><span class="conn-host"></span><span class="conn-meta"></span></span>
        <span class="conn-nums"><span class="conn-bytes"></span><span class="conn-age"></span></span>
        <button class="icon-btn conn-x">${xIcon}</button>`;
    }
    const via = serverLabel(c);
    const meta = [c.process, via, ruleLabel(c.rule)].filter(Boolean).join(" · ");
    setText($(".conn-net", li), (c.network || "").toUpperCase());
    // a site by name; a bare address with its port
    const ipOnly = !c.host || (c.destination || "").startsWith(c.host + ":") || (c.destination || "").startsWith("[" + c.host + "]");
    setText($(".conn-host", li), ipOnly ? (c.destination || c.host) : c.host);
    $(".conn-host", li).title = c.destination || "";
    setText($(".conn-meta", li), meta);
    $(".conn-meta", li).title = c.rule || "";
    li.classList.toggle("direct", !c.server);
    setText($(".conn-bytes", li), "↓ " + fmtBytes(c.download) + "  ↑ " + fmtBytes(c.upload));
    setText($(".conn-age", li), fmtAge(c.start));
    const x = $(".conn-x", li);
    x.title = t("conn.close");
    x.setAttribute("aria-label", t("conn.close"));
    const want = prev ? prev.nextSibling : list.firstChild;
    if (want !== li) list.insertBefore(li, want);
    prev = li;
  }
  for (const li of existing.values()) li.remove();
}

function setText(el, s) { if (el.textContent !== s) el.textContent = s; }

$("#connList").addEventListener("click", async (e) => {
  const x = e.target.closest(".conn-x");
  if (!x) return;
  const li = x.closest("li");
  li.classList.add("closing");
  try { await api("connections/close", { id: li.dataset.id }); loadConnections(); }
  catch (err) { li.classList.remove("closing"); toast(err.message, true); }
});
$("#closeAll").addEventListener("click", async () => {
  try { await api("connections/close", { id: "" }); toast(t("conn.allClosed")); loadConnections(); }
  catch (err) { toast(err.message, true); }
});
$("#connSearch").addEventListener("input", (e) => { connQuery = e.target.value.trim().toLowerCase(); renderConnections(); });

// ------------------------------------------------------------------ wiring
$("#toggle").addEventListener("click", toggle);
$("#openSettings").addEventListener("click", () => openSheet("settings"));
$("#openStats").addEventListener("click", () => openSheet("stats"));
$("#openConnections").addEventListener("click", () => openSheet("connections"));
$("#currentServer").addEventListener("click", () => {
  if (snap && snap.profiles.length === 0) return openAdd();
  if (window.innerWidth >= 900) return;
  openSheet("servers");
});
$$("[data-close]").forEach((b) => b.addEventListener("click", closeSheets));
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !document.querySelector("dialog[open]")) closeSheets(); });

$("#ksReconnect").addEventListener("click", () => api("connect").catch((e) => toast(e.message, true)));
$("#ksOff").addEventListener("click", () => { savePrefs({ killSwitch: false }); toast(t("ks.off")); });

$$(".routing button").forEach((b) => b.addEventListener("click", () => {
  if (!snap || snap.settings.routing === b.dataset.routing) return;
  snap.settings.routing = b.dataset.routing;
  renderRouting();
  fillSettings();
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
    b.textContent = t("profile.updating");
    try { toast((await api("profile/update", { id: p.id })).message); }
    catch (err) { toast(err.message, true); }
    finally { b.disabled = false; b.textContent = t("profile.update"); }
  } else if (b.dataset.act === "update-all") {
    b.disabled = true;
    b.textContent = t("profile.updating");
    try { toast((await api("profile/update-all")).message); }
    catch (err) { toast(err.message, true); }
    finally { b.disabled = false; b.textContent = t("profile.updateAll"); }
  } else if (b.dataset.act === "rename") {
    openRename(p);
  } else if (b.dataset.act === "share") {
    share(t("profile.shareTitle", { name: p.name }), p.url, t("profile.shareHint"));
  } else if (b.dataset.act === "support") {
    api("open", { target: p.supportUrl });
  } else if (b.dataset.act === "delete") {
    if (confirm(p.kind === "manual" ? t("profile.confirmDeleteManual") : t("profile.confirmDelete", { name: p.name }))) {
      try { await api("profile/delete", { id: p.id }); viewProfile = null; toast(t("profile.deleted")); }
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
  const one = e.target.closest("[data-pingone]");
  if (one) {
    e.stopPropagation();
    pings[one.dataset.pingone] = 0;
    delete reasons[one.dataset.pingone];
    renderPings();
    api("ping/node", { profile: viewProfile, node: one.dataset.pingone }).catch((err) => toast(err.message, true));
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

$("#probeStop").addEventListener("click", stopProbe);
$("#refreshSub").addEventListener("click", async () => {
  const p = snap && snap.profiles.find((x) => x.id === viewProfile);
  const b = $("#refreshSub");
  if (b.disabled) return;
  b.disabled = true; b.classList.add("spin");
  try {
    if (p && p.kind === "subscription") { await api("profile/update", { id: p.id }); toast(t("profile.updatedToast")); }
    else toast((await api("profile/update-all")).message || t("profile.allUpdated"));
  } catch (e) { toast(e.message, true); }
  finally { b.disabled = false; b.classList.remove("spin"); }
});
$("#pingAll").addEventListener("click", () => {
  if (probe) return stopProbe();
  if (!viewProfile) return;
  const p = snap.profiles.find((x) => x.id === viewProfile);
  if (!p) return;
  p.nodes.forEach((n) => { pings[n.id] = 0; delete reasons[n.id]; });
  probe = { done: 0, total: p.nodes.length };
  renderProbe();
  renderPings();
  api("ping", { profile: viewProfile }).catch((e) => toast(e.message, true));
});

$("#sortToggle").setAttribute("aria-pressed", String(sortByPing));
$("#sortToggle").addEventListener("click", () => {
  sortByPing = !sortByPing;
  try { localStorage.setItem("vela-sort", sortByPing ? "ping" : ""); } catch (_) {}
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

// switches and selects apply at once; text fields when you leave them, so
// typing a DNS address doesn't reconnect the VPN on every letter
$("#settingsForm").addEventListener("input", (e) => {
  if (!e.target.matches("textarea, input[type=text], input[type=number], input:not([type])")) saveSettings();
});
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

// ------------------------------------------------------------------ rules & apps
function renderRules() {
  const ul = $("#rulesList");
  ul.innerHTML = editRules.map((r, i) => `<li data-i="${i}">
      <input data-k="match" value="${esc(r.match)}" placeholder="example.com" spellcheck="false" aria-label="${esc(t("rule.match"))}">
      <select data-k="action" aria-label="${esc(t("rule.action"))}">
        <option value="direct"${r.action === "direct" ? " selected" : ""}>${esc(t("rule.direct"))}</option>
        <option value="proxy"${r.action === "proxy" ? " selected" : ""}>${esc(t("rule.proxy"))}</option>
        <option value="block"${r.action === "block" ? " selected" : ""}>${esc(t("rule.block"))}</option>
      </select>
      <button type="button" class="icon-btn" data-rm="${i}" aria-label="${esc(t("rule.remove"))}"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>
    </li>`).join("");
}

function renderApps() {
  const f = $("#settingsForm");
  $("#appsBox").hidden = !f.appMode.value;
  $("#appChips").innerHTML = editApps.map((a, i) =>
    `<span class="app-chip">${esc(a)}<button type="button" data-rm-app="${i}" aria-label="${esc(t("split.remove", { name: a }))}">×</button></span>`).join("");
}

$("#rulesList").addEventListener("input", (e) => {
  const li = e.target.closest("li");
  if (!li) return;
  editRules[+li.dataset.i][e.target.dataset.k] = e.target.value;
  e.stopPropagation();
});
$("#rulesList").addEventListener("change", (e) => {
  const li = e.target.closest("li");
  if (li) editRules[+li.dataset.i][e.target.dataset.k] = e.target.value;
  e.stopPropagation();
  saveSettings();
});
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
    || `<li class="hint">${esc(t("split.notFound"))}</li>`;
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
    $("#shareTitle").textContent = t("backup.title");
    $("#shareHint").textContent = r.image ? t("backup.qrHint") : t("backup.bigHint");
    $("#shareText").value = r.text;
    const img = $("#shareQR");
    img.hidden = !r.image;
    if (r.image) img.src = r.image;
    $("#shareDialog").showModal();
  } catch (e) { toast(e.message, true); }
});
$("#importBtn").addEventListener("click", () => {
  openAdd();
  $("#addText").placeholder = t("backup.placeholder");
});
$("#shareCopy").addEventListener("click", () => copyText($("#shareText").value));

async function loadLogs() {
  try {
    const r = await api("logs");
    const pre = $("#logsText");
    pre.textContent = (r.lines || []).join("\n") || t("logs.empty");
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
