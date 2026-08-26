/* Console - the app window's behaviour.
 *
 * Go owns the data: what the agents are, which of them pass the filter, and
 * what order they go in. This file owns the moment: where the cursor is,
 * which groups are collapsed, and how a keypress feels. That split is why
 * the filter and the sort go back over the bridge instead of being redone
 * here - two front ends that sort differently are worse than a round trip.
 *
 * Go pushes frames by calling __snapshot; commands go back through cmd().
 */

const $ = (id) => document.getElementById(id);

const STATUS_ORDER = ["waiting", "busy", "idle", "shell", "dead"];
const BAND_LABEL = { waiting: "Needs you", busy: "Working", idle: "Idle", shell: "Shell", dead: "Ended" };
const QUEUE = ["waiting", "busy", "idle", "dead"];
const SORT_KEYS = [
  ["status", "Status"], ["age", "Age"], ["context", "Context"],
  ["name", "Name"], ["repo", "Repo"], ["dir", "Dir"],
];
const ABBREV = { waiting: "wait", busy: "busy", idle: "idle", shell: "shell", dead: "dead" };

// Thresholds the design fixes: a context bar goes red once compaction is
// close, and a usage meter once more than half the window is spent.
const CONTEXT_ALERT = 85;
const USAGE_ALERT = 60;

const FLASH_FRAMES = 4;
const FLASH_MS = 70;

// Measured from the stylesheet, not from the DOM. Density has to be decided
// from constants or it oscillates: a shorter row makes more rows fit, which
// argues for a taller row, which argues for a shorter one.
const COMFY_ROW_H = 44;
const COMPACT_ROW_H = 30;

// How much height an expanded readout takes. The stylesheet carries a
// starting estimate - it knows the readout goes two-by-two below 1200 - and
// the first expanded render replaces it with the real measurement.
//
// Only expanded renders update it, which is what keeps this from feeding
// back on itself: measuring a collapsed readout would say the readout is
// small, which would argue for expanding it, which would make it large.
function readoutHeight() {
  if (state.readoutH) return state.readoutH;
  const raw = getComputedStyle(document.documentElement).getPropertyValue("--readout-h");
  return parseInt(raw, 10) || 340;
}

const state = {
  snap: null,
  generation: 0,
  cursor: null,          // {pid, sessionId} - survives re-sorts and re-polls
  queue: null,           // status filtering from the sidebar, or null
  collapsed: new Set(["dead"]),
  capacity: 0,
  readoutH: 0,      // measured; the stylesheet's estimate until then
  typing: false,
  confirmingStop: null,
  noticeTimer: null,
};

/* ------------------------------------------------------------ utilities */

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

// Every string that reaches the page comes from a registry file or a
// transcript, neither of which is ours to trust. textContent everywhere;
// there is no innerHTML in this file.
function line(cls, text) { return el("span", cls, text); }

function bar(pct, over) {
  const b = el("span", over ? "bar over" : "bar");
  const fill = el("i");
  const track = el("u");
  fill.style.flex = String(Math.max(pct, 0));
  track.style.flex = String(Math.max(100 - pct, 0));
  b.append(fill, track);
  return b;
}

function key(a) { return a ? a.pid + " " + a.sessionId : ""; }

function send(obj) {
  try { cmd(JSON.stringify(obj)); } catch (e) { /* the window is closing */ }
}

function notice(text, alert) {
  const n = $("notice");
  n.textContent = text || "";
  n.classList.toggle("alert", !!alert);
  clearTimeout(state.noticeTimer);
  if (text) state.noticeTimer = setTimeout(() => { if (n.textContent === text) n.textContent = ""; }, 6000);
}

/* ------------------------------------------------------------ selection */

function agents() {
  const all = state.snap ? state.snap.agents : [];
  return state.queue ? all.filter((a) => a.status === state.queue) : all;
}

function selected() {
  const rows = agents();
  const found = rows.find((a) => key(a) === key(state.cursor));
  return found || rows[0] || null;
}

// visibleRows is what the cursor can land on: rows inside a collapsed group
// are still listed and still sorted, but skipping them is what makes a
// collapse worth having.
function visibleRows() {
  if (!state.snap || !state.snap.grouped) return agents();
  return agents().filter((a) => !state.collapsed.has(a.status));
}

