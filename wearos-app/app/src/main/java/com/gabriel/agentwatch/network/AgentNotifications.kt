package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import java.net.URLEncoder

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
        val denyOptionId: String
    ) : PushMessage()

    data class Done(
        val paneId: String,
        val label: String,
        val title: String,
        val body: String,
        val seq: Long
    ) : PushMessage()

    data class Digest(val title: String, val body: String) : PushMessage()

    /** The pane's prompt was answered or went away: dismiss its approval notification and show nothing. */
    data class Resolved(val paneId: String, val seq: Long?) : PushMessage()

    data class Ignored(val reason: String) : PushMessage()

    /** For logcat: event, pane id and sizes only. Never titles, bodies or prompt text. */
    fun logSummary(): String = when (this) {
        is Blocked -> "blocked pane=$paneId seq=$seq body=${body.length} chars"
        is Done -> "done pane=$paneId seq=$seq body=${body.length} chars"
        is Digest -> "digest body=${body.length} chars"
        is Resolved -> "resolved pane=$paneId seq=$seq"
        is Ignored -> "ignored ($reason)"
    }

    companion object {
        fun parse(data: Map<String, String>): PushMessage {
            val event = data["event"].orEmpty()
            val paneId = data["pane_id"].orEmpty()
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
                        denyOptionId = data["deny_option_id"].orEmpty()
                    )
                "done", "agent_done" ->
                    if (paneId.isBlank()) Ignored("done without pane_id")
                    else Done(
                        paneId = paneId,
                        label = label,
                        title = data["title"]?.takeIf { it.isNotBlank() } ?: "$label finished",
                        body = body,
                        seq = seq ?: 0L
                    )
                "digest" -> Digest(title = data["title"]?.takeIf { it.isNotBlank() } ?: "Agent Watch", body = body)
                "resolved" ->
                    if (paneId.isBlank()) Ignored("resolved without pane_id") else Resolved(paneId, seq)
                else -> Ignored("unknown event '$event'")
            }
        }
    }
}

/** The actions a pane's notification carries; each gets its own PendingIntent identity. */
enum class NotificationAction(val path: String) {
    OPEN("open"),
    ALLOW("allow"),
    DENY("deny"),
    REPLY("reply")
}

object AgentNotifications {
    /** Fixed id for digest pushes (they have no pane). */
    const val DIGEST_ID = 9999

    /** Notification extras that tag what a shown notification is (read back via `activeNotifications`). */
    const val EXTRA_KIND = "aw_kind"
    const val EXTRA_PANE = "aw_pane"
    const val EXTRA_SEQ = "aw_seq"
    const val KIND_APPROVAL = "approval"
    const val KIND_DONE = "done"
    const val KIND_FEEDBACK = "feedback"

    /** One notification per pane: a new push for the pane replaces the old one (phase 4 guide §5). */
    fun idForPane(paneId: String): Int = paneId.hashCode() and 0x7FFFFFFF

    /**
     * The data URI put on every notification intent. PendingIntents are equal when action, data, class
     * and request code match (extras never count), so a URI unique per (pane, action) means a tap can
     * never pick up another pane's extras, whatever the request codes are. Pane ids are percent-encoded.
     */
    fun intentUri(paneId: String, action: NotificationAction): String =
        "agentwatch://notification/${action.path}/${URLEncoder.encode(paneId, "UTF-8")}"

    /** Distinct per action for one pane. Collisions across panes are harmless: [intentUri] tells them apart. */
    fun requestCode(paneId: String, action: NotificationAction): Int = idForPane(paneId) * 4 + action.ordinal
}

/** An approval notification currently shown, as tagged in its extras. */
data class ShownApproval(val notificationId: Int, val paneId: String, val seq: Long)

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
        is AgentsUpdate.All -> {
            val byPane = update.agents.associateBy { it.pane_id }
            shown.filter { obsolete(it, byPane[it.paneId]) }
        }
        is AgentsUpdate.Changed -> shown.filter { it.paneId == update.agent.pane_id && obsolete(it, update.agent) }
        is AgentsUpdate.Removed -> shown.filter { it.paneId == update.paneId }
    }.map { it.notificationId }
}

/** What the silent feedback notification says after a notification action succeeded. */
fun actionSuccessTitle(action: String, isDeny: Boolean): String = when (action) {
    NotificationActionReceiver.ACTION_ANSWER -> if (isDeny) "Denied" else "Approved"
    NotificationActionReceiver.ACTION_CANCEL -> "Canceled" // also used for questions: nothing was "denied"
    NotificationActionReceiver.ACTION_PROMPT -> "Sent"
    else -> "Done"
}
