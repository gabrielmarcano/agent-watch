package com.gabriel.agentwatch.data

import com.google.gson.Gson
import com.google.gson.reflect.TypeToken

/** A prompt accepted locally while the relay was known to be offline. */
data class QueuedPrompt(
    val paneId: String,
    val text: String,
    val expectedSeq: Long
)

interface PromptQueueStore {
    var queuedPrompts: List<QueuedPrompt>
}

private val queuedPromptListType = object : TypeToken<List<QueuedPrompt>>() {}.type

fun encodeQueuedPrompts(prompts: List<QueuedPrompt>): String = Gson().toJson(prompts)

fun decodeQueuedPrompts(encoded: String?): List<QueuedPrompt> = try {
    if (encoded.isNullOrBlank()) emptyList() else Gson().fromJson(encoded, queuedPromptListType) ?: emptyList()
} catch (_: RuntimeException) {
    emptyList()
}
