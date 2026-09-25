package com.leanproxy.jetbrains

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class SettingsTest {

    @Test
    fun `test settings state roundtrip`() {
        val settings = LeanProxySettings()
        settings.metricsEndpoint = "http://test:9091/metrics"
        settings.pollIntervalMs = 7000
        settings.currencySymbol = "£"
        settings.tokenCostPer1000 = 0.005

        val state = settings.state
        assertEquals("http://test:9091/metrics", state.metricsEndpoint)
        assertEquals(7000, state.pollIntervalMs)
        assertEquals("£", state.currencySymbol)
        assertEquals(0.005, state.tokenCostPer1000, 0.0001)

        val loaded = LeanProxySettings()
        loaded.loadState(state)
        assertEquals("http://test:9091/metrics", loaded.metricsEndpoint)
        assertEquals(7000, loaded.pollIntervalMs)
        assertEquals("£", loaded.currencySymbol)
        assertEquals(0.005, loaded.tokenCostPer1000, 0.0001)
    }

    @Test
    fun `test settings default values`() {
        val settings = LeanProxySettings()
        // The metrics port, not the dashboard's 9090.
        assertEquals("http://127.0.0.1:9091/metrics", settings.metricsEndpoint)
        assertEquals(5000, settings.pollIntervalMs)
        assertEquals("$", settings.currencySymbol)
        assertEquals(0.0, settings.tokenCostPer1000, 0.0001)
    }

    @Test
    fun `test poll interval is clamped`() {
        val settings = LeanProxySettings()
        settings.pollIntervalMs = 10
        assertEquals(LeanProxySettings.MIN_POLL_INTERVAL_MS, settings.effectivePollIntervalMs())
    }
}
