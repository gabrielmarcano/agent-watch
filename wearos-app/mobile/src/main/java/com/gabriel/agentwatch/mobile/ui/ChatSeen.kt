package com.gabriel.agentwatch.mobile.ui

import android.content.Context
import com.gabriel.agentwatch.mobile.MobileApplication
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState

/**
 * The last `state_change_seq` the user saw for each chat.
 * Thin compatibility facade over [MobileChatState].
 */
class ChatSeen(private val chatState: MobileChatState) {
    constructor(context: Context) : this(MobileApplication.chatState(context))

    fun seen(key: AgentKey): Long? = chatState.seenSeq(key).takeIf { it >= 0 }
    fun seenAt(key: AgentKey): Long? = chatState.seenAt(key).takeIf { it >= 0 }

    fun markSeen(key: AgentKey, seq: Long) = chatState.markRead(key, seq)
    fun markRead(key: AgentKey, seq: Long) = chatState.markRead(key, seq)
    fun markUnread(key: AgentKey) = chatState.markUnread(key)
    fun prime(agents: List<AgentState>) = chatState.prime(agents)
    fun clearAll() = chatState.clearAll()
}
