import * as assert from 'assert';
import {
  DEFAULT_ENDPOINT,
  DEFAULT_POLL_INTERVAL_MS,
  MIN_POLL_INTERVAL_MS,
  MetricsError,
  estimatedCost,
  fetchMetrics,
  formatTokens,
  pollInterval,
} from '../metrics';

// eslint-disable-next-line @typescript-eslint/no-var-requires
const pkg = require('../../package.json');

function fakeFetch(status: number, body: unknown, seen: { url?: string; headers?: Record<string, string> }) {
  return async (url: string, init?: { headers?: Record<string, string> }) => {
    seen.url = url;
    seen.headers = init?.headers;
    return { ok: status >= 200 && status < 300, status, json: async () => body };
  };
}

suite('LeanProxy Extension Tests', () => {
  test('default endpoint is the metrics port, not the dashboard port', () => {
    assert.strictEqual(DEFAULT_ENDPOINT, 'http://127.0.0.1:9091/metrics');
    const props = pkg.contributes.configuration.properties;
    assert.strictEqual(props['leanproxy.metricsEndpoint'].default, DEFAULT_ENDPOINT);
    assert.strictEqual(props['leanproxy.pollInterval'].default, DEFAULT_POLL_INTERVAL_MS);
  });

  test('poll interval is defaulted and clamped', () => {
    assert.strictEqual(pollInterval(undefined), DEFAULT_POLL_INTERVAL_MS);
    assert.strictEqual(pollInterval(NaN), DEFAULT_POLL_INTERVAL_MS);
    assert.strictEqual(pollInterval(10), MIN_POLL_INTERVAL_MS);
    assert.strictEqual(pollInterval(2500), 2500);
  });

  test('fetchMetrics sends the bearer token when set', async () => {
    const seen: { url?: string; headers?: Record<string, string> } = {};
    await fetchMetrics(DEFAULT_ENDPOINT, 's3cret', fakeFetch(200, {}, seen));
    assert.strictEqual(seen.url, DEFAULT_ENDPOINT);
    assert.strictEqual(seen.headers?.Authorization, 'Bearer s3cret');

    const noToken: { headers?: Record<string, string> } = {};
    await fetchMetrics(DEFAULT_ENDPOINT, undefined, fakeFetch(200, {}, noToken));
    assert.strictEqual(noToken.headers?.Authorization, undefined);
  });

  test('fetchMetrics explains 401 and 404', async () => {
    for (const [status, hint] of [[401, 'Set Metrics Token'], [404, 'dashboard']] as const) {
      await assert.rejects(fetchMetrics(DEFAULT_ENDPOINT, undefined, fakeFetch(status, {}, {})), (err: unknown) => {
        assert.ok(err instanceof MetricsError);
        assert.strictEqual(err.status, status);
        assert.ok(err.message.includes(hint), err.message);
        return true;
      });
    }
  });

  test('fetchMetrics returns the usage section', async () => {
    const window = {
      since: '2026-09-23T00:00:00Z', sessions: 1, original_tokens: 5000, saved_tokens: 4000, saved_percent: 80,
      discovery_calls: 0, discovery_tokens: 0, tool_calls: 2, top_server: 'github', top_tool: 'github.search_code',
      by_server: [{ server: 'github', tools: 1, calls: 2, original_tokens: 900, returned_tokens: 300, saved_tokens: 600 }],
      by_tool: [{ server: 'github', tool: 'search_code', calls: 2, original_tokens: 900, returned_tokens: 300, saved_tokens: 600 }],
    };
    const data = await fetchMetrics(DEFAULT_ENDPOINT, undefined,
      fakeFetch(200, { telemetry: {}, usage: { estimator: 'chars/4', today: window, week: window } }, {}));
    assert.strictEqual(data.usage?.today.saved_tokens, 4000);
    assert.strictEqual(data.usage?.today.by_server[0].server, 'github');
  });

  test('estimated cost needs a configured price', () => {
    assert.strictEqual(estimatedCost(1_000_000, 0), undefined);
    assert.strictEqual(estimatedCost(1_000_000, undefined), undefined);
    assert.strictEqual(estimatedCost(1_000_000, 0.002), 2);
  });

  test('formatTokens', () => {
    assert.strictEqual(formatTokens(999), '999');
    assert.strictEqual(formatTokens(1500), '1.5K');
    assert.strictEqual(formatTokens(2_500_000), '2.5M');
  });
});
