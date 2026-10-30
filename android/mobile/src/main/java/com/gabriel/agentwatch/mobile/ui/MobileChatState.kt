package com.gabriel.agentwatch.mobile.ui

import com.gabriel.agentwatch.mobile.ui.logic.parseInstant
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.key
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.UUID

data class SeenMark(val seq: Long, val atEpochMs: Long)

data class PendingMessage(
    val id: String = UUID.randomUUID().toString(),
    val text: String,
    val sentAt: Long = System.currentTimeMillis()
)

data class ChatStateSnapshot(
    val pinned: Set<String> = emptySet(),
    val muted: Set<String> = emptySet(),
    val hidden: Set<String> = emptySet(),
    val labels: Map<String, String> = emptyMap(),
    val seen: Map<String, SeenMark> = emptyMap(),
    val outbox: Map<String, List<PendingMessage>> = emptyMap()
) {
    fun isPinned(key: AgentKey): Boolean = pinned.contains(key.token)
    fun isMuted(key: AgentKey): Boolean = muted.contains(key.token)
    fun isHidden(key: AgentKey): Boolean = hidden.contains(key.token)
    fun shouldSuppressPush(key: AgentKey): Boolean = isMuted(key) || isHidden(key)

    fun seen(agent: AgentState): Boolean {
        val mark = seen[agent.key.token] ?: return false
        return mark.seq >= agent.state_change_seq
    }

    fun seenAt(key: AgentKey): Long = seen[key.token]?.atEpochMs ?: 0L
    fun seenSeq(key: AgentKey): Long = seen[key.token]?.seq ?: -1L
    fun label(token: String): String = labels[token].orEmpty().ifBlank { token }

    fun pendingFor(
        agent: AgentKey,
        history: List<HistoryItem>,
        now: Long = System.currentTimeMillis()
    ): List<PendingMessage> {
        val messages = outbox[agent.token].orEmpty()
        if (messages.isEmpty()) return emptyList()

        return messages.filter { msg ->
            // Filter expired items (> 10 min)
            val ageMs = now - msg.sentAt
            if (ageMs > 10 * 60 * 1000L) return@filter false

            // Filter acknowledged turns (matching normalized query with askedAt >= sentAt - 5_000)
            val normText = msg.text.trim()
            val acknowledged = history.any { item ->
                val query = item.query?.trim() ?: return@any false
                if (query != normText) return@any false

                val askedAtMs = (item.asked_at ?: item.completed_at)?.let(::parseInstant)?.toEpochMilli() ?: 0L
                askedAtMs == 0L || askedAtMs >= (msg.sentAt - 5_000L)
            }
            !acknowledged
        }
    }
}

class MobileChatState(private val storage: ChatStateStorage) {
    private val lock = Any()
    private val _state = MutableStateFlow(loadInitialSnapshot())
    val state: StateFlow<ChatStateSnapshot> = _state.asStateFlow()

    private fun loadInitialSnapshot(): ChatStateSnapshot {
        return ChatStateSnapshot(
            pinned = storage.loadPinned(),
            muted = storage.loadMuted(),
            hidden = storage.loadHidden(),
            labels = storage.loadLabels(),
            seen = storage.loadSeen(),
            outbox = emptyMap()
        )
    }

    fun isPinned(key: AgentKey): Boolean = _state.value.isPinned(key)
    fun isMuted(key: AgentKey): Boolean = _state.value.isMuted(key)
    fun isHidden(key: AgentKey): Boolean = _state.value.isHidden(key)
    fun shouldSuppressPush(key: AgentKey): Boolean = _state.value.shouldSuppressPush(key)
    fun seen(agent: AgentState): Boolean = _state.value.seen(agent)
    fun seenAt(key: AgentKey): Long = _state.value.seenAt(key)
    fun seenSeq(key: AgentKey): Long = _state.value.seenSeq(key)
    fun label(token: String): String = _state.value.label(token)

    fun isPinned(token: String): Boolean = _state.value.pinned.contains(token)
    fun isMuted(token: String): Boolean = _state.value.muted.contains(token)
    fun isHidden(token: String): Boolean = _state.value.hidden.contains(token)

    fun setPinned(key: AgentKey, pinned: Boolean, label: String = "") = setPinned(key.token, pinned, label)
    fun setPinned(token: String, pinned: Boolean, label: String = "") {
        synchronized(lock) {
            val current = _state.value
            val nextPinned = if (pinned) current.pinned + token else current.pinned - token
            val nextLabels = if (label.isNotBlank()) current.labels + (token to label) else current.labels
            storage.savePinned(nextPinned)
            if (label.isNotBlank()) storage.saveLabel(token, label)
            _state.value = current.copy(pinned = nextPinned, labels = nextLabels)
        }
    }

    fun togglePin(key: AgentKey, label: String = "") = togglePin(key.token, label)
    fun togglePin(token: String, label: String = "") {
        synchronized(lock) {
            setPinned(token, !isPinned(token), label)
        }
    }

