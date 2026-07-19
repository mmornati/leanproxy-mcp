package com.leanproxy.jetbrains

data class ToolMetric(
    val tool_name: String? = null,
    val token_count: Long? = null
)

data class ServerMetric(
    val server_name: String? = null,
    val token_count: Long? = null
)

data class MetricsSnapshot(
    val by_tool: List<ToolMetric>? = null,
    val by_server: List<ServerMetric>? = null,
    val total_spend: Long? = null,
    val top_5_expensive_tools: List<ToolMetric>? = null
)
