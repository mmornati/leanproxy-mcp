package com.leanproxy.jetbrains

import com.google.gson.Gson
import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class SnapshotNullTest {

    private val gson = Gson()

    @Test
    fun `test snapshot with null usage`() {
        val snapshot = gson.fromJson("""{"usage": null}""", MetricsSnapshot::class.java)
        assertNull(snapshot.usage)
    }

    @Test
    fun `test window with null fields`() {
        val json = """
            {"usage": {"today": {"saved_tokens": null, "by_server": null, "by_tool": null}, "week": null}}
        """.trimIndent()

        val usage = gson.fromJson(json, MetricsSnapshot::class.java).usage
        assertNotNull(usage)
        assertNull(usage!!.week)
        assertNull(usage.today?.saved_tokens)
        assertNull(usage.today?.by_server)
        assertNull(usage.today?.by_tool)
    }

    @Test
    fun `test safe defaults for null window fields`() {
        val window = UsageWindow()
        assertEquals(0L, window.saved_tokens ?: 0L)
        assertEquals(0, (window.by_server ?: emptyList()).size)
        assertEquals(0, (window.by_tool ?: emptyList()).size)
    }

    @Test
    fun `test tool display name with missing fields`() {
        assertEquals("unknown", UsageTool().displayName())
        assertEquals("read_file", UsageTool(tool = "read_file").displayName())
        assertEquals("fs.read_file", UsageTool(server = "fs", tool = "read_file").displayName())
    }
}
