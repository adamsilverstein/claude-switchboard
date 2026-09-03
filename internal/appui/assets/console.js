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

// The bands run in the order Go sorted the rows - statusRank in
// internal/ui/model.go, checked by a test that reads this line. The cursor
// walks the list in Go's order, so a band drawn out of that order sends the
// selection jumping up the screen past rows it never landed on.
const STATUS_ORDER = ["waiting", "idle", "busy", "shell"];
const ENDED = "dead";
const BAND_LABEL = { waiting: "Needs you", busy: "Working", idle: "Idle", shell: "Shell", dead: "Ended" };
const QUEUE = ["waiting", "idle", "busy", "dead"];
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
  collapsed: new Set([ENDED]),
  capacity: 0,
  readoutH: 0,      // measured; the stylesheet's estimate until then
  typing: false,
  confirmingStop: null,
  noticeTimer: null,
  columns: {},      // column name -> pixel width you dragged it to
  dragging: null,   // the column being dragged, or null
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

// visibleRows is what the cursor can land on, in the order the screen shows
// it. It walks the same bands renderList draws, so the arrow keys move down
// the list the way the eye reads it whatever the sort is and whatever is
// grouped - the order is decided once, in bands, rather than agreed on
// twice. Rows inside a collapsed group are still listed and still sorted,
// but skipping them is what makes a collapse worth having.
function visibleRows() {
  const rows = agents();
  if (!state.snap || !state.snap.grouped) return rows;
  return bands(rows)
    .filter(([status]) => !state.collapsed.has(status))
    .flatMap(([, group]) => group);
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
//
// offsetTop is only the distance into the list because the stylesheet gives
// #list a position; a test checks that it still does.
function scrollCursorIntoView() {
  const row = $("list").querySelector('[aria-selected="true"]');
  if (!row) return;
  const box = $("list");
  const top = row.offsetTop;
  const bottom = top + row.offsetHeight;
  if (top < box.scrollTop) box.scrollTop = top;
  else if (bottom > box.scrollTop + box.clientHeight) box.scrollTop = bottom - box.clientHeight;
}

/* --------------------------------------------------------------- columns */

/* Dragging the divider between two headers moves the boundary between their
 * columns. The width goes on the root element as a custom property, which is
 * where the header and every row already read their track sizes from - so
 * one assignment moves the whole table and a drag costs no render at all.
 *
 * The columns themselves are not listed here. They are the [data-col] boxes
 * in console.html, in track order, which keeps the markup the one place that
 * says what the table's columns are.
 */

// Narrower than this and the header label has nothing left to show.
const MIN_COL = 44;
// What a flexible column keeps when a fixed one grows into it. Higher than
// MIN_COL because the flexible columns are the two that carry a sentence:
// a NAME column too narrow to show a word has stopped being a column.
const FLEX_FLOOR = 120;

function columnBoxes() {
  return [...$("head").querySelectorAll("[data-col]")];
}

// A column is flexible while nothing has pinned it to a pixel width: the
// stylesheet sizes NAME and SUMMARY in fr, and a drag replaces that with px.
// A column the layout has collapsed reads as 0px, which is not flexible and
// has nothing to give.
function isFlexible(name) {
  const w = getComputedStyle(document.body).getPropertyValue("--col-" + name).trim();
  return w !== "" && !w.endsWith("px");
}

// A grid track sized in fr gives up its width to one sized in pixels, so a
// column can grow exactly as far as the flexible ones have to spare. Past
// that the grid overflows its frame and the table starts scrolling sideways,
// which it never should. What is left to take is measured at the moment the
// drag starts rather than assumed.
function flexSlack(dragged) {
  let slack = 0;
  for (const box of columnBoxes()) {
    const name = box.dataset.col;
    if (name === dragged || !isFlexible(name)) continue;
    slack += Math.max(0, box.offsetWidth - FLEX_FLOOR);
  }
  return slack;
}

// The width you chose and the width on screen are kept apart, because they
// can differ: a window too narrow to show the table you shaped is trimmed to
// fit, and that trim must not overwrite what you asked for. state.columns is
// what you dragged and what Go is told; showColumn is only what is drawn.
//
// A width of zero clears the override and hands the column back to the
// stylesheet, which is the only one of the two that answers to how wide the
// window is.
function showColumn(name, px) {
  const root = document.documentElement.style;
  if (px > 0) root.setProperty("--col-" + name, px + "px");
  else root.removeProperty("--col-" + name);
}

// setColumn records a width and redraws. Zero forgets it.
function setColumn(name, px) {
  if (px > 0) state.columns[name] = px;
  else delete state.columns[name];
  paintColumns();
}

function paintColumns() {
  for (const box of columnBoxes()) showColumn(box.dataset.col, state.columns[box.dataset.col] || 0);
  trimToFit();
}

// A drag can never overflow the table - it only takes what the flexible
// columns have to give - but the width it produced was measured against the
// screen it was made on. Reopened on a laptop, or typed into app.json by
// hand, it can be wider than there is room for. So what does not fit is
// taken off the widest columns first, down to the floor: the wide ones are
// the ones with room to give.
//
// Only what is drawn is trimmed. Widen the window again and the table comes
// back to the shape you gave it.
//
// The rounds are because the pixels a fixed column gives up are shared out
// between the flexible ones by their fr ratio, so handing back exactly what
// was missing can still leave one of them under the floor. Three passes
// settles it, and the loop stops the moment nothing is short.
function trimToFit() {
  for (let round = 0; round < 3; round++) {
    let over = shortfall();
    if (over <= 1) return;
    for (const [name, px] of Object.entries(state.columns).sort((a, b) => b[1] - a[1])) {
      if (over <= 0) break;
      const drawn = drawnWidth(name);
      const take = Math.min(over, drawn - MIN_COL);
      if (take <= 0) continue;
      showColumn(name, drawn - take);
      over -= take;
    }
  }
}

// How much the table is short: what the flexible columns are missing to
// reach their floor, plus anything spilling past the frame.
//
// The floor is the part that matters. A grid track sized in fr shrinks all
// the way to nothing before the row overflows anything, so a test that
// waited for an overflow would wait until NAME had already disappeared.
function shortfall() {
  let short = 0;
  for (const box of columnBoxes()) {
    if (isFlexible(box.dataset.col)) short += Math.max(0, FLEX_FLOOR - box.offsetWidth);
  }
  const head = $("head");
  return short + Math.max(0, head.scrollWidth - head.clientWidth);
}

function drawnWidth(name) {
  const box = $("head").querySelector('[data-col="' + name + '"]');
  return box ? box.offsetWidth : 0;
}

// Apply what a frame carries. Skipped mid-drag: a poll landing during the
// gesture would snap the column back to where it was before you grabbed it.
function applyColumns(widths) {
  if (state.dragging) return;
  const w = widths || {};
  state.columns = {};
  for (const box of columnBoxes()) {
    if (w[box.dataset.col] > 0) state.columns[box.dataset.col] = w[box.dataset.col];
  }
  paintColumns();
}

// Grips hang off the trailing edge of every header but the last, which has
// no boundary of its own - it is the column the others resize into.
function initColumns() {
  const boxes = columnBoxes();
  for (const box of boxes.slice(0, -1)) {
    const name = box.dataset.col;
    const grip = el("i", "grip");
    grip.setAttribute("role", "separator");
    grip.setAttribute("aria-orientation", "vertical");
    grip.setAttribute("aria-label", "Resize the " + name + " column");
    grip.title = "Drag to resize · double-click to reset";
    grip.addEventListener("pointerdown", (e) => startResize(e, box, grip));
    grip.addEventListener("dblclick", () => { setColumn(name, 0); sendColumns(); });
    box.append(grip);
  }
}

function startResize(e, box, grip) {
  if (e.button !== 0) return;
  // Without this the drag turns into a text selection across the header.
  e.preventDefault();
  const name = box.dataset.col;
  const startX = e.clientX;
  const startW = box.offsetWidth;
  const maxW = startW + flexSlack(name);

  const move = (ev) => {
    setColumn(name, Math.round(Math.min(Math.max(startW + ev.clientX - startX, MIN_COL), maxW)));
  };
  const done = () => {
    grip.removeEventListener("pointermove", move);
    grip.removeEventListener("pointerup", done);
    grip.removeEventListener("pointercancel", done);
    grip.classList.remove("dragging");
    document.body.classList.remove("resizing");
    state.dragging = null;
    // What you can see is what you chose. The last few pixels of a drag can
    // be trimmed back by the flexible columns' floor, and remembering the
    // width the pointer asked for rather than the one on screen would put a
    // number in app.json that this window never draws.
    const drawn = drawnWidth(name);
    if (drawn > 0 && drawn < state.columns[name]) state.columns[name] = drawn;
    sendColumns();
  };

  // Capture, so a pointer that outruns the nine pixel grip - which it will,
  // the moment you move faster than the layout - keeps driving the drag.
  grip.setPointerCapture(e.pointerId);
  grip.classList.add("dragging");
  document.body.classList.add("resizing");
  state.dragging = name;
  grip.addEventListener("pointermove", move);
  grip.addEventListener("pointerup", done);
  grip.addEventListener("pointercancel", done);
}

// Sent once the gesture ends rather than on every move: this is what gets
// written to disk, and a drag is a few hundred frames.
function sendColumns() {
  send({ cmd: "columns", widths: state.columns });
}

/* ------------------------------------------------------------- rendering */

function render() {
  const s = state.snap;
  if (!s) return;
  document.body.classList.toggle("compact", s.compact);
  // A column no agent on this machine can fill leaves the table. Without
  // the statusline shim that is the whole context column, and without gh -
  // or without a branch that has a pull request - it is the ref column too.
  //
  // Go counts that over every agent, not the ones the filter left: a column
  // that vanished because your query happened to match only the agents
  // without one would reflow the table under you as you typed.
  document.body.classList.toggle("no-context", !s.anyContext);
  document.body.classList.toggle("no-ref", !s.anyRef);
  applyColumns(s.columns);
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

  facets("repos", "repolist", s.agents, (a) => a.repo, (a) => a.branch || "");
  facets("models", "modellist", s.agents, (a) => a.model, () => "");

  meter("usage7d", s.account.usage7dPct, s.account.usage7dResetsIn ? "resets in " + s.account.usage7dResetsIn : "");

  const age = s.polledAt ? Math.max(0, Math.round((Date.now() - Date.parse(s.polledAt)) / 1000)) : null;
  $("polled").textContent = age === null ? "" : "polled " + age + "s ago";
}

// facets builds a sidebar filter list, and hides the whole block when the
// field it groups by has no source on this machine.
//
// The note beside a group is only shown when the whole group agrees on it.
// Three agents in one repository on three branches have no one branch, and
// naming the first one's asserts something about the other two that is not
// true. The dirty marker is left off for the same reason and stays on the
// rows, where it says which checkout it means.
function facets(blockId, listId, rows, keyOf, noteOf) {
  const groups = new Map();
  for (const a of rows) {
    const k = keyOf(a);
    if (!k) continue;
    if (!groups.has(k)) groups.set(k, { n: 0, note: noteOf(a) });
    const g = groups.get(k);
    g.n++;
    if (g.note !== noteOf(a)) g.note = "";
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
    b.title = g.note ? k + " " + g.note : k;
    b.append(el("b", null, k));
    if (g.note) b.append(el("i", null, g.note));
    b.append(el("span", null, String(g.n)));
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
  for (const [status, group] of bands(rows)) {
    list.append(bandEl(status, group.length));
    if (state.collapsed.has(status)) continue;
    for (const a of group) list.append(rowEl(a, cur, s));
  }
}

// bands splits the rows into the blocks the list draws, in the order Go put
// them in. It is the only place that decides an order, because the screen
// and the keyboard reading two of them is exactly how they came apart.
//
// A status the design did not name still gets a band; an unrecognised status
// must never make an agent invisible, and Go ranks it behind every named
// live status and ahead of the ended rows.
function bands(rows) {
  const out = [];
  const band = (status, group) => { if (group.length) out.push([status, group]); };
  for (const status of STATUS_ORDER) band(status, rows.filter((a) => a.status === status));
  band("", rows.filter((a) => a.status !== ENDED && !STATUS_ORDER.includes(a.status)));
  band(ENDED, rows.filter((a) => a.status === ENDED));
  return out;
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
  if (status === ENDED) {
    b.append(el("span", "hint", state.collapsed.has(ENDED)
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

  r.append(ctxCell(a));
  r.append(line("repo ellipsis", repoLabel(a) || "—"));
  r.append(refCell(a));
  r.append(line("summary ellipsis", a.summary || ""));

  r.onclick = () => { state.cursor = { pid: a.pid, sessionId: a.sessionId }; render(); };
  r.ondblclick = focusAgent;
  return r;
}

// The ref column is the piece of work an agent is on, not the machinery it
// is on it with: the number identifies it, and the state is what says
// whether it is still in front of you or already behind you.
//
// The number is a button because a pull request you can see but not open is
// half an answer. It stops the click from reaching the row so that opening
// a pull request does not also move the cursor.
function refCell(a) {
  const c = el("span", "ref");
  if (!a.ref) {
    c.append(line("num none", "—"));
    return c;
  }
  const n = el("button", "num", a.ref);
  n.type = "button";
  n.title = refTitle(a);
  if (a.refUrl) {
    n.onclick = (e) => { e.stopPropagation(); send({ cmd: "open", url: a.refUrl }); };
  } else {
    n.disabled = true;
  }
  c.append(n);
  if (a.refState) c.append(el("i", "state s-" + a.refState, a.refState));
  return c;
}

function refTitle(a) {
  const what = (a.refKind === "issue" ? "issue " : "pull request ") + a.ref;
  return [what, a.refState, a.refTitle].filter(Boolean).join(" · ");
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
    a.elapsed, repoLabel(a), a.ref ? a.ref + " " + a.refTitle : "", a.tty,
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
  if (a.ref) {
    const l = line("line", "");
    const n = el("button", "num", a.ref);
    n.type = "button";
    n.title = refTitle(a);
    if (a.refUrl) n.onclick = () => send({ cmd: "open", url: a.refUrl });
    else n.disabled = true;
    l.append(n);
    if (a.refTitle) l.append(line("dim", " " + a.refTitle));
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
  if (state.collapsed.has(ENDED)) state.collapsed.delete(ENDED);
  else state.collapsed.add(ENDED);
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

initColumns();

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
  // Reconcile the cursor with the frame that just arrived. It can be
  // holding an agent that has since exited, or one the filter no longer
  // shows, and selected() already falls back to the top of the list - but
  // if the two disagree, the row drawn as selected is not the row the
  // arrow keys move from, and the first keypress goes nowhere.
  //
  // A frame with no rows at all is left alone: a filter that matches
  // nothing for a keystroke should not cost you your place.
  const cur = selected();
  if (cur) state.cursor = { pid: cur.pid, sessionId: cur.sessionId };
  render();
};
window.__notice = (text, alert) => notice(text, alert);

window.addEventListener("resize", () => {
  // A different width can mean a different readout shape, so the
  // measurement has to be taken again rather than carried across.
  state.readoutH = 0;
  reportCapacity();
  // And a different width means a different trim: what did not fit a moment
  // ago may fit now, and the widths you chose are still on record.
  paintColumns();
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