function moveCursor(delta) {
  const rows = visibleRows();
  if (!rows.length) return;
  let i = rows.findIndex((a) => key(a) === key(state.cursor));
  if (i < 0) i = 0;
  else i = Math.min(Math.max(i + delta, 0), rows.length - 1);
  state.cursor = { pid: rows[i].pid, sessionId: rows[i].sessionId };
  render();
  scrollCursorIntoView();
}

// Scroll by whole rows rather than calling scrollIntoView, which jumps the
// container around when the cursor is only just off the edge.
function scrollCursorIntoView() {
  const row = $("list").querySelector('[aria-selected="true"]');
  if (!row) return;
  const box = $("list");
  const top = row.offsetTop;
  const bottom = top + row.offsetHeight;
  if (top < box.scrollTop) box.scrollTop = top;
  else if (bottom > box.scrollTop + box.clientHeight) box.scrollTop = bottom - box.clientHeight;
}

/* ------------------------------------------------------------- rendering */

function render() {
  const s = state.snap;
  if (!s) return;
  document.body.classList.toggle("compact", s.compact);
  // A column no agent on this machine can fill leaves the table. Without
  // the statusline shim that is the whole context column, and without a
  // readable transcript it is the model column too.
  document.body.classList.toggle("no-model", !s.agents.some((a) => a.model));
  document.body.classList.toggle("no-context",
    !s.agents.some((a) => a.contextPct !== null && a.contextPct !== undefined));
  renderSidebar(s);
  renderToolbar(s);
  renderList(s);
  renderReadout(selected());
  // Measured after painting, not at load: the bridge that carries the
  // report may not be bound yet when this file first runs.
  reportCapacity();
}

function renderSidebar(s) {
  const counts = {};
  for (const a of s.agents) counts[a.status] = (counts[a.status] || 0) + 1;
  const waiting = counts.waiting || 0;

  const n = s.agents.length;
  let tally = n + (n === 1 ? " agent" : " agents");
  if (s.total !== n) tally = n + " of " + s.total;
  if (waiting) tally += " · " + waiting + " waiting";
  $("tally").textContent = tally;
  $("tally").classList.toggle("alert", waiting > 0);

  const q = $("queue");
  q.replaceChildren();
  for (const status of QUEUE) {
    const b = el("button", null, BAND_LABEL[status]);
    b.type = "button";
    b.setAttribute("role", "option");
    b.setAttribute("aria-selected", String(state.queue === status));
    b.append(el("span", null, String(counts[status] || 0)));
    b.onclick = () => { state.queue = state.queue === status ? null : status; render(); };
    q.append(b);
  }

  facets("repos", "repolist", s.agents, (a) => a.repo, (a) => (a.branch ? a.branch + (a.dirty ? "*" : "") : ""));
  facets("models", "modellist", s.agents, (a) => a.model, () => "");

  meter("usage7d", s.account.usage7dPct, s.account.usage7dResetsIn ? "resets in " + s.account.usage7dResetsIn : "");

  const age = s.polledAt ? Math.max(0, Math.round((Date.now() - Date.parse(s.polledAt)) / 1000)) : null;
  $("polled").textContent = age === null ? "" : "polled " + age + "s ago";
}

// facets builds a sidebar filter list, and hides the whole block when the
// field it groups by has no source on this machine.
function facets(blockId, listId, rows, keyOf, noteOf) {
  const groups = new Map();
  for (const a of rows) {
    const k = keyOf(a);
    if (!k) continue;
    if (!groups.has(k)) groups.set(k, { n: 0, note: noteOf(a) });
    groups.get(k).n++;
  }
  const block = $(blockId);
  block.hidden = groups.size === 0;
  const list = $(listId);
  list.replaceChildren();
  const active = $("filterInput").value;
  for (const [k, g] of [...groups].sort((a, b) => b[1].n - a[1].n)) {
    const b = el("button", "facet");
    b.type = "button";
    b.setAttribute("aria-pressed", String(active === k));
    b.title = k;
    b.append(el("b", null, k));
    b.append(el("span", null, (g.note ? g.note + " · " : "") + g.n));
    b.onclick = () => setFilter(active === k ? "" : k);
    list.append(b);
  }
}

