/* "One shared gateway": the LeanProxy daemon and its admin dashboard.
 *
 * Scenes of a canvas video (see ../engine/engine.js). Each scene is
 * fn(t, d, cues), a pure function of t; animations hook onto the spoken
 * sentences with at(cues, i). The dashboard is redrawn schematically with
 * fictional demo data (the same mock servers as docs/assets/screenshots).
 */
"use strict";

const STR = {
  en: {
    k1: "THE PROBLEM", h1: "One proxy per session",
    session: "session", proxy: "proxy",
    procs: "server processes", proxies: "proxies", none: "no shared view",
    cond: "10 sessions x 5 servers, one copy each",
    k2: "NEW", h2: "One shared gateway",
    svc: "launchd on macOS, systemd --user on Linux",
    k3: "CLIENTS", h3: "Connect by URL",
    gw: "LeanProxy gateway", one: "one process",
    k4: "ADMIN DASHBOARD", h4: "Every server, one line each",
    servers: "Servers", tools: "TOOLS", calls: "CALLS", errors: "ERRORS", avg: "AVG",
    up: "Up", refused: "Authentication refused: check this server's token", refusedShort: "Authentication refused",
    reconnect: "Reconnect", disable: "Disable", remove: "Remove",
    demo: "demo data",
    k5: "ACTIVITY", h5: "Who called what",
    clients: "Clients", recent: "Recent tool calls",
    never: "arguments and results are never recorded",
    k6: "HOT RELOAD", h6: "Add a server, no restart",
    starting: "Starting, handshake not finished", norestart: "picked up in seconds",
    k7: "TRY IT", c1: "One process.", c2: "One set of servers.", c3: "One place to look.",
    docs: "Docs: Shared Gateway and Admin Dashboard",
  },
};

/* Scanner palette of the docs site, night variant. */
const TEAL = "#4fd0a8", AMBER = "#f6ab5e", RED = "#ff6b76", COBALT = "#8ea0ff";

const SERVERS = [
  { name: "github", tools: 42, calls: 5, avg: "154 ms" },
  { name: "jira", tools: 24, calls: 3, avg: "234 ms" },
  { name: "notion", tools: null, calls: 2, avg: "1.0 ms", down: true },
  { name: "postgres", tools: 10, calls: 2, avg: "110 ms" },
  { name: "slack", tools: 14, calls: 3, avg: "141 ms" },
];

/* A status dot: filled when up, with a halo when down. */
function statusDot(x, y, color, alpha) {
  dot(x, y, 6, color, alpha);
}

function sProblem(t, d, cues) {
  kicker(S.k1, seg(t, 0.1, 0.6));
  headline(S.h1, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 3.5), a2 = at(cues, 2, 7.7);
  const rows = 10, y0 = 196, rh = 38;
  for (let i = 0; i < rows; i++) {
    const y = y0 + i * rh;
    const k = seg(t, 0.7 + i * 0.16, 1.1 + i * 0.16);
    if (k <= 0) continue;
    panel(80, y, 132, 28, { r: 6, fill: C.panel, alpha: k });
    text(`${S.session} ${i + 1}`, 146, y + 19, { size: 13, font: MONO, color: C.soft, align: "center", alpha: k });
    line([[212, y + 14], [252, y + 14]], C.line, 2, k);
    panel(252, y, 96, 28, { r: 6, fill: C.panel2, stroke: COBALT, alpha: k });
    text(S.proxy, 300, y + 19, { size: 13, font: MONO, color: COBALT, align: "center", alpha: k });
    for (let j = 0; j < 5; j++) {
      const ks = outBack(seg(t, a1 + 0.2 + i * 0.07 + j * 0.05, a1 + 0.6 + i * 0.07 + j * 0.05));
      if (ks <= 0) continue;
      const x = 390 + j * 46;
      line([[348, y + 14], [x, y + 14]], C.line, 1, 0.5 * ks);
      panel(x, y + 2, 30 * ks, 24 * ks, { r: 4, fill: AMBER, stroke: false, alpha: 0.9 * ks });
    }
  }
  /* The count, as the voice says it. */
  const kc = seg(t, a2, a2 + 0.5);
  if (kc > 0) {
    const n = Math.round(50 * outCubic(seg(t, a2 + 0.2, a2 + 2.2)));
    text(String(n), 1180, 330, { size: 132, weight: 800, font: SORA, color: AMBER, align: "right", alpha: kc });
    text(S.procs, 1180, 372, { size: 24, color: C.ink, align: "right", alpha: kc });
    text(`+ 10 ${S.proxies}`, 1180, 410, { size: 20, color: COBALT, align: "right", alpha: seg(t, a2 + 0.8, a2 + 1.3) });
    text(S.cond, 1180, 446, { size: 13, font: MONO, color: C.faint, align: "right", alpha: seg(t, a2 + 0.8, a2 + 1.3) });
    const kn = seg(t, a2 + 2.6, a2 + 3.1);
    pill(S.none, 1180, 500, { align: "right", size: 16, color: RED, stroke: RED, fill: "rgba(255,107,118,0.08)", alpha: kn });
  }
}

