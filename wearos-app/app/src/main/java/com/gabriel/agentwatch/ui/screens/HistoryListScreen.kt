package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.layout.Column
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
            if (agentLabel.isNullOrBlank()) {
                ListHeader(
                    modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                    transformation = SurfaceTransformation(spec)
                ) {
                    Text(stringResource(R.string.history_title), maxLines = 1)
                }
            } else {
                // The session's title gets its own lines: one line after "History" cut it at two words.
                Column(
                    transformedItem(spec).padding(start = 18.dp, end = 18.dp, top = 6.dp, bottom = 4.dp),
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    Text(stringResource(R.string.history_title), style = MaterialTheme.typography.titleMedium, maxLines = 1)
                    Text(
                        agentLabel,
                        color = OnSurfaceVariant,
                        style = MaterialTheme.typography.bodySmall,
                        textAlign = TextAlign.Center,
                        maxLines = 3,
                        overflow = TextOverflow.Ellipsis
                    )
                }
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
            // Across all panes, the session's title names the card and the agent's logo leads the age.
            // One pane's history already names its session in the header: what was asked names the card.
            val allPanes = paneId.isNullOrBlank()
            val showAgent = entry.agent.isNotBlank() && allPanes
            val query = entry.query?.takeIf { it.isNotBlank() }
            val age = ageText(entry.completed_at)
            TitleCard(
                onClick = { onSelectHistoryItem(entry) },
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                title = {
                    Text(
                        if (allPanes) entry.label.ifBlank { entry.agent } else query ?: age,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                },
                subtitle = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (showAgent) {
                            AgentLogo(entry.agent, OnSurfaceVariant, 12.sp)
                            Spacer(Modifier.width(4.dp))
                        }
                        // With no query, one pane's card already shows the age as its title.
                        if (allPanes || query != null) {
                            Text(age, color = OnSurfaceVariant, style = MaterialTheme.typography.labelSmall, maxLines = 1)
                        }
                    }
                }
            ) {
                if (allPanes && query != null) {
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
