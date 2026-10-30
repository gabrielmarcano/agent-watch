package com.gabriel.agentwatch.network

import androidx.annotation.Keep
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.key
import com.google.gson.Gson

// The push payload and the notification decisions both clients share. The builders and the
// Android services stay in each app: the watch and the phone lay their notifications out
// differently but must read and dismiss the same pushes (contracts.md §4.1).

/**
 * An FCM data message (contracts §4.1, plus the data-only `resolved` event), parsed.
 * FCM data values are all strings; anything unknown or incomplete becomes [Ignored] and shows nothing.
 */
sealed class PushMessage {
    data class Blocked(
        val paneId: String,
        val label: String,
        val title: String,
        val body: String,
        val seq: Long,
        val fingerprint: String,
        val allowOptionId: String,
        val denyOptionId: String,
        /** The prompt's kind; blank from a relay older than `kind` (contracts §4.1). */
        val kind: String = "",
        /** A question's one-tap answers. */
        val options: List<PushChoice> = emptyList(),
        /** herdr's agent id (`claude`, `agy`, `opencode`, …); the client picks its mark. */
        val agent: String = "",
        /** The agent's host id; "" from a relay that predates hosts (contracts §4.1). */
        val host: String = "",
        /** The host's name; the relay's [title] already carries it when it registers several hosts. */
        val hostName: String = ""
    ) : PushMessage() {
        val key: AgentKey get() = AgentKey(host, paneId)
    }

    data class Done(
        val paneId: String,
        val label: String,
        val title: String,
        val body: String,
        val seq: Long,
        /** herdr's agent id (`claude`, `agy`, `opencode`, …); the client picks its mark. */
        val agent: String = "",
        val host: String = "",
        val hostName: String = ""
    ) : PushMessage() {
        val key: AgentKey get() = AgentKey(host, paneId)
    }

    data class Digest(val title: String, val body: String) : PushMessage()

    /** The pane's prompt was answered or went away: dismiss its approval notification and show nothing. */
    data class Resolved(val paneId: String, val seq: Long?, val host: String = "") : PushMessage() {
        val key: AgentKey get() = AgentKey(host, paneId)
    }

    data class Ignored(val reason: String) : PushMessage()

    /** For logcat: event, pane id and sizes only. Never titles, bodies or prompt text. */
    fun logSummary(): String = when (this) {
        is Blocked -> "blocked host=$host pane=$paneId seq=$seq body=${body.length} chars"
        is Done -> "done host=$host pane=$paneId seq=$seq body=${body.length} chars"
        is Digest -> "digest body=${body.length} chars"
        is Resolved -> "resolved host=$host pane=$paneId seq=$seq"
        is Ignored -> "ignored ($reason)"
    }

    companion object {
        fun parse(data: Map<String, String>): PushMessage {
            val event = data["event"].orEmpty()
            val paneId = data["pane_id"].orEmpty()
            val host = data["host"].orEmpty()
            val hostName = data["host_name"].orEmpty()
            val label = data["label"]?.takeIf { it.isNotBlank() } ?: data["agent"].orEmpty()
            val body = data["body"].orEmpty()
            val seq = data["state_change_seq"]?.toLongOrNull()

            return when (event) {
                "blocked", "agent_blocked" ->
                    if (paneId.isBlank()) Ignored("blocked without pane_id")
                    else Blocked(
                        paneId = paneId,
                        label = label,
                        title = data["title"]?.takeIf { it.isNotBlank() } ?: "$label needs you",
                        body = body,
                        seq = seq ?: 0L,
                        fingerprint = data["fingerprint"].orEmpty(),
                        allowOptionId = data["allow_option_id"].orEmpty(),
                        denyOptionId = data["deny_option_id"].orEmpty(),
                        kind = data["kind"].orEmpty(),
                        options = parseChoices(data["options"]),
                        agent = data["agent"].orEmpty(),
                        host = host,
                        hostName = hostName
                    )
                "done", "agent_done" ->
                    if (paneId.isBlank()) Ignored("done without pane_id")
                    else Done(
                        paneId = paneId,
                        label = label,
                        title = data["title"]?.takeIf { it.isNotBlank() } ?: "$label finished",
                        body = body,
                        seq = seq ?: 0L,
                        agent = data["agent"].orEmpty(),
                        host = host,
                        hostName = hostName
                    )
                "digest" -> Digest(title = data["title"]?.takeIf { it.isNotBlank() } ?: "Agent Watch", body = body)
                "resolved" ->
                    if (paneId.isBlank()) Ignored("resolved without pane_id") else Resolved(paneId, seq, host)
                else -> Ignored("unknown event '$event'")
            }
        }
    }
}

