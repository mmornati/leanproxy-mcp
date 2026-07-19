package com.leanproxy.jetbrains

import com.intellij.openapi.options.Configurable
import com.intellij.util.ui.JBUI
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextField

class SettingsConfigurable : Configurable {
    private var pollIntervalField: JTextField? = null
    private var metricsEndpointField: JTextField? = null
    private var panel: JPanel? = null

    override fun getDisplayName(): String = "LeanProxy Cost Monitor"

    override fun createComponent(): JComponent {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField = JTextField(settings.pollIntervalMs.toString())
        metricsEndpointField = JTextField(settings.metricsEndpoint)

        val p = JPanel(GridBagLayout())
        val c = GridBagConstraints()
        c.insets = JBUI.insets(4)
        c.fill = GridBagConstraints.HORIZONTAL

        c.gridx = 0; c.gridy = 0
        p.add(JLabel("Poll interval (ms):"), c)
        c.gridx = 1; c.weightx = 1.0
        p.add(pollIntervalField!!, c)

        c.gridx = 0; c.gridy = 1; c.weightx = 0.0
        p.add(JLabel("Metrics endpoint:"), c)
        c.gridx = 1; c.weightx = 1.0
        p.add(metricsEndpointField!!, c)

        panel = p
        return p
    }

    override fun isModified(): Boolean {
        val settings = LeanProxySettings.getInstance()
        return pollIntervalField?.text?.toLongOrNull() != settings.pollIntervalMs
                || metricsEndpointField?.text != settings.metricsEndpoint
    }

    override fun apply() {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField?.text?.toLongOrNull()?.let { settings.pollIntervalMs = it }
        metricsEndpointField?.text?.let { if (it.isNotBlank()) settings.metricsEndpoint = it }
    }

    override fun reset() {
        val settings = LeanProxySettings.getInstance()
        pollIntervalField?.text = settings.pollIntervalMs.toString()
        metricsEndpointField?.text = settings.metricsEndpoint
    }

    override fun disposeUIResources() {
        panel = null
        pollIntervalField = null
        metricsEndpointField = null
    }
}
