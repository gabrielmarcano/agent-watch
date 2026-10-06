package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.foundation.lazy.items
import androidx.wear.compose.material3.ListHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.TitleCard
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.AgentLogo
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.ageText
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.util.MarkdownFormatter

/** Finished turns, newest first: all agents, or one pane's when [paneId] is set. */
@Composable
fun HistoryListScreen(
    paneId: String?,
    agentLabel: String?,
    historyItems: List<HistoryItem>,
    onSelectHistoryItem: (HistoryItem) -> Unit
) {
    val items = remember(historyItems, paneId) {
        if (paneId.isNullOrBlank()) historyItems else historyItems.filter { it.pane_id == paneId }
    }
    LaunchedEffect(paneId) { RelayRepository.refresh() }

    ScreenList { spec ->
        item(key = "title") {
            ListHeader(
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec)
            ) {
                Text(
                    if (agentLabel.isNullOrBlank()) stringResource(R.string.history_title)
                    else stringResource(R.string.history_title_agent, agentLabel),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
        if (items.isEmpty()) {
            item(key = "empty") {
                Text(
                    stringResource(R.string.no_history),
                    modifier = transformedItem(spec).padding(vertical = 12.dp),
                    color = OnSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                    textAlign = TextAlign.Center
                )
            }
        }
        items(items, key = { it.id }) { entry ->
            // One line of prose: headings and paragraphs of the answer run together.
            val preview = remember(entry.response) {
                MarkdownFormatter.truncate(entry.response, 120).replace(Regex("\\s*\\n+\\s*"), " ")
            }
            // Across all panes, the agent's logo leads the age; one pane's history leaves it out.
            val showAgent = entry.agent.isNotBlank() && paneId.isNullOrBlank()
            val age = ageText(entry.completed_at)
            TitleCard(
                onClick = { onSelectHistoryItem(entry) },
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                title = {
                    Text(entry.label.ifBlank { entry.agent }, maxLines = 2, overflow = TextOverflow.Ellipsis)
                },
                subtitle = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (showAgent) {
                            AgentLogo(entry.agent, OnSurfaceVariant, 12.sp)
                            Spacer(Modifier.width(4.dp))
                        }
                        Text(age, color = OnSurfaceVariant, style = MaterialTheme.typography.labelSmall, maxLines = 1)
                    }
                }
            ) {
                entry.query?.takeIf { it.isNotBlank() }?.let { query ->
                    Text(
                        query,
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        style = MaterialTheme.typography.bodyMedium
                    )
                }
                Text(
                    preview,
                    color = OnSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium
                )
            }
        }
    }
}
