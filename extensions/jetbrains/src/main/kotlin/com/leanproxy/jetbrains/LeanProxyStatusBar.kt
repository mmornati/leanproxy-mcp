package com.leanproxy.jetbrains

import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.StatusBarWidget
import com.intellij.openapi.wm.StatusBarWidgetFactory
import com.intellij.openapi.wm.ToolWindowManager
import com.intellij.openapi.wm.WindowManager
import com.intellij.util.concurrency.AppExecutorUtil
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import javax.swing.JLabel

class LeanProxyStatusBarWidgetFactory : StatusBarWidgetFactory {
    override fun getId(): String = "LeanProxyStatusBar"
    override fun getDisplayName(): String = "LeanProxy Cost Monitor"
    override fun isAvailable(project: Project): Boolean = true

    override fun createWidget(project: Project): StatusBarWidget {
        val widget = LeanProxyStatusBarWidget(project)
        widget.start()
        return widget
    }

    override fun disposeWidget(widget: StatusBarWidget) {
        if (widget is LeanProxyStatusBarWidget) {
            widget.dispose()
        }
    }
}

class LeanProxyStatusBarWidget(private val project: Project) : StatusBarWidget, StatusBarWidget.TextPresentation {
    private var displayText: String = "LeanProxy..."
    private var pollFuture: ScheduledFuture<*>? = null
    private val metricsClient = MetricsClient()
    private var connected = false

    override fun ID(): String = "LeanProxyStatusBar"

    override fun getPresentation(): StatusBarWidget.TextPresentation = this

    override fun getText(): String = displayText

    override fun getAlignment(): Float = JLabel.LEFT

    override fun getTooltipText(): String? =
        if (connected) "LeanProxy AI Cost \u2014 Click for details"
        else "LeanProxy unavailable"

    override fun getIcon(): javax.swing.Icon? = null

    override fun getClickConsumer(): StatusBarWidget.ClickConsumer? {
        return StatusBarWidget.ClickConsumer {
            val toolWindowManager = ToolWindowManager.getInstance(project)
            toolWindowManager.getToolWindow("LeanProxy")?.show()
        }
    }

    fun start() {
        poll()
        val settings = LeanProxySettings.getInstance()
        pollFuture = AppExecutorUtil.getAppScheduledExecutorService()
            .scheduleWithFixedDelay({ poll() }, settings.pollIntervalMs, settings.pollIntervalMs, TimeUnit.MILLISECONDS)
    }

    override fun dispose() {
        stop()
    }

    private fun stop() {
        pollFuture?.cancel(false)
        pollFuture = null
    }

    fun refresh() {
        poll()
    }

    private fun poll() {
        val settings = LeanProxySettings.getInstance()
        val result = metricsClient.fetch(settings.metricsEndpoint)
        result.onSuccess { snapshot ->
            connected = true
            updateDisplay(snapshot.total_spend)
        }.onFailure { error ->
            connected = false
            displayText = when (error) {
                is MetricsConnectionException -> "\u26A0 LeanProxy"
                is MetricsHttpException -> "\u26A0 HTTP ${error.statusCode}"
                else -> "\u26A0 LeanProxy Error"
            }
            updateWidget()
        }
    }

    private fun updateDisplay(totalSpend: Long?) {
        val safeTotal = totalSpend ?: 0
        val settings = LeanProxySettings.getInstance()
        val estimatedCost = (safeTotal / 1000.0) * settings.tokenCostPer1000
        displayText = if (!estimatedCost.isFinite()) {
            "$ LeanProxy N/A"
        } else {
            String.format("%s %.4f", settings.currencySymbol, estimatedCost)
        }
        updateWidget()
    }

    private fun updateWidget() {
        com.intellij.openapi.application.ApplicationManager.getApplication().invokeLater {
            val statusBar = WindowManager.getInstance().getStatusBar(project)
            statusBar?.updateWidget(ID())
        }
    }
}
