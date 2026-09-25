package com.leanproxy.jetbrains

// The part of LeanProxy's /metrics response the plugin reads. Every field is
// nullable: Gson fills what the proxy sends and leaves the rest null.

data class UsageTool(
    val server: String? = null,
    val tool: String? = null,
    val calls: Long? = null,
    val original_tokens: Long? = null,
    val returned_tokens: Long? = null,
    val saved_tokens: Long? = null
) {
    /** "server.tool", or just the tool when it has no server. */
    fun displayName(): String {
        val t = tool ?: "unknown"
        return if (server.isNullOrEmpty()) t else "$server.$t"
    }
}

data class UsageServer(
    val server: String? = null,
    val tools: Int? = null,
    val calls: Long? = null,
    val original_tokens: Long? = null,
    val returned_tokens: Long? = null,
    val saved_tokens: Long? = null
)

/** Tokens recorded since `since` (today, or week-to-date). */
data class UsageWindow(
    val since: String? = null,
    val sessions: Int? = null,
    val original_tokens: Long? = null,
    val saved_tokens: Long? = null,
    val saved_percent: Double? = null,
    val discovery_calls: Long? = null,
    val discovery_tokens: Long? = null,
    val tool_calls: Long? = null,
    val top_server: String? = null,
    val top_tool: String? = null,
    val by_server: List<UsageServer>? = null,
    val by_tool: List<UsageTool>? = null
)

data class UsageSummary(
    val estimator: String? = null,
    val today: UsageWindow? = null,
    val week: UsageWindow? = null
)

/** `usage` is null when the proxy could not read its usage store. */
data class MetricsSnapshot(
    val usage: UsageSummary? = null
)

/**
 * The estimated cost of `tokens` at `costPer1000`, or null when no price is
 * configured (LeanProxy has no built-in price table).
 */
fun estimatedCost(tokens: Long, costPer1000: Double): Double? {
    if (costPer1000 <= 0.0 || !costPer1000.isFinite()) return null
    val cost = (tokens / 1000.0) * costPer1000
    return if (cost.isFinite()) cost else null
}

/** 1234 -> "1.2K", 2500000 -> "2.5M". */
fun formatTokens(n: Long): String = when {
    n >= 1_000_000 -> String.format("%.1fM", n / 1_000_000.0)
    n >= 1_000 -> String.format("%.1fK", n / 1_000.0)
    else -> n.toString()
}
