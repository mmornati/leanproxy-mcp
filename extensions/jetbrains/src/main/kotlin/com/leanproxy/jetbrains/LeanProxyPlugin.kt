package com.leanproxy.jetbrains

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.wm.ToolWindowManager
import com.intellij.openapi.wm.WindowManager

class OpenCostPanelAction : AnAction(), DumbAware {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val toolWindowManager = ToolWindowManager.getInstance(project)
        val toolWindow = toolWindowManager.getToolWindow("LeanProxy")
        if (toolWindow != null) {
            toolWindow.show()
        }
    }
}

class RefreshStatusBarAction : AnAction(), DumbAware {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val statusBar = WindowManager.getInstance().getStatusBar(project)
        val widget = statusBar?.getWidget("LeanProxyStatusBar") as? LeanProxyStatusBarWidget
        widget?.refresh()
    }
}
