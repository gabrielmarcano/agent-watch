package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentKey
import java.net.URLEncoder

// The push parser and the shared dismissal decisions live in :core (PushMessages.kt); the watch's
// notification builder and its action identity stay here.

/** The actions a pane's notification carries; each gets its own PendingIntent identity. */
enum class NotificationAction(val path: String) {
    OPEN("open"),
    ALLOW("allow"),
    DENY("deny"),
    REPLY("reply"),
    /** A question's answer; the option id makes each one's identity unique. */
    ANSWER("answer")
}

object AgentNotifications {
    /** Fixed id for digest pushes (they have no pane). */
    const val DIGEST_ID = 9999

    /** Notification extras that tag what a shown notification is (read back via `activeNotifications`). */
    const val EXTRA_KIND = "aw_kind"
    const val EXTRA_PANE = "aw_pane"
    const val EXTRA_HOST = "aw_host"
    const val EXTRA_SEQ = "aw_seq"
    const val KIND_APPROVAL = "approval"
    const val KIND_DONE = "done"
    const val KIND_FEEDBACK = "feedback"

    /**
     * One notification per agent, (host, pane): a new push for it replaces the old one. Without a host
     * (an older relay) the id is the pane's, as before hosts.
     */
    fun idFor(agent: AgentKey): Int = agent.token.hashCode() and 0x7FFFFFFF

    /**
     * The data URI put on every notification intent. PendingIntents are equal when action, data, class
     * and request code match (extras never count), so a URI unique per (host, pane, action) means a tap
     * can never pick up another agent's extras, whatever the request codes are. Ids are percent-encoded;
     * the host goes in a query (absent without one, so those URIs stay as before hosts).
     */
    fun intentUri(agent: AgentKey, action: NotificationAction, optionId: String? = null): String {
        val path = if (optionId.isNullOrBlank()) action.path else "${action.path}/${URLEncoder.encode(optionId, "UTF-8")}"
        val base = "agentwatch://notification/$path/${URLEncoder.encode(agent.paneId, "UTF-8")}"
        return if (agent.host.isEmpty()) base else "$base?host=${URLEncoder.encode(agent.host, "UTF-8")}"
    }

    /** Distinct per action for one agent. Collisions across agents are harmless: [intentUri] tells them apart. */
    fun requestCode(agent: AgentKey, action: NotificationAction): Int = idFor(agent) * 4 + action.ordinal
}

/** What the silent feedback notification says after a notification action succeeded. */
fun actionSuccessTitle(action: String, isDeny: Boolean, isChoice: Boolean = false): String = when (action) {
    NotificationActionReceiver.ACTION_ANSWER -> when {
        isDeny -> "Denied"
        isChoice -> "Answered"
        else -> "Approved"
    }
    NotificationActionReceiver.ACTION_CANCEL -> "Canceled" // also used for questions: nothing was "denied"
    NotificationActionReceiver.ACTION_PROMPT -> "Sent"
    else -> "Done"
}
