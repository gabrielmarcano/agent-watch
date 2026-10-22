package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.mutableStateMapOf
import com.gabriel.agentwatch.model.AgentKey

/**
 * Messages typed in a chat that the relay has not published as a turn yet, so the conversation shows
 * them at once (the phone's optimistic "sent" bubbles). One process-wide store, keyed by agent: the
 * chat screen clears each message once its turn arrives with the same query.
 *
 * Nothing here is authoritative: a restart loses it and the relay's history is what remains.
 */
object ChatOutbox {
    private val sent = mutableStateMapOf<String, List<String>>()

    fun pending(key: AgentKey): List<String> = sent[key.token].orEmpty()

    /** The relay accepted [text] for [key]; show it until the turn arrives. */
    fun add(key: AgentKey, text: String) {
        if (text.isBlank()) return
        sent[key.token] = pending(key) + text
    }

    /** Keep only [texts] (the messages still without a turn); a chat with none is forgotten. */
    fun retain(key: AgentKey, texts: List<String>) {
        if (texts.isEmpty()) sent.remove(key.token) else sent[key.token] = texts
    }

    /** A new pairing: none of the old devices' pending messages belong to it. */
    fun clear() = sent.clear()
}
