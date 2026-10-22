package com.gabriel.agentwatch.mobile.ui.logic

/** The pairing code as the screen keeps it: digits only, at most [CODE_LENGTH]. */
fun cleanPairCode(input: String): String = input.filter(Char::isDigit).take(CODE_LENGTH)

const val CODE_LENGTH = 6

/** True when [code] can be sent to `POST /v1/pair`. */
fun pairCodeComplete(code: String): Boolean = code.length == CODE_LENGTH

/**
 * The reply to send for [text], or null when nothing should be sent: never drop input silently
 * (wearos-app/ARCHITECTURE.md §4b), the caller shows "Empty reply — nothing sent".
 */
fun replyToSend(text: String): String? = text.trim().ifBlank { null }

/** The device name the relay stores for this pairing. */
fun pairDeviceName(manufacturer: String, model: String): String =
    "${manufacturer.replaceFirstChar { it.uppercase() }} $model".trim()
