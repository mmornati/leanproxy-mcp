package com.leanproxy.jetbrains

import com.google.gson.Gson
import com.sun.net.httpserver.HttpServer
import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test
import java.net.InetSocketAddress

class MetricsTest {

    private val gson = Gson()

    private val sampleJson = """
        {
            "telemetry": {"requests_total": 12},
            "usage": {
                "estimator": "chars/4",
                "today": {
                    "since": "2026-09-23T00:00:00Z", "sessions": 1,
                    "original_tokens": 5000, "saved_tokens": 4000, "saved_percent": 80.0,
                    "discovery_calls": 1, "discovery_tokens": 90, "tool_calls": 2,
                    "top_server": "github", "top_tool": "github.search_code",
                    "by_server": [{"server": "github", "tools": 1, "calls": 2, "original_tokens": 900, "returned_tokens": 300, "saved_tokens": 600}],
                    "by_tool": [{"server": "github", "tool": "search_code", "calls": 2, "original_tokens": 900, "returned_tokens": 300, "saved_tokens": 600}]
                },
                "week": {"sessions": 3, "original_tokens": 50000, "saved_tokens": 30000, "saved_percent": 60.0, "by_server": [], "by_tool": []}
            }
        }
    """.trimIndent()

    @Test
    fun `test metrics snapshot deserialization`() {
        val snapshot = gson.fromJson(sampleJson, MetricsSnapshot::class.java)
        val usage = snapshot.usage
        assertNotNull(usage)
        assertEquals("chars/4", usage!!.estimator)
        assertEquals(4000L, usage.today?.saved_tokens)
        assertEquals(30000L, usage.week?.saved_tokens)
        assertEquals("github", usage.today?.by_server?.get(0)?.server)
        assertEquals("github.search_code", usage.today?.by_tool?.get(0)?.displayName())
        assertEquals(600L, usage.today?.by_tool?.get(0)?.saved_tokens)
    }

    @Test
    fun `test snapshot without usage section`() {
        val snapshot = gson.fromJson("""{"telemetry": {}}""", MetricsSnapshot::class.java)
        assertNull(snapshot.usage)
    }

    @Test
    fun `test estimated cost needs a price`() {
        assertNull(estimatedCost(1_000_000, 0.0))
        assertEquals(2.0, estimatedCost(1_000_000, 0.002)!!, 0.0001)
    }

    @Test
    fun `test format tokens`() {
        assertEquals("999", formatTokens(999))
        assertEquals("1.5K", formatTokens(1500))
        assertEquals("2.5M", formatTokens(2_500_000))
    }

    @Test
    fun `test metrics client with invalid endpoint`() {
        val client = MetricsClient(gson)
        val result = client.fetch("http://127.0.0.1:1/metrics")
        assertTrue(result.isFailure)
        val exception = result.exceptionOrNull()
        assertTrue(exception is MetricsConnectionException)
    }

    @Test
    fun `test metrics client sends bearer token`() {
        var authorization: String? = null
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/metrics") { exchange ->
            authorization = exchange.requestHeaders.getFirst("Authorization")
            val status = if (authorization == "Bearer s3cret") 200 else 401
            val body = if (status == 200) sampleJson.toByteArray() else ByteArray(0)
            exchange.sendResponseHeaders(status, if (body.isEmpty()) -1 else body.size.toLong())
            if (body.isNotEmpty()) exchange.responseBody.use { it.write(body) }
            exchange.close()
        }
        server.start()
        try {
            val endpoint = "http://127.0.0.1:${server.address.port}/metrics"
            val client = MetricsClient(gson)

            val unauthorized = client.fetch(endpoint)
            assertNull(authorization)
            val error = unauthorized.exceptionOrNull()
            assertTrue(error is MetricsHttpException)
            assertEquals(401, (error as MetricsHttpException).statusCode)
            assertTrue(error.message!!.contains("token"))

            val ok = client.fetch(endpoint, "s3cret")
            assertEquals("Bearer s3cret", authorization)
            assertEquals(4000L, ok.getOrThrow().usage?.today?.saved_tokens)
        } finally {
            server.stop(0)
        }
    }
}
