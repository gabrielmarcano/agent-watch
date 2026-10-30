package com.gabriel.agentwatch.ui.components

import android.app.RemoteInput
import android.content.Intent
import android.speech.RecognizerIntent
import android.util.Log
import android.view.inputmethod.EditorInfo
import androidx.wear.input.RemoteInputIntentHelper
import androidx.wear.input.wearableExtender
import com.gabriel.agentwatch.ui.logic.InputResult
import com.gabriel.agentwatch.ui.logic.inputResult

/**
 * A prompt for an agent through Wear's system input, where the user picks voice, keyboard or
 * handwriting; [intent]'s title names the target ("To: api"). Read it with [result], which never
 * drops text.
 */
object PromptInput {
    private const val KEY = "prompt"
    private const val TAG = "PromptInput"

    fun intent(title: String): Intent {
        val input = RemoteInput.Builder(KEY)
            .setLabel(title)
            .wearableExtender {
                setEmojisAllowed(false)
                setInputActionType(EditorInfo.IME_ACTION_SEND)
            }
            .build()
        return RemoteInputIntentHelper.createActionRemoteInputIntent().also {
            RemoteInputIntentHelper.putRemoteInputsExtra(it, listOf(input))
            RemoteInputIntentHelper.putTitleExtra(it, title)
        }
    }

    /** Logs the result code and the extra keys only, never the text (AGENTS.md §3). */
    fun result(resultCode: Int, data: Intent?): InputResult {
        val result = inputResult(
            resultCode = resultCode,
            remoteInputText = data?.let { RemoteInput.getResultsFromIntent(it)?.getCharSequence(KEY) },
            recognizerResults = data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS),
            extraText = data?.getCharSequenceExtra(Intent.EXTRA_TEXT)
        )
        Log.d(
            TAG,
            "code=$resultCode keys=${data?.extras?.keySet()} clipData=${data?.clipData != null} " +
                "-> ${result.javaClass.simpleName}"
        )
        return result
    }
}

/** Wear's system text input (keyboard, handwriting or voice) for one value, such as the relay URL. */
object TextInput {
    const val KEY = "value"

    fun intent(label: String): Intent {
        val input = RemoteInput.Builder(KEY)
            .setLabel(label)
            .wearableExtender {
                setEmojisAllowed(false)
                setInputActionType(EditorInfo.IME_ACTION_DONE)
            }
            .build()
        return RemoteInputIntentHelper.createActionRemoteInputIntent().also {
            RemoteInputIntentHelper.putRemoteInputsExtra(it, listOf(input))
        }
    }

    /** The text entered, or null when the user backed out. */
    fun result(data: Intent?): String? =
        data?.let { RemoteInput.getResultsFromIntent(it)?.getCharSequence(KEY)?.toString() }
}
