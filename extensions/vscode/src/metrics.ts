// Types and helpers for LeanProxy's /metrics endpoint. Kept free of the
// vscode API so they can be unit-tested directly.

/** One tool's results in a usage window (`usage.today.by_tool[]`). */
export interface UsageTool {
  server: string;
  tool: string;
  calls: number;
  original_tokens: number;
  returned_tokens: number;
  saved_tokens: number;
}

/** One upstream server's results in a usage window. */
export interface UsageServer {
  server: string;
  tools: number;
  calls: number;
  original_tokens: number;
  returned_tokens: number;
  saved_tokens: number;
}

/** Tokens recorded between `since` and now (today, or week-to-date). */
export interface UsageWindow {
  since: string;
  sessions: number;
  original_tokens: number;
  saved_tokens: number;
  saved_percent: number;
  discovery_calls: number;
  discovery_tokens: number;
  tool_calls: number;
  top_server?: string;
  top_tool?: string;
  by_server: UsageServer[];
  by_tool: UsageTool[];
}

export interface UsageSummary {
  estimator: string;
  today: UsageWindow;
  week: UsageWindow;
}

/**
 * The part of the /metrics response the extension reads. `usage` is absent
 * when the proxy could not read its usage store.
 */
export interface MetricsResponse {
  usage?: UsageSummary;
}

/**
 * The metrics endpoint's conventional address: `--metrics-bind
 * 127.0.0.1:9091`. (9090 is the dashboard's default port, which has no
 * /metrics route.)
 */
export const DEFAULT_ENDPOINT = 'http://127.0.0.1:9091/metrics';

/** The proxy appends a usage snapshot every 5 s; polling faster only repeats it. */
export const DEFAULT_POLL_INTERVAL_MS = 5000;
export const MIN_POLL_INTERVAL_MS = 1000;

export class MetricsError extends Error {
  constructor(message: string, readonly status?: number) {
    super(message);
    this.name = 'MetricsError';
  }
}

type FetchLike = (input: string, init?: { headers?: Record<string, string> }) => Promise<{
  ok: boolean;
  status: number;
  json(): Promise<unknown>;
}>;

/**
 * GETs the metrics endpoint, sending `Authorization: Bearer <token>` when a
 * token is set (`--metrics-token`).
 */
export async function fetchMetrics(endpoint: string, token?: string, fetchImpl: FetchLike = fetch): Promise<MetricsResponse> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  let res;
  try {
    res = await fetchImpl(endpoint, { headers });
  } catch (err) {
    throw new MetricsError(`proxy offline (${err instanceof Error ? err.message : String(err)})`);
  }
  if (!res.ok) {
    throw new MetricsError(describeStatus(res.status), res.status);
  }
  return (await res.json()) as MetricsResponse;
}

/** A human explanation of an HTTP error from the metrics endpoint. */
export function describeStatus(status: number): string {
  switch (status) {
    case 401:
      return 'HTTP 401: the metrics endpoint needs a token. Run "LeanProxy: Set Metrics Token".';
    case 403:
      return 'HTTP 403: Host not allowed. Use 127.0.0.1/localhost or add it with --metrics-allowed-hosts.';
    case 404:
      return 'HTTP 404: no /metrics here. Point leanproxy.metricsEndpoint at --metrics-bind (not the dashboard port).';
    default:
      return `HTTP ${status}`;
  }
}

/** The configured poll interval, defaulted and clamped to the minimum. */
export function pollInterval(configured: number | undefined): number {
  if (configured === undefined || !Number.isFinite(configured)) {
    return DEFAULT_POLL_INTERVAL_MS;
  }
  return Math.max(MIN_POLL_INTERVAL_MS, Math.round(configured));
}

/** 1234 -> "1.2K", 2500000 -> "2.5M". */
export function formatTokens(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1)}M`;
  }
  if (n >= 1_000) {
    return `${(n / 1_000).toFixed(1)}K`;
  }
  return `${n}`;
}

/**
 * The estimated cost of `tokens` at `costPer1000`, or undefined when no
 * price is configured (LeanProxy has no built-in price table).
 */
export function estimatedCost(tokens: number, costPer1000: number | undefined): number | undefined {
  if (!costPer1000 || costPer1000 <= 0 || !Number.isFinite(costPer1000)) {
    return undefined;
  }
  const cost = (tokens / 1000) * costPer1000;
  return Number.isFinite(cost) ? cost : undefined;
}