function sGateway(t, d, cues) {
  kicker(S.k2, seg(t, 0.1, 0.6));
  headline(S.h2, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 3.8);
  terminal(150, 220, 980, 270, t, [
    { p: "$ ", s: "leanproxy-mcp daemon install", a: a1 + 0.1, b: a1 + 1.3 },
    { p: "", s: "Installed ~/Library/LaunchAgents/io.leanproxy.mcp.plist", a: a1 + 1.7, c: C.soft },
    { p: "", s: "Gateway running: pid 71614, http://127.0.0.1:8765/mcp", a: a1 + 2.2, c: TEAL },
    { p: "", s: "Dashboard: leanproxy-mcp daemon open", a: a1 + 2.7, c: C.soft },
  ], { title: "zsh", size: 19, lh: 34 });
  text(S.svc, 640, 540, { size: 16, font: MONO, color: C.faint, align: "center", alpha: seg(t, a1 + 2.4, a1 + 3) });
}

function sClients(t, d, cues) {
  kicker(S.k3, seg(t, 0.1, 0.6));
  headline(S.h3, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 6.5);
  const clients = [["Claude Code", "passthrough"], ["Codex", "router"], ["Copilot", "passthrough"], ["Cursor", "passthrough"], ["OpenCode", "router"]];
  const gx = 520, gy = 250, gw = 240, gh = 230, cy = gy + gh / 2;
  /* Gateway. */
  const kg = seg(t, 0.4, 1.0);
  panel(gx, gy, gw, gh, { r: 12, fill: C.panel2, stroke: COBALT, lw: 2, alpha: kg });
  text(S.gw, gx + gw / 2, cy - 14, { size: 20, weight: 700, color: C.ink, align: "center", alpha: kg });
  text("127.0.0.1:8765/mcp", gx + gw / 2, cy + 16, { size: 14, font: MONO, color: COBALT, align: "center", alpha: kg });
  text(S.one, gx + gw / 2, cy + 46, { size: 13, font: MONO, color: C.faint, align: "center", alpha: kg });
  /* Clients, named as the voice lists them. */
  clients.forEach(([name, mode], i) => {
    const y = 212 + i * 66;
    const k = seg(t, 1.6 + i * 0.75, 2.1 + i * 0.75);
    if (k <= 0) return;
    const km = seg(t, a1 + 2.0 + i * 0.12, a1 + 2.4 + i * 0.12);
    panel(80, y - 4, 210, 52, { r: 8, fill: C.panel, alpha: k });
    /* The name moves up to make room for the mode, once the voice says it. */
    text(name, 185, y + 28 - 9 * km, { size: 17, weight: 600, color: C.ink, align: "center", alpha: k });
    text(mode, 185, y + 38, { size: 11, font: MONO, color: mode === "router" ? AMBER : TEAL, align: "center", alpha: km });
    line(partial([[290, y + 22], [gx, cy - 80 + i * 40]], k), C.line, 2, 0.9);
    if (k >= 1) packets(290, y + 22, gx, cy - 80 + i * 40, t + i * 0.3, { n: 2, period: 1.6, color: COBALT, r: 3 });
  });
  /* One set of servers. */
  SERVERS.forEach((sv, i) => {
    const y = 212 + i * 66;
    const k = seg(t, a1 + 0.2 + i * 0.12, a1 + 0.7 + i * 0.12);
    if (k <= 0) return;
    line(partial([[gx + gw, cy - 80 + i * 40], [1010, y + 22]], k), C.line, 2, 0.9);
    panel(1010, y, 190, 44, { r: 8, fill: C.panel, stroke: AMBER, alpha: k });
    text(sv.name, 1105, y + 28, { size: 17, font: MONO, color: AMBER, align: "center", alpha: k });
  });
}

/* The Servers panel of the dashboard, drawn schematically. extra is an
 * optional sixth row; open is how far the "down" row is expanded (0..1). */
