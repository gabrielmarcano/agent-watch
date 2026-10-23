package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.mutableStateMapOf
import com.gabriel.agentwatch.mobile.MobileApplication
import com.gabriel.agentwatch.model.AgentKey

/**
 * Messages typed in a chat that the relay has not published as a turn yet.
 * Thin compatibility facade over [MobileChatState].
 */
object ChatOutbox {
    private val fallbackSent = mutableStateMapOf<String, List<String>>()

    private fun state(): MobileChatState? =
        runCatching { MobileApplication.get().chatState }.getOrNull()

    fun pending(key: AgentKey): List<String> {
        val msgs = state()?.state?.value?.outbox?.get(key.token)
        return msgs?.map { it.text } ?: fallbackSent[key.token].orEmpty()
    }

    fun add(key: AgentKey, text: String) {
        if (text.isBlank()) return
        val chatState = state()
        if (chatState != null) {
            chatState.addOutbox(key, text)
        } else {
            fallbackSent[key.token] = pending(key) + text
        }
    }

    fun retain(key: AgentKey, texts: List<String>) {
        fallbackSent.remove(key.token)
    }

    fun clear() {
        fallbackSent.clear()
        state()?.clearAll()
    }
}