function meter(id, pct, note) {
  const box = $(id);
  // A missing reading means the shim is not installed for any session. The
  // panel is omitted rather than shown at zero, which would be a lie.
  box.hidden = pct === null || pct === undefined;
  if (box.hidden) return;
  $(id + "Pct").textContent = pct + "%";
  const b = $(id + "Bar");
  b.replaceChildren(...bar(pct, pct >= USAGE_ALERT).childNodes);
  b.classList.toggle("over", pct >= USAGE_ALERT);
  $(id + "Note").textContent = note;
}

function renderToolbar(s) {
  const keys = $("sortKeys");
  if (!keys.childElementCount) {
    for (const [k, label] of SORT_KEYS) {
      const b = el("button", null, label);
      b.type = "button";
      b.dataset.sort = k;
      b.onclick = () => send({ cmd: "sort", key: k });
      keys.append(b);
    }
  }
  for (const b of keys.children) b.setAttribute("aria-pressed", String(b.dataset.sort === s.sort));
  for (const b of $("viewKeys").children) {
    b.setAttribute("aria-pressed", String((b.dataset.group === "1") === s.grouped));
    b.onclick = () => send({ cmd: "group", on: b.dataset.group === "1" });
  }
  for (const b of document.querySelectorAll("[data-density]")) {
    b.setAttribute("aria-pressed", String((b.dataset.density === "compact") === s.compact));
    b.onclick = () => send({ cmd: "density", value: b.dataset.density });
  }
  for (const b of $("head").querySelectorAll("[data-sort]")) {
    if (b.dataset.sort === s.sort) b.setAttribute("aria-sort", "ascending");
    else b.removeAttribute("aria-sort");
    b.onclick = () => send({ cmd: "sort", key: b.dataset.sort });
  }
  if (!$("filterInput").matches(":focus")) $("filterInput").value = s.filter;
}

function renderList(s) {
  const err = $("error");
  err.hidden = !s.error;
  err.textContent = s.error || "";

  const list = $("list");
  const rows = agents();
  $("empty").hidden = rows.length > 0;
  list.replaceChildren();

  const cur = key(selected());
  if (!s.grouped) {
    for (const a of rows) list.append(rowEl(a, cur, s));
    return;
  }
  for (const status of STATUS_ORDER) {
    const group = rows.filter((a) => a.status === status);
    if (!group.length) continue;
    list.append(bandEl(status, group.length));
    if (state.collapsed.has(status)) continue;
    for (const a of group) list.append(rowEl(a, cur, s));
  }
  // A status the design did not name still has to appear; an unrecognised
  // status must never make an agent invisible.
  const rest = rows.filter((a) => !STATUS_ORDER.includes(a.status));
  if (rest.length) {
    list.append(bandEl("", rest.length));
    for (const a of rest) list.append(rowEl(a, cur, s));
  }
}

const BAND_COLOUR = {
  waiting: "var(--alert)", busy: "var(--busy)", idle: "var(--idle)",
  shell: "var(--accent-ink)", dead: "var(--muted)",
};

function bandEl(status, n) {
  const b = el("div", "band");
  const label = el("span", null, (BAND_LABEL[status] || "Unknown") + " · " + n);
  label.style.color = BAND_COLOUR[status] || "var(--muted)";
  b.append(label);
  if (status === "dead") {
    b.append(el("span", "hint", state.collapsed.has("dead")
      ? "collapsed - press e to expand"
      : "press e to collapse"));
    b.onclick = toggleEnded;
  }
  return b;
}

