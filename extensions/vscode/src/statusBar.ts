import * as vscode from 'vscode';
import { estimatedCost, fetchMetrics, formatTokens, MetricsError } from './metrics';
import { affectsLeanProxy, readSettings, readToken } from './settings';

export class StatusBarManager implements vscode.Disposable {
  private statusBarItem: vscode.StatusBarItem;
  private pollTimer: ReturnType<typeof setInterval> | undefined;
  private disposables: vscode.Disposable[] = [];

  constructor(private readonly secrets: vscode.SecretStorage) {
    this.statusBarItem = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100
    );
    this.statusBarItem.command = 'leanproxy.openCostPanel';
    this.statusBarItem.tooltip = 'LeanProxy token savings — Click for details';
    this.statusBarItem.text = '$(sync~spin) LeanProxy...';
    this.statusBarItem.show();

    // Pick up a new endpoint, interval or price without a reload.
    this.disposables.push(
      vscode.workspace.onDidChangeConfiguration((e) => {
        if (affectsLeanProxy(e)) {
          this.start();
        }
      })
    );
  }

  start(): void {
    if (this.pollTimer) clearInterval(this.pollTimer);
    this.poll();
    this.pollTimer = setInterval(() => this.poll(), readSettings().pollIntervalMs);
  }

  refresh(): void {
    this.poll();
  }

  dispose(): void {
    if (this.pollTimer) clearInterval(this.pollTimer);
    this.disposables.forEach((d) => d.dispose());
    this.statusBarItem.dispose();
  }

  private async poll(): Promise<void> {
    const settings = readSettings();
    try {
      const data = await fetchMetrics(settings.endpoint, await readToken(this.secrets));
      const usage = data.usage;
      if (!usage) {
        this.statusBarItem.text = '$(info) LeanProxy';
        this.statusBarItem.tooltip = 'LeanProxy is running but has no usage data (its usage store could not be read)';
        return;
      }
      const { today, week } = usage;
      const cost = estimatedCost(today.saved_tokens, settings.tokenCostPer1000);
      this.statusBarItem.text =
        cost === undefined
          ? `$(graph) ${formatTokens(today.saved_tokens)} saved`
          : `$(coin) ${settings.currencySymbol}${cost.toFixed(4)} saved`;
      this.statusBarItem.tooltip =
        `LeanProxy: ${today.saved_tokens.toLocaleString()} tokens saved today (${today.saved_percent.toFixed(1)}%), ` +
        `${week.saved_tokens.toLocaleString()} this week — Click for details`;
    } catch (err) {
      const status = err instanceof MetricsError ? err.status : undefined;
      this.statusBarItem.text = status === 401 ? '$(lock) LeanProxy' : '$(circle-slash) LeanProxy';
      this.statusBarItem.tooltip = `LeanProxy disconnected — ${err instanceof Error ? err.message : String(err)}`;
    }
  }
}
