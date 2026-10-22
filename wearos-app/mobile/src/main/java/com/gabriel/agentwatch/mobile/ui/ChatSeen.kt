package com.gabriel.agentwatch.mobile.ui

import android.content.Context
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.key

/**
 * The last `state_change_seq` the user saw for each chat, so the list can mark what is new since they
 * last opened it (WhatsApp's unread dot). Stored locally: it is a reading habit, not relay state.
 *
 * [prime] records the current sequence for agents the phone has never listed, so a fresh install (or a
 * newly started agent) does not mark every chat unread; after that, any change counts until the chat is
 * opened again.
 */
class ChatSeen(context: Context) {
    private val prefs = context.applicationContext
        .getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** The sequence [key] was last opened at, or null when no chat was opened or primed for it yet. */
    fun seen(key: AgentKey): Long? = prefs.getLong(KEY_PREFIX + key.token, -1L).takeIf { it >= 0 }

    /**
     * When the chat was last read, in epoch millis: the line the unread count counts from. Null when
     * the chat was never opened (the list primes it, so it starts read).
     */
    fun seenAt(key: AgentKey): Long? = prefs.getLong(AT_PREFIX + key.token, -1L).takeIf { it >= 0 }

    /** The chat was opened (or changed while open): everything up to [seq] is read. Monotonic. */
    fun markSeen(key: AgentKey, seq: Long) {
        if (seq < 0) return
        val current = prefs.getLong(KEY_PREFIX + key.token, -1L)
        if (seq > current) prefs.edit().putLong(KEY_PREFIX + key.token, seq).apply()
        // The moment the user read it: the unread badge counts the turns after it.
        prefs.edit().putLong(AT_PREFIX + key.token, System.currentTimeMillis()).apply()
    }

    /**
     * Mark a chat read without opening it (the swipe on the row): the turns up to now count as seen,
     * and the count starts again from here.
     */
    fun markRead(key: AgentKey, seq: Long) = markSeen(key, seq)

    /**
     * Mark a chat unread again (WhatsApp's "Mark as unread"): every turn counts until it is read.
     */
    fun markUnread(key: AgentKey) {
        prefs.edit()
            .putLong(AT_PREFIX + key.token, 0L)
            .remove(KEY_PREFIX + key.token)
            .apply()
    }

    /** Baseline for agents the list shows for the first time; agents already known keep their sequence. */
    fun prime(agents: List<AgentState>) {
        val editor = prefs.edit()
        var changed = false
        agents.forEach { agent ->
            val key = KEY_PREFIX + agent.key.token
            if (!prefs.contains(key)) {
                editor.putLong(key, agent.state_change_seq.coerceAtLeast(0))
                changed = true
            }
        }
        if (changed) editor.apply()
    }

    private companion object {
        const val PREFS = "AgentWatchChatSeen"
        const val KEY_PREFIX = "seen:"
        const val AT_PREFIX = "seenAt:"
    }
}
