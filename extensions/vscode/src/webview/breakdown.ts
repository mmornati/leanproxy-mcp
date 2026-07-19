interface ToolMetric {
  tool_name: string;
  token_count: number;
}

interface ServerMetric {
  server_name: string;
  token_count: number;
}

interface MetricsSnapshot {
  by_tool: ToolMetric[];
  by_server: ServerMetric[];
  total_spend: number;
  top_5_expensive_tools: ToolMetric[];
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
    renderMetrics(message.payload as MetricsSnapshot);
  } else if (message.type === 'error') {
    renderError(message.payload as string);
  }
});

function renderMetrics(data: MetricsSnapshot): void {
  const app = document.getElementById('app');
  if (!app) return;

  app.innerHTML = '';

  const h1 = document.createElement('h1');
  h1.textContent = 'LeanProxy Cost Breakdown';
  app.appendChild(h1);

  const totalCard = document.createElement('div');
  totalCard.className = 'metric-card';
  const totalRow = document.createElement('div');
  totalRow.className = 'metric-row';
  const totalLabel = document.createElement('span');
  totalLabel.className = 'label';
  totalLabel.textContent = 'Total Spend (tokens)';
  const totalValue = document.createElement('span');
  totalValue.className = 'value total';
  totalValue.textContent = data.total_spend.toLocaleString();
  totalRow.appendChild(totalLabel);
  totalRow.appendChild(totalValue);
  totalCard.appendChild(totalRow);
  app.appendChild(totalCard);

  const serverTitle = document.createElement('div');
  serverTitle.className = 'section-title';
  serverTitle.textContent = 'By Server';
  app.appendChild(serverTitle);

  const serverCard = document.createElement('div');
  serverCard.className = 'metric-card';
  if (data.by_server.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No server data';
    serverCard.appendChild(empty);
  } else {
    serverCard.appendChild(buildTable(['Server', 'Tokens'], data.by_server.map((s) => [s.server_name, s.token_count.toLocaleString()])));
  }
  app.appendChild(serverCard);

  const toolTitle = document.createElement('div');
  toolTitle.className = 'section-title';
  toolTitle.textContent = 'By Tool';
  app.appendChild(toolTitle);

  const toolCard = document.createElement('div');
  toolCard.className = 'metric-card';
  if (data.by_tool.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No tool data';
    toolCard.appendChild(empty);
  } else {
    toolCard.appendChild(buildTable(['Tool', 'Tokens'], data.by_tool.map((t) => [t.tool_name, t.token_count.toLocaleString()])));
  }
  app.appendChild(toolCard);

  if (data.top_5_expensive_tools.length > 0) {
    const topTitle = document.createElement('div');
    topTitle.className = 'section-title';
    topTitle.textContent = 'Top 5 Most Expensive Tools';
    app.appendChild(topTitle);

    const topCard = document.createElement('div');
    topCard.className = 'metric-card';
    topCard.appendChild(buildTable(['Tool', 'Tokens'], data.top_5_expensive_tools.map((t) => [t.tool_name, t.token_count.toLocaleString()])));
    app.appendChild(topCard);
  }
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
  p2.textContent = 'Ensure LeanProxy is running and the metrics endpoint is accessible.';
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
