package com.gabriel.agentwatch.network

import androidx.annotation.Keep
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.key
import com.google.gson.Gson
import com.google.gson.JsonObject
import com.google.gson.JsonParseException
import com.google.gson.JsonParser
import com.google.gson.JsonPrimitive
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
        val denyOptionId: String,
        /** The prompt's kind; blank from a relay older than `kind` (contracts §4.1). */
        val kind: String = "",
        /** A question's one-tap answers. */
        val options: List<PushChoice> = emptyList(),
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
 * for a prompt the watch cannot read, then Open. Cancel (esc) is never offered for a question: one
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
        is AgentsUpdate.All -> {
            val byKey = update.agents.associateBy { it.key }
            shown.filter { obsolete(it, byKey[it.key]) }
        }
        is AgentsUpdate.Changed -> shown.filter { it.key == update.agent.key && obsolete(it, update.agent) }
        is AgentsUpdate.Removed -> shown.filter { it.key == update.key }
    }.map { it.notificationId }
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

/**
 * contracts §4.1: FCM does not guarantee order. A `blocked` push whose seq is not newer than the last
 * `resolved` seen for its (host, pane) announces a prompt that was already answered: don't show it.
 */
fun shouldShowBlocked(seq: Long, lastResolved: Long?): Boolean = lastResolved == null || seq > lastResolved

/**
 * The last `resolved` seq per agent, (host, pane), kept across process restarts (the FCM service can be
 * killed between a `resolved` and a late `blocked`). Immutable; bounded to [maxPanes], dropping the
 * least recently recorded one. Stored under [AgentKey.token]: the pane id alone without a host.
 */
class ResolvedSeqs(
    private val maxPanes: Int = 32,
    private val seqs: LinkedHashMap<String, Long> = LinkedHashMap()
) {
    val size: Int get() = seqs.size

    fun lastFor(agent: AgentKey): Long? = seqs[agent.token]

    /** Records [seq] for [agent], keeping the highest seq seen for it. */
    fun record(agent: AgentKey, seq: Long): ResolvedSeqs {
        val id = agent.token
        val next = LinkedHashMap(seqs)
        val kept = maxOf(seq, next.remove(id) ?: seq)
        next[id] = kept
        while (next.size > maxPanes) next.remove(next.keys.first())
        return ResolvedSeqs(maxPanes, next)
    }

    fun encode(): String = JsonObject().apply { seqs.forEach { (pane, seq) -> addProperty(pane, seq) } }.toString()

    companion object {
        /** Anything unreadable decodes to empty: at worst a stale approval is shown, as before this rule. */
        fun decode(raw: String?, maxPanes: Int = 32): ResolvedSeqs {
            if (raw.isNullOrBlank()) return ResolvedSeqs(maxPanes)
            val obj = try {
                JsonParser.parseString(raw) as? JsonObject
            } catch (e: JsonParseException) {
                null
            } ?: return ResolvedSeqs(maxPanes)
            val seqs = LinkedHashMap<String, Long>()
            for ((pane, value) in obj.entrySet()) {
                val prim = value as? JsonPrimitive ?: continue
                if (prim.isNumber) seqs[pane] = prim.asLong
            }
            return ResolvedSeqs(maxPanes, seqs)
        }
    }
}
