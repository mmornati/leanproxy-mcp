package com.leanproxy.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

@State(name = "LeanProxySettings", storages = [Storage("leanproxy-settings.xml")])
class LeanProxySettings : PersistentStateComponent<LeanProxySettings> {
    var metricsEndpoint: String = "http://127.0.0.1:9090/metrics"
    var pollIntervalMs: Long = 1000
    var currencySymbol: String = "$"
    var tokenCostPer1000: Double = 0.002

    override fun getState(): LeanProxySettings = this

    override fun loadState(state: LeanProxySettings) {
        this.metricsEndpoint = state.metricsEndpoint
        this.pollIntervalMs = state.pollIntervalMs
        this.currencySymbol = state.currencySymbol
        this.tokenCostPer1000 = state.tokenCostPer1000
    }

    companion object {
        fun getInstance(): LeanProxySettings =
            ApplicationManager.getApplication().getService(LeanProxySettings::class.java)
    }
}
