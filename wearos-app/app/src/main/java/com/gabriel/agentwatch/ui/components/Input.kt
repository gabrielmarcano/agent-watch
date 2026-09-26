package com.gabriel.agentwatch.ui.components

import android.app.RemoteInput
import android.content.Intent
import android.speech.RecognizerIntent
import android.view.inputmethod.EditorInfo
import androidx.wear.input.RemoteInputIntentHelper
import androidx.wear.input.wearableExtender

/** Voice input for a prompt; [prompt] names the target ("To: api") before the user speaks. */
object RecognizerIntentFactory {
    fun freeForm(prompt: String): Intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
        putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        putExtra(RecognizerIntent.EXTRA_PROMPT, prompt)
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
