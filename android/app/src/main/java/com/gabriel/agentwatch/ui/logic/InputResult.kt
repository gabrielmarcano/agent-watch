package com.gabriel.agentwatch.ui.logic

import android.app.Activity
import com.gabriel.agentwatch.R

/** What came back from the system text input (voice, keyboard or handwriting). */
sealed interface InputResult {
    data class Text(val text: String) : InputResult
    /** The user backed out: nothing to say. */
    data object Canceled : InputResult
    /** RESULT_OK with no usable text. */
    data object Empty : InputResult
    /** A result code the input never documents, without text. */
    data class Unexpected(val resultCode: Int) : InputResult
}

/**
 * Reads an input's result without ever dropping text: the RemoteInput value first, then the
 * recognizer's `EXTRA_RESULTS`, then a plain `EXTRA_TEXT`. Text counts whatever the result code
 * (it only reaches the confirm screen, never the agent directly); without text, only a back-out
 * (RESULT_CANCELED) stays silent.
 */
fun inputResult(
    resultCode: Int,
    remoteInputText: CharSequence?,
    recognizerResults: List<String?>?,
    extraText: CharSequence?
): InputResult {
    val text = sequenceOf(remoteInputText?.toString(), recognizerResults?.firstOrNull { !it.isNullOrBlank() }, extraText?.toString())
        .firstOrNull { !it.isNullOrBlank() }
        ?.trim()
    return when {
        text != null -> InputResult.Text(text)
        resultCode == Activity.RESULT_CANCELED -> InputResult.Canceled
        resultCode == Activity.RESULT_OK -> InputResult.Empty
        else -> InputResult.Unexpected(resultCode)
    }
}

/** The line to show for a result that carries no text; null when nothing should be shown. */
fun inputFeedback(result: InputResult): Int? = when (result) {
    is InputResult.Text, InputResult.Canceled -> null
    InputResult.Empty -> R.string.input_empty
    is InputResult.Unexpected -> R.string.input_failed
}
