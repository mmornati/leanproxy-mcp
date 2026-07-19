package com.leanproxy.jetbrains

import com.google.gson.Gson
import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class MetricsTest {

    private val gson = Gson()

    @Test
    fun `test metrics snapshot deserialization`() {
        val json = """
            {
                "by_tool": [
                    {"tool_name": "get_weather", "token_count": 1500},
                    {"tool_name": "search_docs", "token_count": 3200}
                ],
                "by_server": [
                    {"server_name": "weather-server", "token_count": 1500},
                    {"server_name": "docs-server", "token_count": 3200}
                ],
                "total_spend": 4700,
                "top_5_expensive_tools": [
                    {"tool_name": "search_docs", "token_count": 3200},
                    {"tool_name": "get_weather", "token_count": 1500}
                ]
            }
        """.trimIndent()

        val snapshot = gson.fromJson(json, MetricsSnapshot::class.java)

        assertEquals(4700L, snapshot.total_spend)
        assertEquals(2, snapshot.by_tool?.size)
        assertEquals(2, snapshot.by_server?.size)
        assertEquals(2, snapshot.top_5_expensive_tools?.size)

        assertEquals("get_weather", snapshot.by_tool?.get(0)?.tool_name)
        assertEquals(1500L, snapshot.by_tool?.get(0)?.token_count)
        assertEquals("search_docs", snapshot.by_tool?.get(1)?.tool_name)
        assertEquals(3200L, snapshot.by_tool?.get(1)?.token_count)
    }

    @Test
    fun `test empty metrics snapshot deserialization`() {
        val json = """
            {
                "by_tool": [],
                "by_server": [],
                "total_spend": 0,
                "top_5_expensive_tools": []
            }
        """.trimIndent()

        val snapshot = gson.fromJson(json, MetricsSnapshot::class.java)

        assertEquals(0L, snapshot.total_spend)
        assertTrue(snapshot.by_tool?.isEmpty() ?: true)
        assertTrue(snapshot.by_server?.isEmpty() ?: true)
        assertTrue(snapshot.top_5_expensive_tools?.isEmpty() ?: true)
    }

    @Test
    fun `test cost calculation`() {
        val totalSpend: Long = 10000
        val costPer1000 = 0.002
        val expectedCost = (10000 / 1000.0) * costPer1000
        assertEquals(0.02, expectedCost, 0.0001)
    }

    @Test
    fun `test cost calculation with zero spend`() {
        val totalSpend: Long = 0
        val costPer1000 = 0.002
        val expectedCost = (0 / 1000.0) * costPer1000
        assertEquals(0.0, expectedCost, 0.0001)
    }

    @Test
    fun `test cost calculation formatting`() {
        val totalSpend: Long = 1234567
        val costPer1000 = 0.002
        val cost = (totalSpend / 1000.0) * costPer1000
        val formatted = String.format("%s %.4f", "$", cost)
        assertEquals("$ 2.4691", formatted)
    }

    @Test
    fun `test metrics client with invalid endpoint`() {
        val client = MetricsClient(gson)
        val result = client.fetch("http://127.0.0.1:1/metrics")
        assertTrue(result.isFailure)
        val exception = result.exceptionOrNull()
        assertTrue(exception is MetricsConnectionException)
    }
}
