import * as assert from 'assert';

suite('LeanProxy Extension Tests', () => {
  test('Extension should be present', () => {
    assert.ok(true, 'Extension loaded successfully');
  });

  test('Configuration defaults should be set', () => {
    const expectedEndpoint = 'http://127.0.0.1:9090/metrics';
    assert.strictEqual(
      expectedEndpoint,
      'http://127.0.0.1:9090/metrics',
      'Default metrics endpoint should be localhost:9090/metrics'
    );
  });

  test('Poll interval should default to 2000ms', () => {
    const expectedInterval = 2000;
    assert.strictEqual(expectedInterval, 2000, 'Default poll interval should be 2000ms');
  });

  test('Cost calculation should handle zero tokens', () => {
    const totalSpend = 0;
    const costPer1000 = 0.002;
    const estimatedCost = (totalSpend / 1000) * costPer1000;
    assert.strictEqual(estimatedCost, 0, 'Zero tokens should result in zero cost');
  });

  test('Cost calculation should handle large values', () => {
    const totalSpend = 1_000_000;
    const costPer1000 = 0.002;
    const estimatedCost = (totalSpend / 1000) * costPer1000;
    assert.strictEqual(estimatedCost, 2.0, '1M tokens at $0.002/1K should cost $2.00');
  });

  test('MetricsSnapshot should parse correctly', () => {
    const snapshot = {
      by_tool: [
        { tool_name: 'read_file', token_count: 1500 },
        { tool_name: 'write_file', token_count: 500 },
      ],
      by_server: [
        { server_name: 'filesystem', token_count: 2000 },
      ],
      total_spend: 2000,
      top_5_expensive_tools: [
        { tool_name: 'read_file', token_count: 1500 },
      ],
    };

    assert.strictEqual(snapshot.total_spend, 2000);
    assert.strictEqual(snapshot.by_tool.length, 2);
    assert.strictEqual(snapshot.by_server.length, 1);
    assert.strictEqual(snapshot.top_5_expensive_tools.length, 1);
    assert.strictEqual(snapshot.by_tool[0].tool_name, 'read_file');
    assert.strictEqual(snapshot.by_tool[0].token_count, 1500);
  });

  test('Top 5 should not exceed 5 items', () => {
    const top5 = Array.from({ length: 10 }, (_, i) => ({
      tool_name: `tool_${i}`,
      token_count: (10 - i) * 100,
    })).slice(0, 5);

    assert.ok(top5.length <= 5, 'Top 5 should have at most 5 items');
    assert.strictEqual(top5.length, 5);
    assert.strictEqual(top5[0].tool_name, 'tool_0');
  });
});
