import * as vscode from 'vscode';

interface MetricsSnapshot {
  by_tool: ToolMetric[];
  by_server: ServerMetric[];
  total_spend: number;
  top_5_expensive_tools: ToolMetric[];
}

interface ToolMetric {
  tool_name: string;
  token_count: number;
}

interface ServerMetric {
  server_name: string;
  token_count: number;
}

export class StatusBarManager implements vscode.Disposable {
  private statusBarItem: vscode.StatusBarItem;
  private pollTimer: ReturnType<typeof setInterval> | undefined;
  private lastTotal: number = 0;
  private connected: boolean = false;

  constructor() {
    this.statusBarItem = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100
    );
    this.statusBarItem.command = 'leanproxy.openCostPanel';
    this.statusBarItem.tooltip = 'LeanProxy AI Cost — Click for details';
    this.statusBarItem.text = '$(sync~spin) LeanProxy...';
    this.statusBarItem.show();
  }

  start(): void {
    this.poll();
    const config = vscode.workspace.getConfiguration('leanproxy');
    const interval = config.get<number>('pollInterval', 1000);
    this.pollTimer = setInterval(() => this.poll(), interval);
  }

  refresh(): void {
    this.poll();
  }

  dispose(): void {
    if (this.pollTimer) clearInterval(this.pollTimer);
    this.statusBarItem.dispose();
  }

  private async poll(): Promise<void> {
    const config = vscode.workspace.getConfiguration('leanproxy');
    const endpoint = config.get<string>('metricsEndpoint', 'http://127.0.0.1:9090/metrics');

    try {
      const res = await fetch(endpoint);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as MetricsSnapshot;

      this.connected = true;
      this.lastTotal = data.total_spend;
      this.updateStatusBar(data.total_spend);
      this.statusBarItem.tooltip = 'LeanProxy AI Cost — Click for details';
    } catch {
      this.connected = false;
      this.statusBarItem.text = '$(circle-slash) LeanProxy';
      this.statusBarItem.tooltip = 'LeanProxy disconnected — proxy offline';
    }
  }

  private updateStatusBar(totalSpend: number): void {
    const config = vscode.workspace.getConfiguration('leanproxy');
    const symbol = config.get<string>('currencySymbol', '$');
    const costPer1000 = config.get<number>('tokenCostPer1000', 0.002);
    const estimatedCost = (totalSpend / 1000) * costPer1000;

    if (!isFinite(estimatedCost)) {
      this.statusBarItem.text = `$(coin) N/A`;
    } else {
      this.statusBarItem.text = `$(coin) ${symbol}${estimatedCost.toFixed(4)}`;
    }
  }
}
