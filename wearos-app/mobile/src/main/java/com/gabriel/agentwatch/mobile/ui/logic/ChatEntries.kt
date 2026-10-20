package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.util.MarkdownFormatter
import java.time.DayOfWeek
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.ZoneId
import java.time.temporal.ChronoUnit

/**
 * One row of an agent's conversation, oldest first, as the chat screen renders it
 * (`docs/STATUS.md` — the phone shows turns, not one "last reply").
 */
sealed interface ChatEntry {
    /** A calendar day separator before the turns that follow it. */
    data class Day(val date: LocalDate) : ChatEntry

    /** One finished turn: the user's question and the agent's answer ([HistoryItem]). */
    data class Turn(val item: HistoryItem) : ChatEntry

    /** A message the phone sent that the relay has not published as a turn yet. */
    data class Pending(val text: String) : ChatEntry

    /** The prompt waiting for an answer: the last thing in the conversation. */
    data object Prompt : ChatEntry

    /** The agent is working and no prompt is waiting. */
    data object Working : ChatEntry
}

/**
 * Builds the conversation for [history] (newest first, as the store keeps it): oldest first, with a
 * [ChatEntry.Day] separator whenever the calendar day changes, then any optimistic [pending] messages,
 * then the prompt or the working indicator.
 */
fun chatEntries(
    history: List<HistoryItem>,
    hasPrompt: Boolean,
    working: Boolean,
    pending: List<String> = emptyList(),
    zone: ZoneId = ZoneId.systemDefault()
): List<ChatEntry> {
    val entries = mutableListOf<ChatEntry>()
    var lastDay: LocalDate? = null
    history.asReversed().forEach { item ->
        val day = completedDay(item, zone)
        if (day != null && day != lastDay) {
            entries += ChatEntry.Day(day)
            lastDay = day
        }
        entries += ChatEntry.Turn(item)
    }
    pending.filter { it.isNotBlank() }.forEach { entries += ChatEntry.Pending(it) }
    when {
        hasPrompt -> entries += ChatEntry.Prompt
        working -> entries += ChatEntry.Working
    }
    return entries
}

/** The calendar day a turn finished on, or null when the relay sent no parseable `completed_at`. */
fun completedDay(item: HistoryItem, zone: ZoneId = ZoneId.systemDefault()): LocalDate? =
    parseInstant(item.completed_at)?.atZone(zone)?.toLocalDate()

/**
 * When this agent last did anything: its newest turn's `completed_at`, else its own `updated_at`.
 * The chat list sorts by this, newest first, like a messaging app.
 */
fun AgentState.lastActivity(history: List<HistoryItem>): Instant =
    history.firstOrNull { it.key == key }?.let { parseInstant(it.completed_at) }
        ?: parseInstant(updated_at)
        ?: Instant.EPOCH

/** A pending message is no longer pending once its turn arrives with the same query. */
fun unmatchedPending(history: List<HistoryItem>, pending: List<String>): List<String> {
    val seen = history.mapNotNull { it.query }.toSet()
    return pending.filterNot { it in seen }
}

/**
 * The chat needs the user: a prompt is waiting, or herdr reports `blocked` (a prompt the bridge has
 * not published yet). These chats sit under "Needs you", above the rest, whatever their recency.
 */
fun AgentState.needsAnswer(): Boolean = prompt != null || status == "blocked"

/**
 * The one-line preview of an answer for the chat list: its first non-blank line, markdown markers
 * removed, cut at [maxChars]. Null when there is no text at all.
 */
fun previewLine(item: HistoryItem, maxChars: Int = PREVIEW_CHARS): String? =
    previewText(item.response, maxChars)

/** [previewLine] for a text that has no [HistoryItem] yet (an optimistic message). */
fun previewText(text: String, maxChars: Int = PREVIEW_CHARS): String? =
    MarkdownFormatter.clean(text)
        .lineSequence()
        .map { it.trim() }
        .firstOrNull { it.isNotBlank() }
        ?.let { if (it.length <= maxChars) it else it.take(maxChars - 1).trimEnd() + "…" }

internal const val PREVIEW_CHARS = 160

/**
 * Whether the chat has something new since the user last opened it. A sequence missing from [seenSeq]
 * (an agent first sighted now) is not unread: it was primed by the list, and a fresh install must not
 * mark every conversation unread.
 */
fun isUnread(agent: AgentState, seenSeq: Long?): Boolean =
    seenSeq != null && agent.state_change_seq > seenSeq

/** The time shown at the end of a chat row: the hour today, the day this week, the date after that. */
sealed interface ChatStamp {
    data class Today(val time: LocalTime) : ChatStamp
    data class ThisWeek(val day: DayOfWeek) : ChatStamp
    data class Older(val date: LocalDate) : ChatStamp
}

/**
 * The [ChatStamp] for an RFC 3339 [timestamp] at [now]: null when unreadable. A timestamp from tomorrow
 * (the host's clock ahead of the phone's) counts as today, like `ageOf`.
 */
fun chatStamp(timestamp: String, now: Instant, zone: ZoneId = ZoneId.systemDefault()): ChatStamp? =
    chatStamp(parseInstant(timestamp), now, zone)

/** [chatStamp] for an [instant] already parsed; null for none (an agent that never reported a time). */
fun chatStamp(instant: Instant?, now: Instant, zone: ZoneId = ZoneId.systemDefault()): ChatStamp? {
    if (instant == null) return null
    val at = instant.atZone(zone)
    val today = now.atZone(zone).toLocalDate()
    val days = ChronoUnit.DAYS.between(at.toLocalDate(), today)
    return when {
        days <= 0 -> ChatStamp.Today(at.toLocalTime())
        days < 7 -> ChatStamp.ThisWeek(at.dayOfWeek)
        else -> ChatStamp.Older(at.toLocalDate())
    }
}

/** Whether a day separator reads "Today", "Yesterday" or a date. */
sealed interface ChatDay {
    data object Today : ChatDay
    data object Yesterday : ChatDay
    data class Older(val date: LocalDate) : ChatDay
}

fun chatDay(date: LocalDate, today: LocalDate): ChatDay = when (ChronoUnit.DAYS.between(date, today)) {
    0L -> ChatDay.Today
    1L -> ChatDay.Yesterday
    else -> ChatDay.Older(date)
}

internal fun parseInstant(value: String): Instant? =
    value.takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() }
