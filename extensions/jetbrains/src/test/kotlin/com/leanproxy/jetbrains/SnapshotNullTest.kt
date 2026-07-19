package com.leanproxy.jetbrains

import com.google.gson.Gson
import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class SnapshotNullTest {

    private val gson = Gson()

    @Test
    fun `test snapshot with null fields`() {
        val json = """
            {
                "by_tool": null,
                "by_server": null,
                "total_spend": null,
                "top_5_expensive_tools": null
            }
        """.trimIndent()

        val snapshot = gson.fromJson(json, MetricsSnapshot::class.java)
        assertNull(snapshot.total_spend)
        assertNull(snapshot.by_tool)
        assertNull(snapshot.by_server)
        assertNull(snapshot.top_5_expensive_tools)
    }

    @Test
    fun `test tool metric with null fields`() {
        val json = """
            {
                "tool_name": null,
                "token_count": null
            }
        """.trimIndent()

        val metric = gson.fromJson(json, ToolMetric::class.java)
        assertNull(metric.tool_name)
        assertNull(metric.token_count)
    }

    @Test
    fun `test snapshot partial null fields`() {
        val json = """
            {
                "total_spend": 5000,
                "by_tool": [{"tool_name": "test", "token_count": 100}],
                "by_server": null,
                "top_5_expensive_tools": null
            }
        """.trimIndent()

        val snapshot = gson.fromJson(json, MetricsSnapshot::class.java)
        assertEquals(5000, snapshot.total_spend)
        assertEquals(1, snapshot.by_tool?.size)
        assertNull(snapshot.by_server)
        assertNull(snapshot.top_5_expensive_tools)
    }

    @Test
    fun `test safe defaults for null snapshot fields`() {
        val snapshot = MetricsSnapshot()
        assertEquals(0, snapshot.total_spend ?: 0)
        assertEquals(0, (snapshot.by_tool ?: emptyList()).size)
        assertEquals(0, (snapshot.by_server ?: emptyList()).size)
        assertEquals(0, (snapshot.top_5_expensive_tools ?: emptyList()).size)
    }

    @Test
    fun `test safe defaults for null tool metric`() {
        val metric = ToolMetric()
        assertEquals("unknown", metric.tool_name ?: "unknown")
        assertEquals(0, metric.token_count ?: 0)
    }
}
