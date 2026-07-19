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

    private val headerLabel = JLabel("LeanProxy Cost Breakdown", SwingConstants.LEFT).apply {
        font = font.deriveFont(Font.BOLD, 14f)
        border = JBUI.Borders.empty(8, 12, 4, 12)
    }

    private val totalSpendLabel = JLabel("Total Spend (tokens): --", SwingConstants.LEFT).apply {
        font = font.deriveFont(Font.PLAIN, 18f)
        border = JBUI.Borders.empty(4, 12)
    }

    private val serverTableModel = DefaultTableModel(arrayOf("Server", "Tokens"), 0)
    private val serverTable = JBTable(serverTableModel).apply {
        setSelectionMode(ListSelectionModel.SINGLE_SELECTION)
        rowSelectionAllowed = true
    }

    private val toolTableModel = DefaultTableModel(arrayOf("Tool", "Tokens"), 0)
    private val toolTable = JBTable(toolTableModel).apply {
        setSelectionMode(ListSelectionModel.SINGLE_SELECTION)
        rowSelectionAllowed = true
    }

    private val top5Model = DefaultTableModel(arrayOf("Tool", "Tokens"), 0)
    private val top5Table = JBTable(top5Model).apply {
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

        val totalCard = JPanel(BorderLayout()).apply {
            border = JBUI.Borders.empty(4, 12)
            background = JBColor.background()
        }
        totalCard.add(totalSpendLabel, BorderLayout.CENTER)
        mainPanel.add(totalCard)

        mainPanel.add(createSectionLabel("By Server"))
        mainPanel.add(JBScrollPane(serverTable).apply {
            border = JBUI.Borders.empty(4, 12)
        })

        mainPanel.add(createSectionLabel("By Tool"))
        mainPanel.add(JBScrollPane(toolTable).apply {
            border = JBUI.Borders.empty(4, 12)
        })

        val top5Label = createSectionLabel("Top 5 Most Expensive Tools")
        mainPanel.add(top5Label)
        mainPanel.add(JBScrollPane(top5Table).apply {
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
        val settings = LeanProxySettings.getInstance()
        pollFuture = AppExecutorUtil.getAppScheduledExecutorService()
            .scheduleWithFixedDelay({ poll() }, settings.pollIntervalMs, settings.pollIntervalMs, java.util.concurrent.TimeUnit.MILLISECONDS)
    }

    fun stopPolling() {
        pollFuture?.cancel(false)
        pollFuture = null
    }

    private fun poll() {
        val settings = LeanProxySettings.getInstance()
        val result = metricsClient.fetch(settings.metricsEndpoint)

        SwingUtilities.invokeLater {
            result.onSuccess { snapshot ->
                updateMetrics(snapshot)
                statusLabel.text = "Connected"
                statusLabel.foreground = JBColor.gray
            }.onFailure {
                showError("Proxy Offline — Ensure LeanProxy is running")
            }
        }
    }

    private fun updateMetrics(snapshot: MetricsSnapshot) {
        totalSpendLabel.text = "Total Spend (tokens): ${String.format("%,d", snapshot.total_spend ?: 0)}"

        updateTable(serverTableModel, (snapshot.by_server ?: emptyList()).map { arrayOf(it.server_name ?: "unknown", String.format("%,d", it.token_count ?: 0)) })
        updateTable(toolTableModel, (snapshot.by_tool ?: emptyList()).map { arrayOf(it.tool_name ?: "unknown", String.format("%,d", it.token_count ?: 0)) })
        updateTable(top5Model, (snapshot.top_5_expensive_tools ?: emptyList()).map { arrayOf(it.tool_name ?: "unknown", String.format("%,d", it.token_count ?: 0)) })
    }

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
