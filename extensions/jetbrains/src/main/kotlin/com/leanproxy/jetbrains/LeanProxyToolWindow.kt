package com.leanproxy.jetbrains

import com.intellij.openapi.Disposable
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Disposer
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.table.JBTable
import com.intellij.util.concurrency.AppExecutorUtil
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.Font
import javax.swing.*
import javax.swing.table.DefaultTableModel
import javax.swing.table.TableCellRenderer

class LeanProxyToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = LeanProxyToolWindowPanel(project)
        val content = toolWindow.contentManager.factory.createContent(panel, "", false)
        toolWindow.contentManager.addContent(content)
        Disposer.register(toolWindow.disposable, panel)
    }
}

class LeanProxyToolWindowPanel(private val project: Project) : JPanel(BorderLayout()), Disposable {
    private val metricsClient = MetricsClient()
    private var pollFuture: java.util.concurrent.ScheduledFuture<*>? = null

    private var scheduledIntervalMs: Long = 0

    private val headerLabel = JLabel("LeanProxy Token Usage", SwingConstants.LEFT).apply {
        font = font.deriveFont(Font.BOLD, 14f)
        border = JBUI.Borders.empty(8, 12, 4, 12)
    }

    private val todayLabel = JLabel("Today: --", SwingConstants.LEFT).apply {
        font = font.deriveFont(Font.PLAIN, 16f)
        border = JBUI.Borders.empty(4, 12)
    }

    private val weekLabel = JLabel("This week: --", SwingConstants.LEFT).apply {
        font = font.deriveFont(Font.PLAIN, 14f)
        border = JBUI.Borders.empty(2, 12)
    }

    private val noteLabel = JLabel(
        "<html>Tokens measured by the proxy. Today is since 00:00 UTC; the week starts Monday 00:00 UTC. " +
            "Per-server and per-tool rows need the response governor (response.enabled: true).</html>",
        SwingConstants.LEFT
    ).apply {
        foreground = JBColor.gray
        border = JBUI.Borders.empty(4, 12)
    }

    private val serverTableModel = DefaultTableModel(arrayOf("Server", "Calls", "Response tokens", "Saved"), 0)
    private val serverTable = JBTable(serverTableModel).apply {
        setSelectionMode(ListSelectionModel.SINGLE_SELECTION)
        rowSelectionAllowed = true
    }

    private val toolTableModel = DefaultTableModel(arrayOf("Tool", "Calls", "Response tokens", "Saved"), 0)
    private val toolTable = JBTable(toolTableModel).apply {
        setSelectionMode(ListSelectionModel.SINGLE_SELECTION)
        rowSelectionAllowed = true
    }

    private val statusLabel = JLabel("Connecting to LeanProxy...", SwingConstants.CENTER).apply {
        foreground = JBColor.gray
        border = JBUI.Borders.empty(8, 12)
    }

    init {
        setupUI()
        startPolling()
    }

    private fun setupUI() {
        val mainPanel = JPanel()
        mainPanel.layout = BoxLayout(mainPanel, BoxLayout.Y_AXIS)

        mainPanel.add(headerLabel)

        val totalCard = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            border = JBUI.Borders.empty(4, 12)
            background = JBColor.background()
        }
        totalCard.add(todayLabel)
        totalCard.add(weekLabel)
        mainPanel.add(totalCard)
        mainPanel.add(noteLabel)

        mainPanel.add(createSectionLabel("By Server (today)"))
        mainPanel.add(JBScrollPane(serverTable).apply {
            border = JBUI.Borders.empty(4, 12)
        })

        mainPanel.add(createSectionLabel("Top Tools by Response Size (today)"))
        mainPanel.add(JBScrollPane(toolTable).apply {
            border = JBUI.Borders.empty(4, 12)
        })

        mainPanel.add(statusLabel)

        val scrollPane = JBScrollPane(mainPanel)
        add(scrollPane, BorderLayout.CENTER)
    }

    private fun createSectionLabel(text: String): JLabel {
        return JLabel(text, SwingConstants.LEFT).apply {
            font = font.deriveFont(Font.BOLD, 12f)
            border = JBUI.Borders.empty(12, 12, 4, 12)
        }
    }

    fun startPolling() {
        poll()
        schedule()
    }

    private fun schedule() {
        pollFuture?.cancel(false)
        scheduledIntervalMs = LeanProxySettings.getInstance().effectivePollIntervalMs()
        pollFuture = AppExecutorUtil.getAppScheduledExecutorService()
            .scheduleWithFixedDelay({ poll() }, scheduledIntervalMs, scheduledIntervalMs, java.util.concurrent.TimeUnit.MILLISECONDS)
    }

    fun stopPolling() {
        pollFuture?.cancel(false)
        pollFuture = null
    }

    private fun poll() {
        val settings = LeanProxySettings.getInstance()
        // Pick up a poll interval changed in the settings.
        if (pollFuture != null && settings.effectivePollIntervalMs() != scheduledIntervalMs) {
            schedule()
        }
        val result = metricsClient.fetch(settings.metricsEndpoint, LeanProxySettings.metricsToken())

        SwingUtilities.invokeLater {
            result.onSuccess { snapshot ->
                val usage = snapshot.usage
                if (usage == null) {
                    showError("LeanProxy is running but could not read its usage store")
                } else {
                    updateMetrics(usage, settings)
                    statusLabel.text = "Connected (${usage.estimator ?: "chars/4"})"
                    statusLabel.foreground = JBColor.gray
                }
            }.onFailure { error ->
                showError("Proxy offline \u2014 ${error.message ?: "ensure LeanProxy runs with --metrics-bind"}")
            }
        }
    }

    private fun updateMetrics(usage: UsageSummary, settings: LeanProxySettings) {
        todayLabel.text = windowText("Today", usage.today, settings)
        weekLabel.text = windowText("This week", usage.week, settings)

        val today = usage.today
        updateTable(serverTableModel, (today?.by_server ?: emptyList()).map {
            arrayOf(if (it.server.isNullOrEmpty()) "(no server)" else it.server, num(it.calls), num(it.original_tokens), num(it.saved_tokens))
        })
        updateTable(toolTableModel, (today?.by_tool ?: emptyList()).take(10).map {
            arrayOf(it.displayName(), num(it.calls), num(it.original_tokens), num(it.saved_tokens))
        })
    }

    private fun windowText(label: String, w: UsageWindow?, settings: LeanProxySettings): String {
        val saved = w?.saved_tokens ?: 0
        val text = String.format(
            "%s: %,d tokens saved of %,d (%.1f%%)",
            label, saved, w?.original_tokens ?: 0, w?.saved_percent ?: 0.0
        )
        val cost = estimatedCost(saved, settings.tokenCostPer1000) ?: return text
        return text + String.format(" \u2014 ~%s%.4f", settings.currencySymbol, cost)
    }

    private fun num(n: Long?): String = String.format("%,d", n ?: 0)

    private fun updateTable(model: DefaultTableModel, rows: List<Array<String>>) {
        model.setRowCount(0)
        for (row in rows) {
            model.addRow(row)
        }
    }

    private fun showError(message: String) {
        statusLabel.text = message
        statusLabel.foreground = JBColor.RED
    }

    override fun dispose() {
        stopPolling()
    }
}
