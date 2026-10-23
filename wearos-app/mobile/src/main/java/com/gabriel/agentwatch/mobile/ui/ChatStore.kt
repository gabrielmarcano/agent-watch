package com.gabriel.agentwatch.mobile.ui

import android.content.Context
import com.gabriel.agentwatch.mobile.MobileApplication

/**
 * What the owner did with each chat on this phone: muted, pinned, hidden.
 * Thin compatibility facade over [MobileChatState].
 */
class ChatStore(private val chatState: MobileChatState) {
    constructor(context: Context) : this(MobileApplication.chatState(context))

    fun isMuted(token: String): Boolean = chatState.isMuted(token)
    fun isPinned(token: String): Boolean = chatState.isPinned(token)
    fun isHidden(token: String): Boolean = chatState.isHidden(token)

    fun mute(token: String, label: String) = chatState.mute(token, label)
    fun unmute(token: String) = chatState.unmute(token)

    fun pin(token: String, label: String) = chatState.pin(token, label)
    fun unpin(token: String) = chatState.unpin(token)

    fun hide(token: String, label: String) = chatState.hide(token, label)
    fun unhide(token: String) = chatState.unhide(token)

    fun muted(): List<String> = chatState.state.value.muted.toList().sorted()
    fun pinned(): List<String> = chatState.state.value.pinned.toList().sorted()
    fun hidden(): List<String> = chatState.state.value.hidden.toList().sorted()

    fun mutedCount(): Int = chatState.state.value.muted.size
    fun label(token: String): String = chatState.label(token)
    fun clearAll() = chatState.clearAll()
}
