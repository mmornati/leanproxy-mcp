import * as vscode from 'vscode';
import { StatusBarManager } from './statusBar';

let statusBarManager: StatusBarManager | undefined;

export function activate(context: vscode.ExtensionContext) {
  statusBarManager = new StatusBarManager();
  statusBarManager.start();

  context.subscriptions.push(
    vscode.commands.registerCommand('leanproxy.openCostPanel', () => {
      openCostPanel(context);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('leanproxy.refreshStatusBar', () => {
      statusBarManager?.refresh();
    })
  );

  context.subscriptions.push(statusBarManager);
}

export function deactivate() {
  statusBarManager?.dispose();
  statusBarManager = undefined;
}

function openCostPanel(context: vscode.ExtensionContext) {
  const panel = vscode.window.createWebviewPanel(
    'leanproxyCostPanel',
    'LeanProxy Cost Breakdown',
    vscode.ViewColumn.Beside,
    {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(context.extensionUri, 'out', 'webview')],
    }
  );

  const config = vscode.workspace.getConfiguration('leanproxy');
  const endpoint = config.get<string>('metricsEndpoint', 'http://127.0.0.1:9090/metrics');

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  function sendMetrics() {
    fetch(endpoint)
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((data) => {
        panel.webview.postMessage({ type: 'metrics', payload: data });
      })
      .catch(() => {
        panel.webview.postMessage({ type: 'error', payload: 'proxy offline' });
      });
  }

  const htmlPath = vscode.Uri.joinPath(context.extensionUri, 'out', 'webview', 'index.html');
  panel.webview.html = getWebviewContent(panel.webview, htmlPath);

  panel.onDidChangeViewState((e) => {
    if (e.webviewPanel.visible) {
      sendMetrics();
    }
  });

  panel.onDidDispose(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  sendMetrics();
  pollTimer = setInterval(sendMetrics, 1000);
}

function getWebviewContent(webview: vscode.Webview, htmlPath: vscode.Uri): string {
  const scriptUri = webview.asWebviewUri(
    vscode.Uri.joinPath(htmlPath, '..', 'breakdown.js')
  );
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>LeanProxy Cost Breakdown</title>
  <style>
    body { font-family: var(--vscode-font-family); padding: 16px; color: var(--vscode-foreground); }
    h1 { font-size: 1.2em; margin: 0 0 12px; font-weight: 600; }
    .metric-card { background: var(--vscode-editor-background); border: 1px solid var(--vscode-widget-border); border-radius: 6px; padding: 12px; margin-bottom: 12px; }
    .metric-row { display: flex; justify-content: space-between; align-items: center; padding: 4px 0; }
    .metric-row + .metric-row { border-top: 1px solid var(--vscode-widget-border); margin-top: 4px; padding-top: 8px; }
    .label { color: var(--vscode-descriptionForeground); font-size: 0.85em; }
    .value { font-weight: 600; font-size: 0.95em; }
    .total { font-size: 1.4em; font-weight: 700; color: var(--vscode-editorWarning-foreground); }
    .section-title { font-size: 0.95em; font-weight: 600; margin: 16px 0 8px; }
    .empty-state { text-align: center; padding: 40px 16px; color: var(--vscode-descriptionForeground); }
    .empty-state h2 { font-size: 1.1em; margin: 0 0 8px; }
    .error-state { text-align: center; padding: 40px 16px; color: var(--vscode-errorForeground); }
    .table { width: 100%; border-collapse: collapse; }
    .table th { text-align: left; font-size: 0.8em; color: var(--vscode-descriptionForeground); padding: 4px 8px; border-bottom: 1px solid var(--vscode-widget-border); }
    .table td { padding: 6px 8px; border-bottom: 1px solid var(--vscode-widget-border); font-size: 0.9em; }
  </style>
</head>
<body>
  <div id="app">
    <div class="empty-state" id="loading">
      <h2>Connecting to LeanProxy...</h2>
    </div>
  </div>
  <script src="${scriptUri}"></script>
</body>
</html>`;
}