function rowEl(a, cur, s) {
  const r = el("div", "row");
  r.setAttribute("role", "row");
  r.dataset.status = a.status || "";
  r.dataset.live = String(a.live);
  r.setAttribute("aria-selected", String(key(a) === cur));

  r.append(line("dot", a.live ? "●" : "○"));
  r.append(line("status", s.compact ? (ABBREV[a.status] || "?") : (a.status || "?")));
  r.append(line("age", a.age));

  const nameCell = el("span", "name-cell");
  nameCell.append(line("name ellipsis", a.name));
  const sub = [a.model, repoLabel(a)].filter(Boolean).join(" · ");
  if (sub) nameCell.append(line("sub ellipsis", sub));
  r.append(nameCell);

  r.append(line("model ellipsis", modelLabel(a)));
  r.append(ctxCell(a));
  r.append(line("repo ellipsis", repoLabel(a) || "—"));
  r.append(line("summary ellipsis", a.summary || ""));

  r.onclick = () => { state.cursor = { pid: a.pid, sessionId: a.sessionId }; render(); };
  r.ondblclick = focusAgent;
  return r;
}

// The model column carries its context window with it: "Sonnet 4.6" and
// "Sonnet 4.6 · 1M" are meaningfully different sessions.
function modelLabel(a) {
  if (!a.model) return "—";
  return a.contextWindow ? a.model + " · " + a.contextWindow : a.model;
}

function repoLabel(a) {
  if (!a.repo) return "";
  return a.repo + (a.branch ? " " + a.branch : "") + (a.dirty ? "*" : "");
}

function ctxCell(a) {
  const c = el("span", "ctx");
  if (a.contextPct === null || a.contextPct === undefined) {
    // No denominator means no percentage. A bar at zero would read as an
    // empty context window, which is a different and wrong claim.
    c.append(line("pct", "—"));
    return c;
  }
  const over = a.contextPct >= CONTEXT_ALERT;
  c.append(bar(a.contextPct, over));
  c.append(line(over ? "pct over" : "pct", a.contextPct + "%"));
  return c;
}

/* -------------------------------------------------------------- readout */

function renderReadout(a) {
  const cells = $("cells");
  cells.replaceChildren();
  const readout = $("readout").classList;

  if (!a) {
    $("readlabel").textContent = "Selection";
    $("readwhere").textContent = "";
    $("last").hidden = true;
    $("focusBtn").disabled = true;
    $("stopBtn").disabled = true;
    readout.remove("strip");
    return;
  }
  $("readlabel").textContent = "Selection · " + a.name;
  $("focusBtn").disabled = !a.focusable;
  $("stopBtn").disabled = !a.live;

  // The four-cell readout costs about 130px of height. Once the list is
  // already overflowing, that height is worth more to the list.
  const strip = stripped();
  readout.toggle("strip", strip);
  if (strip) {
    $("readwhere").textContent = stripLine(a);
    $("last").hidden = true;
    return;
  }
  $("readwhere").textContent = ["pid " + a.pid, a.tty, a.tmux ? "tmux " + a.tmux : ""]
    .filter(Boolean).join(" · ");

  cells.append(sessionCell(a));
  const ctx = contextCell(a);
  if (ctx) cells.append(ctx);
  const usage = usageCell();
  if (usage) cells.append(usage);
  cells.append(locationCell(a));

  $("last").hidden = !a.summary;
  $("last").textContent = a.summary || "";
  state.readoutH = Math.round($("readout").getBoundingClientRect().height);
}

// listSpace is the height the list has to work with once the toolbar and an
// expanded readout have taken theirs.
function listSpace() {
  return $("main").clientHeight - $("toolbar").offsetHeight - readoutHeight() - 40;
}

// The four-cell readout costs about 250px. Once the list cannot fit even at
// compact density, that height is worth more to the list than to the cells.
function stripped() {
  if (!state.snap || !state.snap.compact) return false;
  return agents().length * COMPACT_ROW_H > listSpace();
}

// reportCapacity tells Go how many comfortable rows fit, so the density
// decision is about the window rather than about a number of agents.
function reportCapacity() {
  const fit = Math.max(1, Math.floor(listSpace() / COMFY_ROW_H));
  if (fit !== state.capacity) {
    state.capacity = fit;
    send({ cmd: "capacity", rows: fit });
  }
}

function stripLine(a) {
  return [
    a.model,
    a.contextPct === null || a.contextPct === undefined ? "" : "ctx " + a.contextPct + "%",
    a.elapsed, repoLabel(a), a.tty,
    a.permissionMode ? a.permissionMode + " mode" : "",
  ].filter(Boolean).join(" · ");
}

