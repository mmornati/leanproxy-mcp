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

    fun fetch(endpoint: String): Result<MetricsSnapshot> {
        return try {
            val request = HttpRequest.newBuilder()
                .uri(URI.create(endpoint))
                .header("Accept", "application/json")
                .timeout(Duration.ofSeconds(5))
                .GET()
                .build()

            val response = httpClient.send(request, HttpResponse.BodyHandlers.ofString())

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
class MetricsHttpException(val statusCode: Int) : MetricsException("HTTP $statusCode")
class MetricsConnectionException(message: String, cause: Throwable?) : MetricsException(message, cause)
