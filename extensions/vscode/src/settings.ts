import * as vscode from 'vscode';
import { DEFAULT_ENDPOINT, pollInterval } from './metrics';

/** SecretStorage key of the `--metrics-token` value. */
export const TOKEN_SECRET_KEY = 'leanproxy.metricsToken';

export interface Settings {
  endpoint: string;
  pollIntervalMs: number;
  currencySymbol: string;
  tokenCostPer1000: number;
}

export function readSettings(): Settings {
  const config = vscode.workspace.getConfiguration('leanproxy');
  return {
    endpoint: config.get<string>('metricsEndpoint', DEFAULT_ENDPOINT) || DEFAULT_ENDPOINT,
    pollIntervalMs: pollInterval(config.get<number>('pollInterval')),
    currencySymbol: config.get<string>('currencySymbol', '$'),
    tokenCostPer1000: config.get<number>('tokenCostPer1000', 0),
  };
}

/** The metrics token, kept in VS Code's SecretStorage rather than settings.json. */
export async function readToken(secrets: vscode.SecretStorage): Promise<string | undefined> {
  return (await secrets.get(TOKEN_SECRET_KEY)) || undefined;
}

/** Asks for the metrics token; an empty value clears it. */
export async function promptForToken(secrets: vscode.SecretStorage): Promise<boolean> {
  const value = await vscode.window.showInputBox({
    title: 'LeanProxy metrics token',
    prompt: 'The --metrics-token value (sent as "Authorization: Bearer <token>"). Leave empty to clear it.',
    password: true,
    ignoreFocusOut: true,
  });
  if (value === undefined) {
    return false;
  }
  if (value === '') {
    await secrets.delete(TOKEN_SECRET_KEY);
  } else {
    await secrets.store(TOKEN_SECRET_KEY, value);
  }
  return true;
}

/** Whether a configuration change affects the extension. */
export function affectsLeanProxy(e: vscode.ConfigurationChangeEvent): boolean {
  return e.affectsConfiguration('leanproxy');
}
