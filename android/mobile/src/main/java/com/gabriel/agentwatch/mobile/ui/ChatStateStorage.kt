package com.gabriel.agentwatch.mobile.ui

import android.content.Context
import android.content.SharedPreferences

interface ChatStateStorage {
    fun loadPinned(): Set<String>
    fun savePinned(tokens: Set<String>)
    fun loadMuted(): Set<String>
    fun saveMuted(tokens: Set<String>)
    fun loadHidden(): Set<String>
    fun saveHidden(tokens: Set<String>)
    fun loadLabels(): Map<String, String>
    fun saveLabel(token: String, label: String)
    fun loadSeen(): Map<String, SeenMark>
    fun saveSeen(token: String, mark: SeenMark)
    fun clearAll()
}

class SharedPrefsChatStateStorage(
    private val flagsPrefs: SharedPreferences,
    private val seenPrefs: SharedPreferences
) : ChatStateStorage {

    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(FLAGS_PREFS, Context.MODE_PRIVATE),
        context.applicationContext.getSharedPreferences(SEEN_PREFS, Context.MODE_PRIVATE)
    )

    override fun loadPinned(): Set<String> = loadPrefixTokens(flagsPrefs, PREFIX_PINNED, KEY_PINNED_SET)

    override fun savePinned(tokens: Set<String>) {
        savePrefixTokens(flagsPrefs, PREFIX_PINNED, KEY_PINNED_SET, tokens)
    }

    override fun loadMuted(): Set<String> = loadPrefixTokens(flagsPrefs, PREFIX_MUTED, KEY_MUTED_SET)

    override fun saveMuted(tokens: Set<String>) {
        savePrefixTokens(flagsPrefs, PREFIX_MUTED, KEY_MUTED_SET, tokens)
    }

    override fun loadHidden(): Set<String> = loadPrefixTokens(flagsPrefs, PREFIX_HIDDEN, KEY_HIDDEN_SET)

    override fun saveHidden(tokens: Set<String>) {
        savePrefixTokens(flagsPrefs, PREFIX_HIDDEN, KEY_HIDDEN_SET, tokens)
    }

    override fun loadLabels(): Map<String, String> {
        val result = mutableMapOf<String, String>()
        for ((key, value) in flagsPrefs.all) {
            if (key.startsWith(PREFIX_LABEL) && value is String) {
                result[key.removePrefix(PREFIX_LABEL)] = value
            }
        }
        return result
    }

    override fun saveLabel(token: String, label: String) {
        flagsPrefs.edit().putString(PREFIX_LABEL + token, label).apply()
    }

    override fun loadSeen(): Map<String, SeenMark> {
        val result = mutableMapOf<String, SeenMark>()
        val all = seenPrefs.all

        // Read all seen marks (supports both seen: / seenAt: and seq_ / at_ keys)
        for ((key, value) in all) {
            val token = when {
                key.startsWith(PREFIX_SEEN) -> key.removePrefix(PREFIX_SEEN)
                key.startsWith(PREFIX_SEQ) -> key.removePrefix(PREFIX_SEQ)
                else -> null
            } ?: continue

            if (result.containsKey(token)) continue
            val seq = (value as? Number)?.toLong() ?: continue
            val at = (all[PREFIX_SEEN_AT + token] as? Number)?.toLong()
                ?: (all[PREFIX_AT + token] as? Number)?.toLong()
                ?: 0L
            result[token] = SeenMark(seq = seq, atEpochMs = at)
        }
        return result
    }

    override fun saveSeen(token: String, mark: SeenMark) {
        seenPrefs.edit()
            .putLong(PREFIX_SEEN + token, mark.seq)
            .putLong(PREFIX_SEEN_AT + token, mark.atEpochMs)
            .putLong(PREFIX_SEQ + token, mark.seq)
            .putLong(PREFIX_AT + token, mark.atEpochMs)
            .apply()
    }

    override fun clearAll() {
        flagsPrefs.edit().clear().apply()
        seenPrefs.edit().clear().apply()
    }

    private fun loadPrefixTokens(prefs: SharedPreferences, prefix: String, setKey: String): Set<String> {
        val fromSet = prefs.getStringSet(setKey, null)
        val fromPrefix = prefs.all.keys
            .filter { it.startsWith(prefix) }
            .map { it.removePrefix(prefix) }
            .toSet()
        return if (fromSet != null) fromSet + fromPrefix else fromPrefix
    }

    private fun savePrefixTokens(prefs: SharedPreferences, prefix: String, setKey: String, tokens: Set<String>) {
        val editor = prefs.edit()
        val existingKeys = prefs.all.keys.filter { it.startsWith(prefix) }
        for (key in existingKeys) {
            val token = key.removePrefix(prefix)
            if (!tokens.contains(token)) {
                editor.remove(key)
            }
        }
        for (token in tokens) {
            editor.putBoolean(prefix + token, true)
        }
        editor.putStringSet(setKey, tokens)
        editor.apply()
    }

    companion object {
        const val FLAGS_PREFS = "AgentWatchChatFlags"
        const val SEEN_PREFS = "AgentWatchChatSeen"

        private const val PREFIX_MUTED = "muted:"
        private const val PREFIX_PINNED = "pinned:"
        private const val PREFIX_HIDDEN = "hidden:"
        private const val PREFIX_LABEL = "label:"

        private const val KEY_MUTED_SET = "muted_tokens"
        private const val KEY_PINNED_SET = "pinned_tokens"
        private const val KEY_HIDDEN_SET = "hidden_tokens"

        private const val PREFIX_SEEN = "seen:"
        private const val PREFIX_SEEN_AT = "seenAt:"
        private const val PREFIX_SEQ = "seq_"
        private const val PREFIX_AT = "at_"
    }
}

class InMemoryChatStateStorage : ChatStateStorage {
    private val lock = Any()
    private val pinned = mutableSetOf<String>()
    private val muted = mutableSetOf<String>()
    private val hidden = mutableSetOf<String>()
    private val labels = mutableMapOf<String, String>()
    private val seen = mutableMapOf<String, SeenMark>()

    override fun loadPinned(): Set<String> = synchronized(lock) { pinned.toSet() }

    override fun savePinned(tokens: Set<String>) {
        synchronized(lock) {
            pinned.clear()
            pinned.addAll(tokens)
        }
    }

    override fun loadMuted(): Set<String> = synchronized(lock) { muted.toSet() }

    override fun saveMuted(tokens: Set<String>) {
        synchronized(lock) {
            muted.clear()
            muted.addAll(tokens)
        }
    }

    override fun loadHidden(): Set<String> = synchronized(lock) { hidden.toSet() }

    override fun saveHidden(tokens: Set<String>) {
        synchronized(lock) {
            hidden.clear()
            hidden.addAll(tokens)
        }
    }

    override fun loadLabels(): Map<String, String> = synchronized(lock) { labels.toMap() }

    override fun saveLabel(token: String, label: String) {
        synchronized(lock) {
            labels[token] = label
        }
    }

    override fun loadSeen(): Map<String, SeenMark> = synchronized(lock) { seen.toMap() }

    override fun saveSeen(token: String, mark: SeenMark) {
        synchronized(lock) {
            seen[token] = mark
        }
    }

    override fun clearAll() {
        synchronized(lock) {
            pinned.clear()
            muted.clear()
            hidden.clear()
            labels.clear()
            seen.clear()
        }
    }
}
