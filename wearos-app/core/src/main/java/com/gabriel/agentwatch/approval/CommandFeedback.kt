package com.gabriel.agentwatch.approval

import com.gabriel.agentwatch.network.RelayError
import kotlinx.coroutines.TimeoutCancellationException
import java.io.IOException
import java.net.SocketTimeoutException

/** Where a command result is shown; only the wording of "changed" errors differs. */
enum class FeedbackSurface { SCREEN, NOTIFICATION }

/**
 * Client-neutral classification of relay and transport failures.
 *
 * @property refresh whether the failure implies local client state is stale and should be re-fetched.
 * @property clearsOptimisticPrompt whether the failure implies an in-flight optimistic answer or prompt
 * should be cleared because it cannot succeed.
 */
enum class RelayErrorKind(
    val refresh: Boolean,
    val clearsOptimisticPrompt: Boolean
) {
    PROMPT_CHANGED(refresh = true, clearsOptimisticPrompt = true),
    AGENT_NOT_BLOCKED(refresh = true, clearsOptimisticPrompt = true),
    AGENT_BLOCKED(refresh = true, clearsOptimisticPrompt = false),
    PANE_NOT_FOUND(refresh = true, clearsOptimisticPrompt = true),
    HOST_OFFLINE(refresh = false, clearsOptimisticPrompt = false),
    HERDR_UNAVAILABLE(refresh = false, clearsOptimisticPrompt = false),
    BRIDGE_BUSY(refresh = true, clearsOptimisticPrompt = false),
    NOT_ALLOWED(refresh = false, clearsOptimisticPrompt = false),
    TIMEOUT(refresh = true, clearsOptimisticPrompt = false),
    NETWORK(refresh = false, clearsOptimisticPrompt = false),
    UNAUTHORIZED(refresh = false, clearsOptimisticPrompt = true),
    OTHER(refresh = false, clearsOptimisticPrompt = false);
}

/**
 * Maps any throwable/failure into a client-neutral [RelayErrorKind].
 */
fun classifyRelayError(error: Throwable?): RelayErrorKind = when (error) {
    is RelayError -> when (error.code) {
        "stale_state", "prompt_changed", "unknown_option", "host_required" -> RelayErrorKind.PROMPT_CHANGED
        "agent_busy" -> RelayErrorKind.AGENT_NOT_BLOCKED
        "agent_blocked" -> RelayErrorKind.AGENT_BLOCKED
        "unknown_pane" -> RelayErrorKind.PANE_NOT_FOUND
        "host_offline" -> RelayErrorKind.HOST_OFFLINE
        "herdr_offline", "herdr_unavailable" -> RelayErrorKind.HERDR_UNAVAILABLE
        "bridge_busy", "rate_limited" -> RelayErrorKind.BRIDGE_BUSY
        "not_allowed" -> RelayErrorKind.NOT_ALLOWED
        "timeout" -> RelayErrorKind.TIMEOUT
        "unauthorized", "not_paired" -> RelayErrorKind.UNAUTHORIZED
        else -> when (error.httpStatus) {
            401 -> RelayErrorKind.UNAUTHORIZED
            403 -> RelayErrorKind.NOT_ALLOWED
            409 -> RelayErrorKind.PROMPT_CHANGED
            429 -> RelayErrorKind.BRIDGE_BUSY
            504, 524 -> RelayErrorKind.TIMEOUT
            in 500..599 -> RelayErrorKind.NETWORK
            else -> RelayErrorKind.OTHER
        }
    }
    is SocketTimeoutException, is TimeoutCancellationException -> RelayErrorKind.TIMEOUT
    is IOException -> RelayErrorKind.NETWORK
    else -> RelayErrorKind.OTHER
}

/**
 * What the wrist shows after a command (`answer`, `cancel`, `prompt`).
 *
 * @property refresh the watch's view is probably stale: re-fetch the snapshot.
 * @property needsPairing the device token is missing or rejected.
 */
data class CommandFeedback(
    val message: String,
    val isError: Boolean,
    val refresh: Boolean = false,
    val needsPairing: Boolean = false
) {
    companion object {
        fun success(message: String) = CommandFeedback(message, isError = false)
    }
}

/**
 * Maps a failed command to a short wrist message (contracts §2.4). Always an error.
 *
 * Decides on [RelayError.code] first and the HTTP status second; the relay's free-text
 * `message` (raw bridge text such as "state sequence mismatch: …") is never shown.
 */
fun commandErrorFeedback(error: Throwable, surface: FeedbackSurface = FeedbackSurface.SCREEN): CommandFeedback {
    fun changed(what: String) = CommandFeedback(
        message = when (surface) {
            FeedbackSurface.SCREEN -> "$what — refreshed"
            FeedbackSurface.NOTIFICATION -> "$what — open the app"
        },
        isError = true,
        refresh = true
    )
    fun problem(message: String, refresh: Boolean = false, needsPairing: Boolean = false) =
        CommandFeedback(message, isError = true, refresh = refresh, needsPairing = needsPairing)

    // The relay has no separate code for it; the bridge's message says to answer on the Mac (contracts §2.4).
    fun RelayError.isFocusRefusal() = message.contains("on the Mac", ignoreCase = true)

    val sessionExpired = problem("Session expired — pair again", needsPairing = true)
    val relayTimedOut = problem("Relay timed out", refresh = true) // the command may still have run
    val somethingWrong = problem("Something went wrong")

    return when (error) {
        is RelayError -> when (error.code) {
            "stale_state" -> changed("Agent changed")
            // OpenCode focus guard: nothing was pressed and the prompt is unchanged, so no refresh or retry.
            "prompt_changed" -> if (error.isFocusRefusal()) problem("Answer on the device") else changed("Prompt changed")
            "unknown_option" -> changed("Prompt changed")
            "unknown_pane" -> problem("Agent closed", refresh = true)
            // An old command path while several hosts have the pane (contracts §2.4): a refresh brings each agent's host.
            "host_required" -> changed("Several devices")
            "agent_busy" -> problem("Agent is busy", refresh = true)
            "agent_blocked" -> problem("Answer the question first", refresh = true)
            "agent_state_unknown" -> problem("Agent state unknown", refresh = true)
            "unauthorized" -> sessionExpired
            "not_paired" -> problem("Not paired — pair again", needsPairing = true)
            "host_offline" -> problem("Device offline")
            "herdr_offline", "herdr_unavailable" -> problem("herdr stopped")
            "not_allowed" -> problem("Not allowed")
            "timeout" -> problem("No answer from device", refresh = true) // the device may still have acted
            "rate_limited" -> problem("Try again later")
            "pair_code_invalid" -> problem("Invalid code")
            "invalid_request", "internal", "empty_response" -> somethingWrong
            // No ErrorResponse body (proxy/CDN page) or a code this build does not know.
            else -> when (error.httpStatus) {
                401 -> sessionExpired
                403 -> problem("Not allowed")
                409 -> changed("Agent changed")
                429 -> problem("Try again later")
                504, 524 -> relayTimedOut
                in 500..599 -> problem("Relay unreachable")
                else -> somethingWrong
            }
        }
        is SocketTimeoutException, is TimeoutCancellationException -> relayTimedOut
        is IOException -> problem("Can't reach the relay")
        else -> somethingWrong
    }
}

/** Alias for [commandErrorFeedback] supporting nullable [error]. */
fun commandFailure(error: Throwable?, surface: FeedbackSurface = FeedbackSurface.SCREEN): CommandFeedback =
    commandErrorFeedback(error ?: RuntimeException("unknown failure"), surface)