function cell(label) {
  const c = el("div", "cell");
  c.append(el("span", "label", label));
  return c;
}

function sessionCell(a) {
  const c = cell("Session");
  if (a.model) {
    const l = line("line", a.model);
    if (a.contextWindow) l.append(line("dim", " · " + a.contextWindow + " context"));
    c.append(l);
  }
  if (a.repo) {
    const l = line("line", a.repo);
    if (a.branch) l.append(line("branch", " git:(" + a.branch + (a.dirty ? "*" : "") + ")"));
    c.append(l);
  }
  if (a.elapsed) c.append(line("line soft", "elapsed " + a.elapsed));
  if (a.permissionMode) c.append(el("span", "chip", a.permissionMode + " mode"));
  return c;
}

function contextCell(a) {
  if (a.contextPct === null || a.contextPct === undefined) return null;
  const c = cell("Context");
  const over = a.contextPct >= CONTEXT_ALERT;
  const big = el("div", "big");
  big.append(el("b", null, a.contextPct + "%"), el("span", null, a.contextLabel || ""));
  c.append(big, bar(a.contextPct, over));
  if (over) c.append(line("note", "compaction is close"));
  return c;
}

// The five-hour window rather than the seven-day one: the sidebar already
// shows seven days, and a readout cell that repeats the sidebar and never
// changes as the cursor moves is a quarter of the readout wasted.
function usageCell() {
  const acct = state.snap.account;
  const pct = acct.usage5hPct;
  if (pct === null || pct === undefined) return null;
  const c = cell("Usage · 5 hours");
  const big = el("div", "big");
  big.append(el("b", null, pct + "%"), el("span", null, "all agents"));
  c.append(big, bar(pct, pct >= USAGE_ALERT));
  c.append(line("note", acct.usage5hResetsIn ? "resets in " + acct.usage5hResetsIn : ""));
  return c;
}

// Where the agent is, which is the thing this window exists to answer. The
// design's Environment cell wanted CLAUDE.md, MCP and tool tallies; none of
// those have a source yet, and a cell full of dashes is worse than a cell
// showing something true.
function locationCell(a) {
  const c = cell("Location");
  if (a.tty) c.append(line("line", a.tty));
  if (a.tmux) c.append(line("line", "tmux " + a.tmux));
  if (!a.focusable) c.append(line("line soft", a.live ? "not focusable" : "no longer running"));
  c.append(line("line soft ellipsis", a.cwdShort));
  return c;
}

/* --------------------------------------------------------------- actions */

function focusAgent() {
  const a = selected();
  if (!a) return;
  if (!a.focusable) {
    notice(a.live ? "not focusable" : "agent is gone; nothing to focus", true);
    return;
  }
  flash();
  notice("focusing " + a.name + "…");
  send({ cmd: "focus", pid: a.pid, sessionId: a.sessionId });
}

// Focusing takes a few hundred milliseconds of AppleScript, long enough to
// wonder whether the keypress registered. The row blinks for the duration.
function flash() {
  const row = $("list").querySelector('[aria-selected="true"]');
  if (!row) return;
  const frames = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 1 : FLASH_FRAMES;
  let n = 0;
  const tick = () => {
    row.classList.toggle("flash");
    if (++n < frames * 2) setTimeout(tick, FLASH_MS);
    else row.classList.remove("flash");
  };
  tick();
}

function stopAgent() {
  const a = selected();
  if (!a) return;
  if (!a.live) { notice("agent is already gone", true); return; }
  state.confirmingStop = a;
  notice("stop “" + a.name + "” (SIGTERM pid " + a.pid + ")? y/N", true);
}

function toggleEnded() {
  if (state.collapsed.has("dead")) state.collapsed.delete("dead");
  else state.collapsed.add("dead");
  render();
}

function setFilter(q) {
  $("filterInput").value = q;
  send({ cmd: "filter", q: q });
}

/* -------------------------------------------------------------- keyboard */

const SORT_BY_KEY = { s: "status", a: "age", c: "context", n: "name", r: "repo", d: "dir" };

