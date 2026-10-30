package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.ChatDay
import com.gabriel.agentwatch.mobile.ui.logic.ChatStamp
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.ui.logic.Age
import com.gabriel.agentwatch.ui.logic.backgroundCounts
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.format.TextStyle
import java.util.Locale

/** How long ago something happened ("5 min ago"). */
@Composable
fun ageText(age: Age): String = when (age) {
    Age.JustNow -> stringResource(R.string.age_now)
    is Age.Minutes -> stringResource(R.string.age_minutes, age.n)
    is Age.Hours -> stringResource(R.string.age_hours, age.n)
    is Age.Days -> stringResource(R.string.age_days, age.n)
    Age.Unknown -> ""
}

/**
 * The time at the end of a chat row or message, in the phone's own format: the hour today, the day
 * this week, the date after that. "" when the relay sent no readable time.
 */
fun chatStampText(stamp: ChatStamp?, locale: Locale = Locale.getDefault()): String = when (stamp) {
    null -> ""
    is ChatStamp.Today -> stamp.time.format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale))
    is ChatStamp.ThisWeek -> stamp.day.getDisplayName(TextStyle.SHORT, locale)
    is ChatStamp.Older -> stamp.date.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.SHORT).withLocale(locale))
}

/** The text of a day separator: Today, Yesterday, or the date. */
@Composable
fun chatDayText(day: ChatDay): String = when (day) {
    ChatDay.Today -> stringResource(R.string.chat_today)
    ChatDay.Yesterday -> stringResource(R.string.chat_yesterday)
    is ChatDay.Older -> day.date.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM))
}

/**
 * What the agent still runs in the background (android/ARCHITECTURE.md §4b: what it waits on, shells
 * and monitors), or null when nothing does. One fact per item, joined by the separator.
 */
@Composable
fun backgroundLine(agent: AgentState): String? {
    val counts = agent.backgroundCounts()
    if (!counts.any) return null
    val parts = buildList {
        if (counts.agents > 0) add(pluralStringResource(R.plurals.background_agents, counts.agents, counts.agents))
        if (counts.shells > 0) add(pluralStringResource(R.plurals.background_shells, counts.shells, counts.shells))
        if (counts.monitors > 0) add(pluralStringResource(R.plurals.background_monitors, counts.monitors, counts.monitors))
    }
    return parts.joinToString(stringResource(R.string.background_separator))
}
