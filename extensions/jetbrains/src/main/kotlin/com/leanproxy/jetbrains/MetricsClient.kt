package com.leanproxy.jetbrains

import com.google.gson.Gson
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration

class MetricsClient(private val gson: Gson = Gson()) {
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(5))
        .build()

    /**
     * GETs the metrics endpoint, sending "Authorization: Bearer <token>" when
     * a token (the proxy's --metrics-token) is set.
     */
    fun fetch(endpoint: String, token: String? = null): Result<MetricsSnapshot> {
        return try {
            val builder = HttpRequest.newBuilder()
                .uri(URI.create(endpoint))
                .header("Accept", "application/json")
                .timeout(Duration.ofSeconds(5))
                .GET()
            if (!token.isNullOrEmpty()) {
                builder.header("Authorization", "Bearer $token")
            }

            val response = httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())

            if (response.statusCode() != 200) {
                return Result.failure(MetricsHttpException(response.statusCode()))
            }

            val snapshot = gson.fromJson(response.body(), MetricsSnapshot::class.java)
            Result.success(snapshot)
        } catch (e: java.io.IOException) {
            Result.failure(MetricsConnectionException(e.message ?: "Connection failed", e))
        } catch (e: Exception) {
            Result.failure(MetricsException("Unexpected error: ${e.message}", e))
        }
    }
}

open class MetricsException(message: String, cause: Throwable? = null) : Exception(message, cause)
class MetricsHttpException(val statusCode: Int) : MetricsException(describeStatus(statusCode))
class MetricsConnectionException(message: String, cause: Throwable?) : MetricsException(message, cause)

/** A human explanation of an HTTP error from the metrics endpoint. */
fun describeStatus(statusCode: Int): String = when (statusCode) {
    401 -> "HTTP 401: the metrics endpoint needs a token (Settings > Tools > LeanProxy Cost Monitor)"
    403 -> "HTTP 403: Host not allowed; use 127.0.0.1/localhost or add it with --metrics-allowed-hosts"
    404 -> "HTTP 404: no /metrics here; point the endpoint at --metrics-bind, not the dashboard port"
    else -> "HTTP $statusCode"
}
