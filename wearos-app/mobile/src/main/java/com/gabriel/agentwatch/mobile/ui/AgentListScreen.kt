package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.Amber
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.ui.logic.AttentionSection
import com.gabriel.agentwatch.ui.logic.ListNotice
import com.gabriel.agentwatch.ui.logic.ageOf
import com.gabriel.agentwatch.ui.logic.attentionSections
import com.gabriel.agentwatch.ui.logic.listStatus
import com.gabriel.agentwatch.ui.logic.shownStatus
import java.time.Instant

/**
 * Every agent, grouped by what it needs from the user and most urgent first (wearos-app/
 * ARCHITECTURE.md §4a-§4b). One page: the phone has the room the watch does not, and the host is
 * named on each row when the relay knows several (hostNameFor).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AgentListScreen(
    state: UiState,
    onOpen: (AgentState) -> Unit,
    onHistory: () -> Unit,
    onRefresh: () -> Unit
) {
    val status = listStatus(state)
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.agents_title)) },
                actions = {
                    TextButton(onClick = onRefresh) { Text(stringResource(R.string.refresh)) }
                    TextButton(onClick = onHistory) { Text(stringResource(R.string.history)) }
                }
            )
        }
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            status.notice?.let { Notice(it) }
            if (state.agents.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.no_agents), color = OnSurfaceVariant)
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    attentionSections(state.agents).forEach { section ->
                        item(key = "section-${section.section}") {
                            SectionHeader(section.section, section.agents.size)
                        }
                        items(section.agents, key = { it.key.token }) { agent ->
                            AgentRow(
                                agent = agent,
                                hostName = hostNameFor(state.hosts, agent.host),
                                dimmed = status.dimmed,
                                onClick = { onOpen(agent) }
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SectionHeader(section: AttentionSection, count: Int) {
    Text(
        text = "${sectionTitle(section)} · $count",
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 4.dp, top = 8.dp, bottom = 2.dp)
    )
}

@Composable
private fun Notice(notice: ListNotice) {
    val text = when (notice) {
        ListNotice.CONNECTING -> stringResource(R.string.notice_connecting)
        ListNotice.RELAY_UNREACHABLE -> stringResource(R.string.notice_relay_unreachable)
        ListNotice.MAC_OFFLINE -> stringResource(R.string.notice_device_offline)
        ListNotice.HERDR_STOPPED -> stringResource(R.string.notice_herdr_stopped)
    }
    val color = if (notice == ListNotice.CONNECTING) OnSurfaceVariant else Amber
    Text(
        text = text,
        color = color,
        style = MaterialTheme.typography.labelLarge,
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)
    )
}

@Composable
private fun AgentRow(agent: AgentState, hostName: String?, dimmed: Boolean, onClick: () -> Unit) {
    val style = statusStyle(agent.shownStatus())
    val age = ageText(ageOf(agent.updated_at, Instant.now()))
    val statusLine = listOfNotNull(
        stringResource(style.label).takeIf { it.isNotBlank() },
        age.takeIf { it.isNotBlank() }
    ).joinToString(" · ")
    val background = backgroundLine(agent)

    Card(
        colors = CardDefaults.cardColors(containerColor = style.container),
        modifier = Modifier
            .fillMaxWidth()
            .alpha(if (dimmed) 0.5f else 1f)
            .clickable(onClick = onClick)
    ) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(12.dp).background(style.accent, CircleShape))
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    text = agent.label.ifBlank { agent.agent },
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis
                )
                Text(statusLine, color = style.accent, style = MaterialTheme.typography.labelLarge)
                background?.let {
                    Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall)
                }
                agent.workspace?.takeIf { it.isNotBlank() }?.let {
                    Text(
                        text = it,
                        color = OnSurfaceVariant,
                        style = MaterialTheme.typography.bodySmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
            hostName?.let {
                Spacer(Modifier.width(8.dp))
                Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.labelMedium, maxLines = 1)
            }
        }
    }
}