function serversPanel(x, y, w, kIn, kDown, open, extra) {
  const rows = extra ? SERVERS.concat([extra]) : SERVERS;
  const rh = 50, head = 46, openH = 104 * open;
  const compact = w < 700;
  const h = head + rows.length * rh + openH + 10;
  panel(x, y, w, h, { r: 12, fill: C.panel, alpha: kIn });
  text(S.servers, x + 24, y + 31, { size: 19, weight: 700, color: C.ink, alpha: kIn });
  [[S.tools, 190], [S.calls, 130], [S.avg, 50]].forEach(([label, off]) =>
    text(label, x + w - off, y + 30, { size: 10, font: MONO, color: C.faint, align: "right", alpha: kIn }));
  let ry = y + head;
  rows.forEach((sv, i) => {
    const k = sv.k !== undefined ? sv.k : seg(kIn, 0.1 + i * 0.1, 0.5 + i * 0.1);
    if (k > 0) {
      line([[x + 12, ry], [x + w - 12, ry]], C.line, 1, 0.6 * k);
      const down = sv.down && kDown > 0;
      const color = sv.warn ? AMBER : down ? RED : TEAL;
      statusDot(x + 30, ry + rh / 2, color, k);
      if (down) ring(x + 30, ry + rh / 2, 6 + 6 * kDown, RED, 2, 0.5 * kDown);
      text(sv.name, x + 52, ry + 31, { size: 17, weight: 700, color: C.ink, alpha: k });
      const summary = sv.summary || (down ? (compact ? S.refusedShort : S.refused) : S.up);
      text(summary, x + 190, ry + 31, { size: 14, color: sv.warn ? AMBER : down ? RED : C.soft, alpha: k });
      text(sv.tools === null || sv.tools === undefined ? "-" : String(sv.tools), x + w - 190, ry + 31, { size: 15, font: MONO, color: C.ink, align: "right", alpha: k });
      text(String(sv.calls), x + w - 130, ry + 31, { size: 15, font: MONO, color: C.ink, align: "right", alpha: k });
      text(sv.avg, x + w - 50, ry + 31, { size: 15, font: MONO, color: C.ink, align: "right", alpha: k });
    }
    ry += rh;
    if (sv.down && open > 0) {
      /* The expanded row: last error and the actions. */
      text("transport error: authorization required", x + 52, ry + 20, { size: 13, font: MONO, color: RED, alpha: open });
      [S.reconnect, S.disable, S.remove].forEach((label, j) => {
        const kb = seg(open, 0.3 + j * 0.15, 0.7 + j * 0.15);
        pill(label, x + 52 + j * 132, ry + 46, { size: 14, color: j === 2 ? RED : C.ink, stroke: j === 2 ? RED : C.line, fill: C.panel2, alpha: kb });
      });
      ry += openH;
    }
  });
  text(S.demo, x + w - 16, y + h + 20, { size: 11, font: MONO, color: C.faint, align: "right", alpha: kIn });
}

function sDashboard(t, d, cues) {
  kicker(S.k4, seg(t, 0.1, 0.6));
  headline(S.h4, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 4.4), a2 = at(cues, 2, 8);
  const kIn = seg(t, 0.5, 2.3);
  const kDown = seg(t, a1, a1 + 0.5);
  const open = inOut(seg(t, a2 - 0.2, a2 + 0.8));
  serversPanel(160, 186, 960, kIn, kDown, open);
  if (kDown > 0 && open < 1) highlight(166, 186 + 46 + 2 * 50 + 2, 948, 46, kDown * (1 - open), RED);
}

function sActivity(t, d, cues) {
  kicker(S.k5, seg(t, 0.1, 0.6));
  headline(S.h5, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 6.2);
  /* Clients. */
  const kc = seg(t, 0.5, 1.0);
  panel(80, 190, 420, 250, { r: 12, fill: C.panel, alpha: kc });
  text(S.clients, 104, 221, { size: 18, weight: 700, color: C.ink, alpha: kc });
  [["claude-code", "passthrough", 3], ["codex-mcp-client", "router", 3], ["Visual Studio Code", "passthrough", 3], ["opencode", "router", 2]].forEach(([name, mode, n], i) => {
    const y = 262 + i * 44, k = seg(t, 0.9 + i * 0.2, 1.3 + i * 0.2);
    line([[92, y - 26], [488, y - 26]], C.line, 1, 0.6 * k);
    text(name, 104, y, { size: 15, weight: 600, color: C.ink, alpha: k });
    text(mode, 380, y, { size: 12, font: MONO, color: mode === "router" ? AMBER : TEAL, align: "right", alpha: k });
    text(String(n), 476, y, { size: 15, font: MONO, color: C.soft, align: "right", alpha: k });
  });
  /* Recent tool calls, newest on top as they arrive. */
  const kp = seg(t, 1.6, 2.1);
  panel(530, 190, 670, 360, { r: 12, fill: C.panel, alpha: kp });
  text(S.recent, 554, 221, { size: 18, weight: 700, color: C.ink, alpha: kp });
  const CALLS = [
    ["claude-code", "github", "list_commits", "122 ms", true],
    ["opencode", "github", "merge_pull_request", "238 ms", true],
    ["codex-mcp-client", "slack", "slack_list_channels", "122 ms", true],
    ["claude-code", "postgres", "pg_list_indexes", "43 ms", true],
    ["Visual Studio Code", "github", "delete_everything", "1.3 ms", false],
    ["Visual Studio Code", "jira", "jira_update_issue", "262 ms", true],
  ];
  CALLS.forEach(([client, server, tool, dur, ok], i) => {
    const y = 262 + i * 46, k = seg(t, 2.3 + i * 0.45, 2.7 + i * 0.45);
    if (k <= 0) return;
    line([[542, y - 27], [1188, y - 27]], C.line, 1, 0.6 * k);
    if (!ok) panel(530, y - 26, 4, 44, { r: 0, fill: RED, stroke: false, alpha: k });
    text(client, 554, y, { size: 13, color: C.soft, alpha: k });
    text(server, 716, y, { size: 14, weight: 700, color: C.ink, alpha: k });
    text(tool, 806, y, { size: 13, font: MONO, color: COBALT, alpha: k });
    text(dur, 1090, y, { size: 13, font: MONO, color: C.ink, align: "right", alpha: k });
    pill(ok ? "ok" : "error", 1180, y - 17, { align: "right", size: 11, font: MONO, color: ok ? TEAL : RED, stroke: ok ? TEAL : RED, fill: C.panel, alpha: k });
  });
  const kn = seg(t, a1, a1 + 0.5);
  pill(S.never, 80, 480, { size: 15, color: TEAL, stroke: TEAL, fill: "rgba(79,208,168,0.08)", alpha: kn });
  text(S.demo, 1200, 572, { size: 11, font: MONO, color: C.faint, align: "right", alpha: kp });
}

