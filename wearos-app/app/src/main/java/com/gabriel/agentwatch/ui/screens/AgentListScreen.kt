package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.foundation.lazy.items
import androidx.wear.compose.foundation.lazy.rememberTransformingLazyColumnState
import androidx.wear.compose.material3.Button
import androidx.wear.compose.material3.ButtonDefaults
import androidx.wear.compose.material3.CircularProgressIndicator
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.ListSubHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.OutlinedButton
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.ui.components.AgentLogo
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.agentStatus
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.logic.AttentionSection
import com.gabriel.agentwatch.ui.logic.ListNotice
import com.gabriel.agentwatch.ui.logic.attentionSections
import com.gabriel.agentwatch.ui.logic.listStatus
import com.gabriel.agentwatch.ui.theme.Amber
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.Red

private const val DIMMED_ALPHA = 0.6f

@Composable
fun AgentListScreen(
    state: UiState,
    onAgentClick: (paneId: String) -> Unit,
    onHistoryClick: () -> Unit,
    onSettingsClick: () -> Unit
) {
    // The last notice shown, so a retry after a failure keeps saying "Can't reach the relay".
    val lastNotice = remember { arrayOfNulls<ListNotice>(1) }
    val status = listStatus(state, lastNotice[0])
    SideEffect { lastNotice[0] = status.notice }
    val sections = remember(state.agents) { attentionSections(state.agents) }

    // The notice is always item 0; when it changes or the first agents arrive, show the top again
    // unless the user has scrolled down the list.
    val listState = rememberTransformingLazyColumnState()
    LaunchedEffect(status.notice) {
        if (listState.anchorItemIndex <= 2) listState.scrollToItem(0)
    }
    val hasAgents = sections.isNotEmpty()
    LaunchedEffect(hasAgents) {
        if (hasAgents) listState.scrollToItem(0)
    }

    ScreenList(state = listState) { spec ->
        item(key = "notice") {
            status.notice?.let { Notice(it, transformedItem(spec).padding(start = 20.dp, end = 20.dp, bottom = 4.dp)) }
        }

        if (state.agents.isEmpty()) {
            when (status.notice) {
                ListNotice.CONNECTING -> item(key = "progress") {
                    Row(transformedItem(spec), horizontalArrangement = Arrangement.Center) {
                        CircularProgressIndicator(Modifier.size(32.dp))
                    }
                }
                null -> item(key = "empty") {
                    Text(
                        stringResource(R.string.no_agents),
                        modifier = transformedItem(spec).padding(vertical = 8.dp),
                        style = MaterialTheme.typography.titleMedium,
                        textAlign = TextAlign.Center
                    )
                }
                else -> Unit // the notice says it all
            }
        }

        sections.forEach { section ->
            item(key = "section-${section.section}") {
                ListSubHeader(
                    modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                    transformation = SurfaceTransformation(spec)
                ) {
                    Text("${stringResource(section.section.title)} · ${section.agents.size}")
                }
            }
            items(section.agents, key = { it.pane_id }) { agent ->
                AgentRow(
                    agent = agent,
                    onClick = { onAgentClick(agent.pane_id) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .transformedHeight(this, spec)
                        .alpha(if (status.dimmed) DIMMED_ALPHA else 1f),
                    transformation = SurfaceTransformation(spec)
                )
            }
        }

        item(key = "history") {
            FilledTonalButton(
                onClick = onHistoryClick,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp).transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_history, null, MaterialTheme.colorScheme.onSurface) },
                label = { Text(stringResource(R.string.history)) }
            )
        }
        item(key = "settings") {
            OutlinedButton(
                onClick = onSettingsClick,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_settings, null, OnSurfaceVariant) },
                label = { Text(stringResource(R.string.settings)) }
            )
        }
    }
}

private val AttentionSection.title: Int
    get() = when (this) {
        AttentionSection.NEEDS_YOU -> R.string.section_needs_you
        AttentionSection.DONE -> R.string.section_done
        AttentionSection.WORKING -> R.string.section_working
        AttentionSection.IDLE -> R.string.section_idle
        AttentionSection.UNKNOWN -> R.string.section_unknown
    }

@Composable
private fun Notice(notice: ListNotice, modifier: Modifier) {
    val (icon, text, color) = when (notice) {
        ListNotice.CONNECTING -> Triple(R.drawable.ic_sync, R.string.notice_connecting, OnSurfaceVariant)
        ListNotice.RELAY_UNREACHABLE -> Triple(R.drawable.ic_cloud_off, R.string.notice_relay_unreachable, Red)
        ListNotice.MAC_OFFLINE -> Triple(R.drawable.ic_computer, R.string.notice_device_offline, Amber)
        ListNotice.HERDR_STOPPED -> Triple(R.drawable.ic_computer, R.string.notice_herdr_stopped, Amber)
    }
    Row(modifier, horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
        ResIcon(icon, null, color, Modifier.size(18.dp))
        Spacer(Modifier.width(6.dp))
        Text(stringResource(text), color = color, style = MaterialTheme.typography.labelMedium, textAlign = TextAlign.Center)
    }
}

/**
 * One agent: status icon, name (2 lines), then one short fact per line (`ARCHITECTURE.md` §4b): the
 * agent's logo and the status word, never cut; the background agents' count, when it waits on them;
 * the workspace, cut at its end when it does not fit.
 */
@Composable
fun AgentRow(
    agent: AgentState,
    onClick: () -> Unit,
    modifier: Modifier,
    transformation: SurfaceTransformation?
) {
    val status = agentStatus(agent)
    Button(
        onClick = onClick,
        modifier = modifier,
        transformation = transformation,
        colors = ButtonDefaults.buttonColors(
            containerColor = status.style.container,
            contentColor = status.style.onContainer,
            secondaryContentColor = status.style.onContainer,
            iconColor = status.style.accent
        ),
        icon = { ResIcon(status.style.icon, stringResource(R.string.cd_status, status.word), status.style.accent) },
        secondaryLabel = {
            Column {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    AgentLogo(agent.agent)
                    Spacer(Modifier.width(4.dp))
                    Text(status.word, color = status.style.accent, fontWeight = FontWeight.SemiBold, maxLines = 1)
                }
                // Wraps rather than cut at large font scales.
                status.background?.let { Text(it, color = status.style.accent, maxLines = 2) }
                agent.workspace?.takeIf { it.isNotBlank() }?.let {
                    Text(it, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        },
        label = { Text(agent.label.ifBlank { agent.pane_id }, maxLines = 2, overflow = TextOverflow.Ellipsis) }
    )
}
