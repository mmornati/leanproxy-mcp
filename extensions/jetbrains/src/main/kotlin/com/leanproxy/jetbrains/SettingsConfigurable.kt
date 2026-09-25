package com.leanproxy.jetbrains

import com.intellij.openapi.options.Configurable
import com.intellij.util.ui.JBUI
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JPasswordField
import javax.swing.JTextField

class SettingsConfigurable : Configurable {
    private var pollIntervalField: JTextField? = null
    private var metricsEndpointField: JTextField? = null
    private var metricsTokenField: JPasswordField? = null
    private var currencySymbolField: JTextField? = null
    private var tokenCostField: JTextField? = null
    private var panel: JPanel? = null

    override fun getDisplayName(): String = "LeanProxy Cost Monitor"

    override fun createComponent(): JComponent {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField = JTextField(settings.pollIntervalMs.toString())
        metricsEndpointField = JTextField(settings.metricsEndpoint)
        metricsTokenField = JPasswordField(LeanProxySettings.metricsToken() ?: "")
        currencySymbolField = JTextField(settings.currencySymbol)
        tokenCostField = JTextField(settings.tokenCostPer1000.toString())

        val p = JPanel(GridBagLayout())
        val c = GridBagConstraints()
        c.insets = JBUI.insets(4)
        c.fill = GridBagConstraints.HORIZONTAL

        fun addRow(row: Int, label: String, field: JComponent) {
            c.gridx = 0; c.gridy = row; c.weightx = 0.0
            p.add(JLabel(label), c)
            c.gridx = 1; c.weightx = 1.0
            p.add(field, c)
        }

        addRow(0, "Metrics endpoint (--metrics-bind + /metrics):", metricsEndpointField!!)
        addRow(1, "Metrics token (--metrics-token, optional):", metricsTokenField!!)
        addRow(2, "Poll interval (ms, min ${LeanProxySettings.MIN_POLL_INTERVAL_MS}):", pollIntervalField!!)
        addRow(3, "Currency symbol:", currencySymbolField!!)
        addRow(4, "Price per 1000 tokens (0 = tokens only):", tokenCostField!!)

        panel = p
        return p
    }

    private fun tokenText(): String = metricsTokenField?.password?.let { String(it) } ?: ""

    override fun isModified(): Boolean {
        val settings = LeanProxySettings.getInstance()
        return pollIntervalField?.text?.toLongOrNull() != settings.pollIntervalMs
                || metricsEndpointField?.text != settings.metricsEndpoint
                || currencySymbolField?.text != settings.currencySymbol
                || tokenCostField?.text?.toDoubleOrNull() != settings.tokenCostPer1000
                || tokenText() != (LeanProxySettings.metricsToken() ?: "")
    }

    override fun apply() {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField?.text?.toLongOrNull()?.let { settings.pollIntervalMs = maxOf(LeanProxySettings.MIN_POLL_INTERVAL_MS, it) }
        metricsEndpointField?.text?.let { if (it.isNotBlank()) settings.metricsEndpoint = it.trim() }
        currencySymbolField?.text?.let { settings.currencySymbol = it }
        tokenCostField?.text?.toDoubleOrNull()?.let { if (it >= 0.0) settings.tokenCostPer1000 = it }
        LeanProxySettings.setMetricsToken(tokenText())
    }

    override fun reset() {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField?.text = settings.pollIntervalMs.toString()
        metricsEndpointField?.text = settings.metricsEndpoint
        metricsTokenField?.text = LeanProxySettings.metricsToken() ?: ""
        currencySymbolField?.text = settings.currencySymbol
        tokenCostField?.text = settings.tokenCostPer1000.toString()
    }

    override fun disposeUIResources() {
        panel = null
        pollIntervalField = null
        metricsEndpointField = null
        metricsTokenField = null
        currencySymbolField = null
        tokenCostField = null
    }
}