/** One answer a question's notification offers (contracts §4.1 `options`). */
@Keep
data class PushChoice(val id: String = "", val label: String = "")

/** A JSON array string of choices; anything malformed means none. */
private fun parseChoices(json: String?): List<PushChoice> {
    if (json.isNullOrBlank()) return emptyList()
    return try {
        Gson().fromJson(json, Array<PushChoice>::class.java)?.filter { it.id.isNotBlank() && it.label.isNotBlank() }.orEmpty()
    } catch (_: Exception) {
        emptyList()
    }
}

/** One button of a blocked notification. */
data class NotificationButton(val kind: Kind, val label: String, val optionId: String? = null) {
    enum class Kind { ANSWER, DENY, CANCEL, OPEN }
}

/**
 * The buttons of a blocked notification: a question's answers, a permission's Allow and Deny, nothing
 * for a prompt the client cannot read, then Open. Cancel (esc) is never offered for a question: one
 * mistaken tap would dismiss it. A push from a relay without `kind` keeps the old Allow / Deny-or-Cancel.
 */
fun blockedButtons(message: PushMessage.Blocked): List<NotificationButton> {
    val buttons = mutableListOf<NotificationButton>()
    fun allowDeny(cancelWithoutDeny: Boolean) {
        if (message.allowOptionId.isNotBlank()) {
            buttons += NotificationButton(NotificationButton.Kind.ANSWER, "Allow", message.allowOptionId)
        }
        when {
            message.denyOptionId.isNotBlank() -> buttons += NotificationButton(NotificationButton.Kind.DENY, "Deny", message.denyOptionId)
            cancelWithoutDeny -> buttons += NotificationButton(NotificationButton.Kind.CANCEL, "Cancel")
        }
    }
    when (message.kind) {
        "question" -> message.options.forEach { buttons += NotificationButton(NotificationButton.Kind.ANSWER, it.label, it.id) }
        "unknown" -> Unit
        "permission" -> allowDeny(cancelWithoutDeny = false)
        else -> allowDeny(cancelWithoutDeny = true)
    }
    buttons += NotificationButton(NotificationButton.Kind.OPEN, "Open")
    return buttons
}

/** An approval notification currently shown, as tagged in its extras. [host]: "" before hosts. */
data class ShownApproval(val notificationId: Int, val paneId: String, val seq: Long, val host: String = "") {
    val key: AgentKey get() = AgentKey(host, paneId)
}

/**
 * A `resolved` push arrived for the shown approval's pane. Dismiss it unless the shown prompt is newer
 * than the resolution (FCM does not guarantee order: a late `resolved` must not hide the next prompt).
 */
fun shouldDismissOnResolved(shown: ShownApproval?, resolvedSeq: Long?): Boolean =
    shown != null && (resolvedSeq == null || shown.seq <= resolvedSeq)

/**
 * Approval notifications made obsolete by what the repository just learned: the pane is gone, or its
 * agent is no longer blocked at a state at least as new as the notification's.
 */
fun approvalsToDismiss(shown: List<ShownApproval>, update: AgentsUpdate): List<Int> {
    fun obsolete(approval: ShownApproval, agent: AgentState?): Boolean =
        agent == null || (agent.status != "blocked" && agent.state_change_seq >= approval.seq)

    return when (update) {
        // findAgent: an approval shown under a relay before hosts (host "") still finds its pane while
        // only one host has it, so it stays while that agent is blocked at the same seq.
        is AgentsUpdate.All -> shown.filter { obsolete(it, update.agents.findAgent(it.key)) }
        is AgentsUpdate.Changed -> shown.filter { it.key == update.agent.key && obsolete(it, update.agent) }
        is AgentsUpdate.Removed -> shown.filter { it.key == update.key }
    }.map { it.notificationId }
}
