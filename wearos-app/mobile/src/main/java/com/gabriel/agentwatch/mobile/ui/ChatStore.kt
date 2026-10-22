package com.gabriel.agentwatch.mobile.ui

import android.content.Context

/**
 * What the owner did with each chat on this phone: muted, pinned, hidden.
 *
 * All of it is local, a reading habit like the seen marks, not relay state: the relay knows nothing
 * about a chat being muted or pinned, and a chat that is hidden comes back with the next snapshot.
 *
 * A muted chat posts no notification (the phone's own, local one). The watch keeps its own push:
 * the relay does not know about this, and the wrist is the owner's choice too.
 */
class ChatStore(context: Context) {
    private val prefs = context.applicationContext
        .getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** Whether the chat with this [token] (the agent key's token) is muted. */
    fun isMuted(token: String): Boolean = prefs.getBoolean(MUTED + token, false)

    /** Whether the chat is pinned to the top of the list. */
    fun isPinned(token: String): Boolean = prefs.getBoolean(PINNED + token, false)

    /** Whether the chat is hidden from the list (WhatsApp's "delete": the agent still runs). */
    fun isHidden(token: String): Boolean = prefs.getBoolean(HIDDEN + token, false)

    /** Mute a chat, remembering its name for the settings list. */
    fun mute(token: String, label: String) {
        prefs.edit().putBoolean(MUTED + token, true).putString(LABEL + token, label).apply()
    }

    /** Unmute a chat. */
    fun unmute(token: String) {
        prefs.edit().remove(MUTED + token).apply()
    }

    /** Pin a chat to the top of the list. */
    fun pin(token: String, label: String) {
        prefs.edit().putBoolean(PINNED + token, true).putString(LABEL + token, label).apply()
    }

    /** Unpin a chat. */
    fun unpin(token: String) {
        prefs.edit().remove(PINNED + token).apply()
    }

    /** Hide a chat from the list. */
    fun hide(token: String, label: String) {
        prefs.edit().putBoolean(HIDDEN + token, true).putString(LABEL + token, label).apply()
    }

    /** Bring a hidden chat back. */
    fun unhide(token: String) {
        prefs.edit().remove(HIDDEN + token).apply()
    }

    /** Every muted chat, by token. */
    fun muted(): List<String> = tokens(MUTED)

    /** Every pinned chat, by token. */
    fun pinned(): List<String> = tokens(PINNED)

    /** Every hidden chat, by token. */
    fun hidden(): List<String> = tokens(HIDDEN)

    /** How many chats are muted. */
    fun mutedCount(): Int = muted().size

    /** The name the chat had when it was last changed. */
    fun label(token: String): String = prefs.getString(LABEL + token, token).orEmpty()

    private fun tokens(prefix: String): List<String> = prefs.all.keys
        .filter { it.startsWith(prefix) }
        .map { it.removePrefix(prefix) }
        .sorted()

    private companion object {
        const val PREFS = "AgentWatchChatFlags"
        const val MUTED = "muted:"
        const val PINNED = "pinned:"
        const val HIDDEN = "hidden:"
        const val LABEL = "label:"
    }
}
