package com.leanproxy.jetbrains

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class ExceptionHierarchyTest {

    @Test
    fun `test metrics exception hierarchy`() {
        val connEx = MetricsConnectionException("connection refused", null)
        val httpEx = MetricsHttpException(500)
        val genericEx = MetricsException("something went wrong")

        assertTrue(connEx is MetricsException)
        assertTrue(httpEx is MetricsException)
        assertTrue(genericEx is MetricsException)
    }

    @Test
    fun `test metrics http exception status code`() {
        val ex = MetricsHttpException(404)
        assertEquals(404, ex.statusCode)
        assertTrue(ex.message?.contains("404") == true || ex.message?.contains("HTTP") == true)
    }

    @Test
    fun `test metrics connection exception message`() {
        val ex = MetricsConnectionException("Connection refused", java.io.IOException("refused"))
        assertTrue(ex.message?.contains("Connection refused") == true)
        assertNotNull(ex.cause)
        assertTrue(ex.cause is java.io.IOException)
    }
}
