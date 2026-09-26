package com.gabriel.agentwatch.approval

import com.gabriel.agentwatch.approval.FeedbackSurface.NOTIFICATION
import com.gabriel.agentwatch.approval.FeedbackSurface.SCREEN
import com.gabriel.agentwatch.network.RelayError
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.IOException
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.UnknownHostException

class CommandFeedbackTest {

    /** Raw text the bridge puts in ErrorBody.message; it must never reach the wrist. */
    private val raw = "state sequence mismatch: expected 334, got 336"

    private fun relay(code: String, status: Int) = RelayError(code, raw, status)

    private fun assertFeedback(
        error: Throwable,
        message: String,
        refresh: Boolean,
        needsPairing: Boolean = false,
        surface: FeedbackSurface = SCREEN
    ) {
        val fb = commandErrorFeedback(error, surface)
        assertEquals("message for $error", message, fb.message)
        assertTrue("$error must be shown as an error", fb.isError)
        assertEquals("refresh for $error", refresh, fb.refresh)
        assertEquals("needsPairing for $error", needsPairing, fb.needsPairing)
    }

    @Test
    fun staleStateIsARefreshedErrorNotRawBridgeText() {
        assertFeedback(relay("stale_state", 409), "Agent changed — refreshed", refresh = true)
    }

    @Test
    fun promptChangedAndUnknownOptionSayPromptChanged() {
        assertFeedback(relay("prompt_changed", 409), "Prompt changed — refreshed", refresh = true)
        assertFeedback(relay("unknown_option", 409), "Prompt changed — refreshed", refresh = true)
    }

    @Test
    fun aFocusRefusalSendsTheUserToTheMacWithoutARetry() {
        // OpenCode: the bridge pressed nothing because the Mac's focus moved; the prompt itself is unchanged.
        val focus = RelayError(
            "prompt_changed",
            "focus may have moved on the Mac; answer it on the Mac (focused button is \"Reject\")",
            409
        )
        assertFeedback(focus, "Answer on the device", refresh = false)
        assertFeedback(focus, "Answer on the device", refresh = false, surface = NOTIFICATION)
    }

    @Test
    fun unknownPaneSaysAgentClosedAndRefreshes() {
        assertFeedback(relay("unknown_pane", 404), "Agent closed", refresh = true)
    }

    @Test
    fun agentStateConflictsRefresh() {
        assertFeedback(relay("agent_busy", 409), "Agent is busy", refresh = true)
        assertFeedback(relay("agent_blocked", 409), "Answer the question first", refresh = true)
        assertFeedback(relay("agent_state_unknown", 409), "Agent state unknown", refresh = true)
    }

    @Test
    fun unauthorizedAsksToPairAgain() {
        assertFeedback(relay("unauthorized", 401), "Session expired — pair again", refresh = false, needsPairing = true)
        // Non-JSON 401 (e.g. from a proxy) is mapped by HTTP status.
        assertFeedback(relay("http_401", 401), "Session expired — pair again", refresh = false, needsPairing = true)
        // Repository has no client because the watch is not paired.
        assertFeedback(RelayError("not_paired", "Client not configured", 0), "Not paired — pair again", refresh = false, needsPairing = true)
    }

    @Test
    fun hostSideOutagesUseContractWording() {
        assertFeedback(relay("host_offline", 503), "Device offline", refresh = false)
        assertFeedback(relay("herdr_offline", 503), "herdr stopped", refresh = false)
        // The Mac may or may not have acted: refresh to learn the outcome.
        assertFeedback(relay("timeout", 504), "No answer from device", refresh = true)
    }

    @Test
    fun otherRelayCodes() {
        assertFeedback(relay("rate_limited", 429), "Try again later", refresh = false)
        assertFeedback(relay("invalid_request", 400), "Something went wrong", refresh = false)
        assertFeedback(relay("internal", 500), "Something went wrong", refresh = false)
        assertFeedback(relay("some_future_code", 418), "Something went wrong", refresh = false)
    }

    @Test
    fun nonJsonHttpErrorsAreMappedByStatus() {
        assertFeedback(relay("http_409", 409), "Agent changed — refreshed", refresh = true)
        assertFeedback(relay("http_502", 502), "Relay unreachable", refresh = false)
        assertFeedback(relay("http_504", 504), "Relay timed out", refresh = true)
        assertFeedback(relay("http_524", 524), "Relay timed out", refresh = true)
    }

    @Test
    fun networkFailures() {
        assertFeedback(IOException("boom"), "Can't reach the relay", refresh = false)
        assertFeedback(UnknownHostException("relay.example"), "Can't reach the relay", refresh = false)
        assertFeedback(ConnectException("refused"), "Can't reach the relay", refresh = false)
        assertFeedback(SocketTimeoutException("read timed out"), "Relay timed out", refresh = true)
        assertFeedback(IllegalStateException("weird"), "Something went wrong", refresh = false)
    }

    @Test
    fun coroutineTimeoutSaysRelayTimedOut() {
        val timeout = try {
            runBlocking { withTimeout(1) { delay(1_000) } }
            fail("withTimeout should have thrown")
            return
        } catch (e: Exception) {
            e
        }
        assertFeedback(timeout, "Relay timed out", refresh = true)
    }

    @Test
    fun notificationWordingPointsToTheApp() {
        assertFeedback(relay("stale_state", 409), "Agent changed — open the app", refresh = true, surface = NOTIFICATION)
        assertFeedback(relay("prompt_changed", 409), "Prompt changed — open the app", refresh = true, surface = NOTIFICATION)
        // Not every failure is "Could not reach the relay".
        assertFeedback(relay("host_offline", 503), "Device offline", refresh = false, surface = NOTIFICATION)
        assertFeedback(relay("agent_busy", 409), "Agent is busy", refresh = true, surface = NOTIFICATION)
    }

    @Test
    fun everyErrorIsRedWristSizedAndHidesRawText() {
        val codes = listOf(
            "invalid_request" to 400, "unauthorized" to 401, "unknown_pane" to 404, "stale_state" to 409,
            "prompt_changed" to 409, "agent_busy" to 409, "agent_blocked" to 409, "agent_state_unknown" to 409,
            "unknown_option" to 409, "pair_code_invalid" to 403, "rate_limited" to 429, "host_offline" to 503,
            "herdr_offline" to 503, "timeout" to 504, "internal" to 500, "http_500" to 500, "not_paired" to 0
        )
        for ((code, status) in codes) {
            for (surface in FeedbackSurface.values()) {
                val fb = commandErrorFeedback(relay(code, status), surface)
                assertTrue("$code/$surface must be an error", fb.isError)
                assertTrue("$code/$surface too long for the wrist: '${fb.message}'", fb.message.length <= 30)
                assertFalse("$code/$surface leaks raw bridge text", fb.message.contains("mismatch"))
                assertFalse("$code/$surface leaks the HTTP status", fb.message.contains("$status") && status != 0)
            }
        }
    }

    @Test
    fun successIsNotAnError() {
        val fb = CommandFeedback.success("Sent answer")
        assertFalse(fb.isError)
        assertFalse(fb.refresh)
        assertEquals("Sent answer", fb.message)
    }
}