function sReload(t, d, cues) {
  kicker(S.k6, seg(t, 0.1, 0.6));
  headline(S.h6, seg(t, 0.2, 0.9));
  const a1 = at(cues, 1, 3.6);
  terminal(80, 200, 520, 190, t, [
    { p: "$ ", s: "leanproxy-mcp server add docs \\", a: 0.8, b: 1.9 },
    { p: "    ", s: "--url https://example.com/mcp", a: 1.9, b: 2.8 },
    { p: "", s: 'Server "docs" added successfully', a: 3.1, c: TEAL },
  ], { title: "zsh", size: 15, lh: 28 });
  /* The dashboard on the right: the new row arrives, then turns green. */
  const arrive = seg(t, a1 + 0.2, a1 + 0.8), ready = seg(t, a1 + 1.8, a1 + 2.2);
  const extra = { name: "docs", tools: ready > 0.5 ? 6 : null, calls: 0, avg: "-", k: arrive, warn: ready < 0.5, summary: ready < 0.5 ? S.starting : S.up };
  serversPanel(640, 186, 560, 1, 1, 0, extra);
  if (arrive > 0 && ready < 1) highlight(646, 186 + 46 + 5 * 50 + 2, 548, 46, arrive * (1 - ready), AMBER);
  pill(S.norestart, 80, 430, { size: 16, color: TEAL, stroke: TEAL, fill: "rgba(79,208,168,0.08)", alpha: seg(t, a1 + 2.0, a1 + 2.5) });
}

function sClose(t, d, cues) {
  kicker(S.k7, seg(t, 0.1, 0.6));
  [S.c1, S.c2, S.c3].forEach((line1, i) => {
    const k = seg(t, 0.5 + i * 0.95, 1.0 + i * 0.95);
    text(line1, 80, 190 + i * 74 + (1 - outCubic(k)) * 14, { size: 54, weight: 800, font: SORA, color: i === 2 ? TEAL : C.ink, alpha: k });
  });
  const kt = seg(t, 3.6, 4.1);
  terminal(80, 430, 700, 96, t, [{ p: "$ ", s: "leanproxy-mcp daemon install", a: 3.8, b: 4.8 }], { title: "zsh", size: 20, lh: 34 });
  text(S.docs, 80, 570, { size: 16, color: C.faint, alpha: kt });
}

VIDEO.create({
  title: { en: "LeanProxy: one shared gateway" },
  kicker: { en: "LeanProxy-MCP" },
  langs: ["en"],
  theme: {
    bg: "#0a0f20", bg2: "#0c1226", panel: "#131a31", panel2: "#18213d", line: "#2a3558",
    ink: "#e9edf6", soft: "#b9c1d6", faint: "#8a94b0",
    accent: COBALT, accent2: TEAL, good: TEAL, warn: AMBER, bad: RED,
    term: "#070b18", termInk: "#e9edf6",
  },
  strings: STR,
  poster: "clients",
  scenes: { problem: sProblem, gateway: sGateway, clients: sClients, dashboard: sDashboard, activity: sActivity, reload: sReload, close: sClose },
});
