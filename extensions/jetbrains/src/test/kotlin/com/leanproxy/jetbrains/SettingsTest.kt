package com.leanproxy.jetbrains

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class SettingsTest {

    @Test
    fun `test settings state roundtrip`() {
        val settings = LeanProxySettings()
        settings.metricsEndpoint = "http://test:9090/metrics"
        settings.pollIntervalMs = 5000
        settings.currencySymbol = "\u00a3"
        settings.tokenCostPer1000 = 0.005

        val state = settings.state
        assertEquals("http://test:9090/metrics", state.metricsEndpoint)
        assertEquals(5000, state.pollIntervalMs)
        assertEquals("\u00a3", state.currencySymbol)
        assertEquals(0.005, state.tokenCostPer1000, 0.0001)

        val loaded = LeanProxySettings()
        loaded.loadState(state)
        assertEquals("http://test:9090/metrics", loaded.metricsEndpoint)
        assertEquals(5000, loaded.pollIntervalMs)
        assertEquals("\u00a3", loaded.currencySymbol)
        assertEquals(0.005, loaded.tokenCostPer1000, 0.0001)
    }

    @Test
    fun `test settings default values`() {
        val settings = LeanProxySettings()
        assertEquals("http://127.0.0.1:9090/metrics", settings.metricsEndpoint)
        assertEquals(1000, settings.pollIntervalMs)
        assertEquals("$", settings.currencySymbol)
        assertEquals(0.002, settings.tokenCostPer1000, 0.0001)
    }
}
