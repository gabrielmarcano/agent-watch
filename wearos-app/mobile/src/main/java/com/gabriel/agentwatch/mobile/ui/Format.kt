package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.ui.logic.Age
import com.gabriel.agentwatch.ui.logic.AttentionSection
import com.gabriel.agentwatch.ui.logic.backgroundCounts

/** The list section title for [section]. */
@Composable
fun sectionTitle(section: AttentionSection): String = stringResource(
    when (section) {
        AttentionSection.NEEDS_YOU -> R.string.section_needs_you
        AttentionSection.DONE -> R.string.section_done
        AttentionSection.WORKING -> R.string.section_working
        AttentionSection.IDLE -> R.string.section_idle
        AttentionSection.UNKNOWN -> R.string.section_unknown
    }
)

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
 * What the agent still runs in the background (wearos-app/ARCHITECTURE.md §4b: what it waits on, shells
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