    fun pin(token: String, label: String = "") = setPinned(token, true, label)
    fun unpin(token: String) = setPinned(token, false)

    fun setMuted(key: AgentKey, muted: Boolean, label: String = "") = setMuted(key.token, muted, label)
    fun setMuted(token: String, muted: Boolean, label: String = "") {
        synchronized(lock) {
            val current = _state.value
            val nextMuted = if (muted) current.muted + token else current.muted - token
            val nextLabels = if (label.isNotBlank()) current.labels + (token to label) else current.labels
            storage.saveMuted(nextMuted)
            if (label.isNotBlank()) storage.saveLabel(token, label)
            _state.value = current.copy(muted = nextMuted, labels = nextLabels)
        }
    }

    fun toggleMute(key: AgentKey, label: String = "") = toggleMute(key.token, label)
    fun toggleMute(token: String, label: String = "") {
        synchronized(lock) {
            setMuted(token, !isMuted(token), label)
        }
    }

    fun mute(token: String, label: String = "") = setMuted(token, true, label)
    fun unmute(token: String) = setMuted(token, false)

    fun setHidden(key: AgentKey, hidden: Boolean, label: String = "") = setHidden(key.token, hidden, label)
    fun setHidden(token: String, hidden: Boolean, label: String = "") {
        synchronized(lock) {
            val current = _state.value
            val nextHidden = if (hidden) current.hidden + token else current.hidden - token
            val nextLabels = if (label.isNotBlank()) current.labels + (token to label) else current.labels
            storage.saveHidden(nextHidden)
            if (label.isNotBlank()) storage.saveLabel(token, label)
            _state.value = current.copy(hidden = nextHidden, labels = nextLabels)
        }
    }

    fun hide(token: String, label: String = "") = setHidden(token, true, label)
    fun unhide(token: String) = setHidden(token, false)

    fun markSeen(
        agent: AgentState,
        items: List<HistoryItem> = emptyList(),
        now: Long = System.currentTimeMillis()
    ) {
        val maxHistoryMs = items.mapNotNull { item ->
            (item.completed_at ?: item.asked_at)?.let(::parseInstant)?.toEpochMilli()
        }.maxOrNull() ?: 0L
        val atEpochMs = maxOf(now, maxHistoryMs)
        val mark = SeenMark(seq = agent.state_change_seq, atEpochMs = atEpochMs)

        synchronized(lock) {
            val current = _state.value
            storage.saveSeen(agent.key.token, mark)
            val nextSeen = current.seen + (agent.key.token to mark)
            _state.value = current.copy(seen = nextSeen)
        }
    }

    fun markRead(key: AgentKey, seq: Long, now: Long = System.currentTimeMillis()) {
        val mark = SeenMark(seq = seq, atEpochMs = now)
        synchronized(lock) {
            val current = _state.value
            storage.saveSeen(key.token, mark)
            val nextSeen = current.seen + (key.token to mark)
            _state.value = current.copy(seen = nextSeen)
        }
    }

    fun markUnread(key: AgentKey) {
        val mark = SeenMark(seq = -1L, atEpochMs = 0L)
        synchronized(lock) {
            val current = _state.value
            storage.saveSeen(key.token, mark)
            val nextSeen = current.seen + (key.token to mark)
            _state.value = current.copy(seen = nextSeen)
        }
    }

    fun prime(agents: List<AgentState>, now: Long = System.currentTimeMillis()) {
        synchronized(lock) {
            val current = _state.value
            val unprimed = agents.filter { !current.seen.containsKey(it.key.token) }
            if (unprimed.isEmpty()) return

            val updatedSeen = current.seen.toMutableMap()
            for (agent in unprimed) {
                val mark = SeenMark(seq = agent.state_change_seq.coerceAtLeast(0), atEpochMs = now)
                storage.saveSeen(agent.key.token, mark)
                updatedSeen[agent.key.token] = mark
            }
            _state.value = current.copy(seen = updatedSeen)
        }
    }

    fun addOutbox(agent: AgentKey, text: String, now: Long = System.currentTimeMillis()): PendingMessage {
        val pendingMsg = PendingMessage(text = text, sentAt = now)
        synchronized(lock) {
            val current = _state.value
            val existing = current.outbox[agent.token].orEmpty()
            val nextOutbox = current.outbox + (agent.token to (existing + pendingMsg))
            _state.value = current.copy(outbox = nextOutbox)
        }
        return pendingMsg
    }

    fun reconcileOutbox(agent: AgentKey, history: List<HistoryItem>, now: Long = System.currentTimeMillis()) {
        synchronized(lock) {
            val current = _state.value
            val filtered = current.pendingFor(agent, history, now)
            val nextOutbox = if (filtered.isEmpty()) {
                current.outbox - agent.token
            } else {
                current.outbox + (agent.token to filtered)
            }
            _state.value = current.copy(outbox = nextOutbox)
        }
    }

    fun clearAll() {
        synchronized(lock) {
            storage.clearAll()
            _state.value = ChatStateSnapshot()
        }
    }
}