window.addEventListener("keydown", (e) => {
  // cmd-q: this window has no menu bar, so macOS gives us no quit of our
  // own. Catch it before anything else sees the key.
  if (e.metaKey && !e.ctrlKey && !e.altKey && e.key.toLowerCase() === "q") {
    e.preventDefault();
    send({ cmd: "quit" });
    return;
  }
  if (e.metaKey && e.key === "1") {
    e.preventDefault();
    $("app").classList.toggle("no-sidebar");
    $("sidebar").classList.toggle("hidden");
    return;
  }

  if (state.confirmingStop) {
    const a = state.confirmingStop;
    state.confirmingStop = null;
    e.preventDefault();
    if (e.key === "y") send({ cmd: "stop", pid: a.pid, sessionId: a.sessionId });
    else notice("stop cancelled");
    return;
  }

  if (state.typing) {
    if (e.key === "Escape") { e.preventDefault(); setFilter(""); $("filterInput").blur(); }
    else if (e.key === "Enter") { e.preventDefault(); $("filterInput").blur(); }
    else if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      moveCursor(e.key === "ArrowUp" ? -1 : 1);
    }
    return;
  }

  switch (e.key) {
    case "ArrowUp": case "k": e.preventDefault(); moveCursor(-1); return;
    case "ArrowDown": case "j": e.preventDefault(); moveCursor(1); return;
    case "Enter": e.preventDefault(); focusAgent(); return;
    case "/": e.preventDefault(); $("filterInput").focus(); return;
    case "Escape": e.preventDefault(); state.queue = null; setFilter(""); return;
    case "g": e.preventDefault(); moveCursor(-1e6); return;
    case "G": e.preventDefault(); moveCursor(1e6); return;
    case "e": e.preventDefault(); toggleEnded(); return;
    case "q": e.preventDefault(); send({ cmd: "quit" }); return;
  }
  if (e.ctrlKey && e.key.toLowerCase() === "x") { e.preventDefault(); stopAgent(); return; }
  if (!e.ctrlKey && !e.metaKey && SORT_BY_KEY[e.key]) {
    e.preventDefault();
    send({ cmd: "sort", key: SORT_BY_KEY[e.key] });
  }
}, true);

/* ---------------------------------------------------------------- wiring */

$("filterInput").addEventListener("focus", () => { state.typing = true; });
$("filterInput").addEventListener("blur", () => { state.typing = false; });
$("filterInput").addEventListener("input", (e) => send({ cmd: "filter", q: e.target.value }));
$("focusBtn").onclick = focusAgent;
$("stopBtn").onclick = stopAgent;
$("copycwd").onclick = () => {
  const a = selected();
  if (!a) return;
  navigator.clipboard.writeText(a.cwd).then(
    () => notice("copied " + a.cwdShort),
    () => notice("could not copy", true));
};

// Go calls these. __snapshot carries a whole frame; a frame that arrives out
// of order is dropped rather than rendered, since a stale one would move the
// cursor back to where it used to be.
window.__snapshot = (json) => {
  const s = JSON.parse(json);
  if (s.generation < state.generation) return;
  state.generation = s.generation;
  state.snap = s;
  if (!state.cursor && s.agents.length) {
    state.cursor = { pid: s.agents[0].pid, sessionId: s.agents[0].sessionId };
  }
  render();
};
window.__notice = (text, alert) => notice(text, alert);

window.addEventListener("resize", () => {
  // A different width can mean a different readout shape, so the
  // measurement has to be taken again rather than carried across.
  state.readoutH = 0;
  reportCapacity();
});

// Announce the page to whatever is hosting it. Nothing else tells the window
// that a frame would land anywhere, so until this arrives it holds them back
// - and a page that never announced would sit empty forever.
//
// It repeats until the first frame answers. The bridge may not be bound at
// the moment this file runs, and there is no event that says when it is.
function announce() {
  if (state.snap) return;
  send({ cmd: "hello" });
  setTimeout(announce, 200);
}
announce();

// The clock in "polled Ns ago" has to keep moving between polls.
setInterval(() => { if (state.snap) renderSidebar(state.snap); }, 1000);
