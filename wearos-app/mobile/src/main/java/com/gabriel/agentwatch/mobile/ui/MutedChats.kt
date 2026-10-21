package com.gabriel.agentwatch.mobile.ui

import android.content.Context

/**
 * The chats the owner muted: a local reading habit, like the seen marks, not relay state.
 *
 * A muted chat posts no notification (the phone's own, local one). The watch keeps its own push: the
 * relay does not know about this, and the wrist is the owner's choice too.
 */
class MutedChats(context: Context) {
    private val app = context.applicationContext
    private val muted = app.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
    private val labels = app.getSharedPreferences(LABELS, Context.MODE_PRIVATE)

    /** Whether the chat with this [token] (the agent key's token) is muted. */
    fun isMuted(token: String): Boolean = muted.getBoolean(token, false)

    /** Mute a chat, remembering its name for the settings list. */
    fun mute(token: String, label: String) {
        muted.edit().putBoolean(token, true).apply()
        labels.edit().putString(token, label).apply()
    }

    /** Unmute a chat. */
    fun unmute(token: String) {
        muted.edit().remove(token).apply()
        labels.edit().remove(token).apply()
    }

    /** Every muted chat, by token. */
    fun all(): List<String> = muted.all.keys.sorted()

    /** How many chats are muted. */
    fun count(): Int = muted.all.size

    /** The name the chat had when it was muted. */
    fun label(token: String): String = labels.getString(token, token).orEmpty()

    private companion object {
        const val PREFS = "AgentWatchMuted"
        const val LABELS = "AgentWatchMutedLabels"
    }
}
