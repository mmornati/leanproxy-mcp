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
    private var tooltip: String = "LeanProxy unavailable"
    private var scheduledIntervalMs: Long = 0

    override fun ID(): String = "LeanProxyStatusBar"

    override fun getPresentation(): StatusBarWidget.TextPresentation = this

    override fun getText(): String = displayText

    override fun getAlignment(): Float = JLabel.LEFT

    override fun getTooltipText(): String? = tooltip

    override fun getIcon(): javax.swing.Icon? = null

    override fun getClickConsumer(): StatusBarWidget.ClickConsumer? {
        return StatusBarWidget.ClickConsumer {
            val toolWindowManager = ToolWindowManager.getInstance(project)
            toolWindowManager.getToolWindow("LeanProxy")?.show()
        }
    }

    fun start() {
        poll()
        schedule()
    }

    private fun schedule() {
        pollFuture?.cancel(false)
        scheduledIntervalMs = LeanProxySettings.getInstance().effectivePollIntervalMs()
        pollFuture = AppExecutorUtil.getAppScheduledExecutorService()
            .scheduleWithFixedDelay({ poll() }, scheduledIntervalMs, scheduledIntervalMs, TimeUnit.MILLISECONDS)
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
        // Pick up a poll interval changed in the settings.
        if (pollFuture != null && settings.effectivePollIntervalMs() != scheduledIntervalMs) {
            schedule()
        }
        val result = metricsClient.fetch(settings.metricsEndpoint, LeanProxySettings.metricsToken())
        result.onSuccess { snapshot ->
            updateDisplay(snapshot.usage, settings)
        }.onFailure { error ->
            displayText = when (error) {
                is MetricsConnectionException -> "\u26A0 LeanProxy"
                is MetricsHttpException -> "\u26A0 HTTP ${error.statusCode}"
                else -> "\u26A0 LeanProxy Error"
            }
            tooltip = "LeanProxy unavailable \u2014 ${error.message}"
            updateWidget()
        }
    }

    private fun updateDisplay(usage: UsageSummary?, settings: LeanProxySettings) {
        if (usage == null) {
            displayText = "LeanProxy: no usage data"
            tooltip = "LeanProxy is running but could not read its usage store"
            updateWidget()
            return
        }
        val savedToday = usage.today?.saved_tokens ?: 0
        val savedWeek = usage.week?.saved_tokens ?: 0
        val cost = estimatedCost(savedToday, settings.tokenCostPer1000)
        displayText = if (cost == null) {
            "LeanProxy: ${formatTokens(savedToday)} saved"
        } else {
            String.format("LeanProxy: %s%.4f saved", settings.currencySymbol, cost)
        }
        tooltip = String.format(
            "LeanProxy: %,d tokens saved today (%.1f%%), %,d this week \u2014 Click for details",
            savedToday, usage.today?.saved_percent ?: 0.0, savedWeek
        )
        updateWidget()
    }

    private fun updateWidget() {
        com.intellij.openapi.application.ApplicationManager.getApplication().invokeLater {
            val statusBar = WindowManager.getInstance().getStatusBar(project)
            statusBar?.updateWidget(ID())
        }
    }
}
