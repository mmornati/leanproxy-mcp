package com.leanproxy.jetbrains

import com.intellij.credentialStore.CredentialAttributes
import com.intellij.credentialStore.Credentials
import com.intellij.credentialStore.generateServiceName
import com.intellij.ide.passwordSafe.PasswordSafe
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

@State(name = "LeanProxySettings", storages = [Storage("leanproxy-settings.xml")])
class LeanProxySettings : PersistentStateComponent<LeanProxySettings> {
    // The metrics endpoint's conventional address (--metrics-bind
    // 127.0.0.1:9091); 9090 is the dashboard's default port, which has no
    // /metrics route.
    var metricsEndpoint: String = DEFAULT_ENDPOINT
    // The proxy records a new usage snapshot every 5 seconds.
    var pollIntervalMs: Long = DEFAULT_POLL_INTERVAL_MS
    var currencySymbol: String = "$"
    // 0 shows tokens only: LeanProxy has no built-in price table.
    var tokenCostPer1000: Double = 0.0

    override fun getState(): LeanProxySettings = this

    override fun loadState(state: LeanProxySettings) {
        this.metricsEndpoint = state.metricsEndpoint
        this.pollIntervalMs = state.pollIntervalMs
        this.currencySymbol = state.currencySymbol
        this.tokenCostPer1000 = state.tokenCostPer1000
    }

    /** The poll interval, clamped to the minimum. */
    fun effectivePollIntervalMs(): Long = maxOf(MIN_POLL_INTERVAL_MS, pollIntervalMs)

    companion object {
        const val DEFAULT_ENDPOINT = "http://127.0.0.1:9091/metrics"
        const val DEFAULT_POLL_INTERVAL_MS = 5000L
        const val MIN_POLL_INTERVAL_MS = 1000L

        fun getInstance(): LeanProxySettings =
            ApplicationManager.getApplication().getService(LeanProxySettings::class.java)

        private val tokenAttributes: CredentialAttributes
            get() = CredentialAttributes(generateServiceName("LeanProxy", "metricsToken"))

        /**
         * The --metrics-token value, kept in the IDE's password safe rather
         * than in leanproxy-settings.xml. Null when not set.
         */
        fun metricsToken(): String? =
            PasswordSafe.instance.getPassword(tokenAttributes)?.takeIf { it.isNotEmpty() }

        /** Stores the metrics token; null or empty clears it. */
        fun setMetricsToken(token: String?) {
            val credentials = if (token.isNullOrEmpty()) null else Credentials("metrics", token)
            PasswordSafe.instance.set(tokenAttributes, credentials)
        }
    }
}
