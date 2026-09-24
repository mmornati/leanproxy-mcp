// Mirrors src/metrics.ts's types (this file is compiled as a standalone
// webview script, without module imports).
interface UsageTool {
  server: string;
  tool: string;
  calls: number;
  original_tokens: number;
  returned_tokens: number;
  saved_tokens: number;
}

interface UsageServer {
  server: string;
  tools: number;
  calls: number;
  original_tokens: number;
  saved_tokens: number;
}

interface UsageWindow {
  sessions: number;
  original_tokens: number;
  saved_tokens: number;
  saved_percent: number;
  discovery_tokens: number;
  by_server: UsageServer[];
  by_tool: UsageTool[];
}

interface MetricsResponse {
  usage?: { estimator: string; today: UsageWindow; week: UsageWindow };
}

interface Display {
  currencySymbol: string;
  tokenCostPer1000: number;
}

declare function acquireVsCodeApi(): {
  postMessage(message: unknown): void;
  getState(): unknown;
  setState(state: unknown): void;
};

const vscode = acquireVsCodeApi();

window.addEventListener('message', (event: MessageEvent) => {
  const message = event.data;
  if (message.type === 'metrics') {
    renderMetrics(message.payload as MetricsResponse, message.display as Display);
  } else if (message.type === 'error') {
    renderError(message.payload as string);
  }
});

function renderMetrics(data: MetricsResponse, display: Display): void {
  const app = document.getElementById('app');
  if (!app) return;

  app.innerHTML = '';

  const h1 = document.createElement('h1');
  h1.textContent = 'LeanProxy Token Usage';
  app.appendChild(h1);

  const usage = data.usage;
  if (!usage) {
    app.appendChild(emptyState('No usage data: the proxy could not read its usage store (~/.leanproxy/usage).'));
    return;
  }

  app.appendChild(windowCard('Today', usage.today, display));
  app.appendChild(windowCard('This week', usage.week, display));

  const note = document.createElement('p');
  note.className = 'note';
  note.textContent = `Tokens measured by the proxy (${usage.estimator}). Today is since 00:00 UTC; the week starts Monday 00:00 UTC.`;
  app.appendChild(note);

  app.appendChild(sectionTitle('By server (today)'));
  const serverCard = document.createElement('div');
  serverCard.className = 'metric-card';
  if (usage.today.by_server.length === 0) {
    serverCard.appendChild(emptyState('No tool results today. Per-server and per-tool figures need the response governor (response.enabled: true).'));
  } else {
    serverCard.appendChild(buildTable(
      ['Server', 'Calls', 'Response tokens', 'Saved'],
      usage.today.by_server.map((s) => [s.server || '(no server)', num(s.calls), num(s.original_tokens), num(s.saved_tokens)])
    ));
  }
  app.appendChild(serverCard);

  if (usage.today.by_tool.length > 0) {
    app.appendChild(sectionTitle('Top tools by response size (today)'));
    const toolCard = document.createElement('div');
    toolCard.className = 'metric-card';
    toolCard.appendChild(buildTable(
      ['Tool', 'Calls', 'Response tokens', 'Saved'],
      usage.today.by_tool.slice(0, 10).map((t) => [t.server ? `${t.server}.${t.tool}` : t.tool, num(t.calls), num(t.original_tokens), num(t.saved_tokens)])
    ));
    app.appendChild(toolCard);
  }
}

function windowCard(label: string, w: UsageWindow, display: Display): HTMLElement {
  const card = document.createElement('div');
  card.className = 'metric-card';
  card.appendChild(row(`${label}: tokens saved`, num(w.saved_tokens), 'value total'));
  card.appendChild(row('Of original tokens', `${num(w.original_tokens)} (${w.saved_percent.toFixed(1)}%)`));
  if (display.tokenCostPer1000 > 0) {
    const cost = (w.saved_tokens / 1000) * display.tokenCostPer1000;
    card.appendChild(row('Estimated cost saved (your price)', `${display.currencySymbol}${cost.toFixed(4)}`));
  }
  card.appendChild(row('Discovery cost (tokens)', num(w.discovery_tokens)));
  card.appendChild(row('Sessions', num(w.sessions)));
  return card;
}

function row(label: string, value: string, valueClass = 'value'): HTMLElement {
  const r = document.createElement('div');
  r.className = 'metric-row';
  const l = document.createElement('span');
  l.className = 'label';
  l.textContent = label;
  const v = document.createElement('span');
  v.className = valueClass;
  v.textContent = value;
  r.appendChild(l);
  r.appendChild(v);
  return r;
}

function sectionTitle(text: string): HTMLElement {
  const title = document.createElement('div');
  title.className = 'section-title';
  title.textContent = text;
  return title;
}

function emptyState(text: string): HTMLElement {
  const empty = document.createElement('div');
  empty.className = 'empty-state';
  empty.textContent = text;
  return empty;
}

function num(n: number): string {
  return n.toLocaleString();
}

function renderError(message: string): void {
  const app = document.getElementById('app');
  if (!app) return;

  app.innerHTML = '';

  const errorDiv = document.createElement('div');
  errorDiv.className = 'error-state';

  const h2 = document.createElement('h2');
  h2.textContent = 'Proxy Offline';
  errorDiv.appendChild(h2);

  const p = document.createElement('p');
  p.textContent = message;
  errorDiv.appendChild(p);

  const p2 = document.createElement('p');
  p2.style.cssText = 'font-size:0.85em;color:var(--vscode-descriptionForeground);';
  p2.textContent = 'Start LeanProxy with --metrics-bind 127.0.0.1:9091 and check leanproxy.metricsEndpoint.';
  errorDiv.appendChild(p2);

  app.appendChild(errorDiv);
}

function buildTable(headers: string[], rows: string[][]): HTMLTableElement {
  const table = document.createElement('table');
  table.className = 'table';

  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const h of headers) {
    const th = document.createElement('th');
    th.textContent = h;
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const row of rows) {
    const tr = document.createElement('tr');
    for (const cell of row) {
      const td = document.createElement('td');
      td.textContent = cell;
      tr.appendChild(td);
    }
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);

  return table;
}
