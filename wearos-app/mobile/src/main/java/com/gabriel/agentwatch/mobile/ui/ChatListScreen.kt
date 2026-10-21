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
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.chatStamp
import com.gabriel.agentwatch.mobile.ui.logic.isUnread
import com.gabriel.agentwatch.mobile.ui.logic.lastActivity
import com.gabriel.agentwatch.mobile.ui.logic.needsAnswer
import com.gabriel.agentwatch.mobile.ui.logic.previewLine
import com.gabriel.agentwatch.mobile.ui.logic.previewText
import com.gabriel.agentwatch.mobile.ui.logic.unmatchedPending
import com.gabriel.agentwatch.mobile.ui.theme.Amber
import com.gabriel.agentwatch.mobile.ui.theme.Blue
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.mobile.ui.theme.OutlineVariant
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.ui.logic.ListNotice
import com.gabriel.agentwatch.update.UpdatePhase
import com.gabriel.agentwatch.update.UpdateState
import com.gabriel.agentwatch.ui.logic.listStatus
import com.gabriel.agentwatch.ui.logic.shownStatus
import java.time.Instant

/**
 * The home screen: every agent as a chat, most recent first, with the chats that need an answer
 * above the rest. With several hosts, chips filter by machine (`docs/STATUS.md` — the phone groups
 * by host instead of the watch's per-host pages).
 *
 * Each row is one conversation: the agent's mark, its name, the last thing it said (or the prompt
 * waiting), when, what it is doing, and the machine it runs on. A blue dot marks a chat that changed
 * since it was last opened ([ChatSeen]).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatListScreen(
    state: UiState,
    update: UpdateState,
    onOpen: (AgentState) -> Unit,
    onHistory: () -> Unit,
    onNewSession: () -> Unit,
    onRefresh: () -> Unit,
    onUpdate: () -> Unit,
    onRetryUpdate: () -> Unit,
    onCancelUpdate: () -> Unit,
    onInstallUpdate: () -> Unit,
    onDismissUpdate: () -> Unit
) {
    val context = LocalContext.current
    val seen = remember(context) { ChatSeen(context) }
    var hostFilter by rememberSaveable { mutableStateOf<String?>(null) }

    // A host the relay no longer lists cannot stay selected.
    LaunchedEffect(state.hosts) {
        if (hostFilter != null && state.hosts.none { it.id == hostFilter }) hostFilter = null
    }
    // Agents the phone lists for the first time start read; later changes dot until the chat is opened.
    LaunchedEffect(state.agents) { seen.prime(state.agents) }

    val visible = state.agents.filter { hostFilter == null || it.host == hostFilter }
    val needsYou = visible
        .filter { it.needsAnswer() }
        .sortedByDescending { it.lastActivity(state.history) }
    val chats = visible
        .filterNot { it.needsAnswer() }
        .sortedByDescending { it.lastActivity(state.history) }

    val status = if (hostFilter == null) {
        listStatus(state)
    } else {
        val host = state.hosts.firstOrNull { it.id == hostFilter }
        listStatus(state, host?.online ?: false, host?.herdr_online ?: false, visible.isNotEmpty())
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.chats_title)) },
                actions = {
                    IconButton(onClick = onNewSession) {
                        Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.new_session))
                    }
                    IconButton(onClick = onRefresh) {
                        Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh))
                    }
                    TextButton(onClick = onHistory) { Text(stringResource(R.string.history)) }
                }
            )
        }
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            UpdateBanner(
                update = update,
                onUpdate = onUpdate,
                onRetry = onRetryUpdate,
                onCancel = onCancelUpdate,
                onInstall = onInstallUpdate,
                onDismiss = onDismissUpdate
            )
            status.notice?.let { Notice(it) }
            if (state.hosts.size > 1) {
                HostChips(hosts = state.hosts, selected = hostFilter, onSelect = { hostFilter = it })
            }
            when {
                visible.isEmpty() && state.agents.isEmpty() -> EmptyList(stringResource(R.string.no_agents))
                visible.isEmpty() -> EmptyList(stringResource(R.string.no_agents_host))
                else -> LazyColumn(modifier = Modifier.fillMaxSize()) {
                    if (needsYou.isNotEmpty()) {
                        item(key = "header-needs-you") {
                            SectionHeader(stringResource(R.string.section_needs_you), needsYou.size)
                        }
                        items(needsYou, key = { "need-${it.key.token}" }) { agent ->
                            ChatRow(
                                agent = agent,
                                history = state.history,
                                hostName = hostNameFor(state.hosts, agent.host),
                                unread = isUnread(agent, seen.seen(agent.key)),
                                dimmed = status.dimmed,
                                onClick = { onOpen(agent) }
                            )
                        }
                        if (chats.isNotEmpty()) {
                            item(key = "header-chats") {
                                SectionHeader(stringResource(R.string.chats_section), chats.size)
                            }
                        }
                    }
                    items(chats, key = { "chat-${it.key.token}" }) { agent ->
                        ChatRow(
                            agent = agent,
                            history = state.history,
                            hostName = hostNameFor(state.hosts, agent.host),
                            unread = isUnread(agent, seen.seen(agent.key)),
                            dimmed = status.dimmed,
                            onClick = { onOpen(agent) }
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun HostChips(
    hosts: List<HostInfo>,
    selected: String?,
    onSelect: (String?) -> Unit
) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        item(key = "all") {
            FilterChip(
                selected = selected == null,
                onClick = { onSelect(null) },
                label = { Text(stringResource(R.string.host_all)) }
            )
        }
        items(hosts, key = { it.id }) { host ->
            FilterChip(
                selected = selected == host.id,
                onClick = { onSelect(host.id) },
                label = {
                    Text(
                        text = host.name.ifBlank { host.id },
                        color = if (host.online) MaterialTheme.colorScheme.onSurface else OnSurfaceVariant
                    )
                }
            )
        }
    }
}

@Composable
private fun ChatRow(
    agent: AgentState,
    history: List<HistoryItem>,
    hostName: String?,
    unread: Boolean,
    dimmed: Boolean,
    onClick: () -> Unit
) {
    val style = statusStyle(agent.shownStatus())
    val background = backgroundLine(agent)
    val statusLine = listOfNotNull(
        stringResource(style.label).takeIf { it.isNotBlank() },
        hostName,
        background
    ).joinToString(" · ")
    val preview = rowPreview(agent, history)
    val activity = agent.lastActivity(history).takeIf { it != Instant.EPOCH }
    val time = chatStampText(chatStamp(activity, Instant.now()))
    val unreadLabel = stringResource(R.string.chat_unread)

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .alpha(if (dimmed) 0.5f else 1f)
            .clickable(onClick = onClick)
            .padding(start = 16.dp, end = 16.dp, top = 10.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        AgentAvatar(agent)
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = agent.label.ifBlank { agent.agent },
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = time,
                    style = MaterialTheme.typography.labelSmall,
                    color = if (unread) Blue else OnSurfaceVariant
                )
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = preview,
                    style = MaterialTheme.typography.bodyMedium,
                    color = OnSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f)
                )
                if (unread) {
                    Spacer(Modifier.width(8.dp))
                    Box(
                        Modifier
                            .size(10.dp)
                            .background(Blue, CircleShape)
                            .semantics { contentDescription = unreadLabel }
                    )
                }
            }
            Text(
                text = statusLine,
                style = MaterialTheme.typography.labelSmall,
                color = style.accent,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
    }
    HorizontalDivider(
        modifier = Modifier.padding(start = 78.dp, end = 16.dp),
        color = OutlineVariant.copy(alpha = 0.5f)
    )
}

/** The row's second line: the prompt when one waits, else the last thing said, else what the agent is doing. */
@Composable
private fun rowPreview(agent: AgentState, history: List<HistoryItem>): String {
    val prompt = agent.prompt
    if (prompt != null) {
        return prompt.title.ifBlank { stringResource(R.string.chat_waiting_answer) }
    }
    ChatOutbox.pending(agent.key).let { sent ->
        unmatchedPending(history, sent).lastOrNull()?.let { text ->
            previewText(text)?.let { return it }
        }
    }
    val last = history.firstOrNull { it.key == agent.key }
    if (last != null) {
        return previewLine(last) ?: stringResource(R.string.chat_no_messages)
    }
    return when (agent.status) {
        "working" -> stringResource(R.string.chat_working)
        else -> stringResource(R.string.chat_no_messages)
    }
}

@Composable
private fun SectionHeader(title: String, count: Int) {
    Text(
        text = "$title · $count",
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 4.dp)
    )
}

@Composable
private fun EmptyList(text: String) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Text(text, color = OnSurfaceVariant)
    }
}

@Composable
private fun UpdateBanner(
    update: UpdateState,
    onUpdate: () -> Unit,
    onRetry: () -> Unit,
    onCancel: () -> Unit,
    onInstall: () -> Unit,
    onDismiss: () -> Unit
) {
    if (update.phase == UpdatePhase.IDLE || update.phase == UpdatePhase.CHECKING || update.phase == UpdatePhase.UP_TO_DATE) {
        return
    }
    val info = update.info
    val version = info?.version_name?.takeIf { it.isNotBlank() } ?: info?.version_code?.toString().orEmpty()

    Card(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
        Column(
            modifier = Modifier.padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp)
        ) {
            Text(stringResource(R.string.update_title), style = MaterialTheme.typography.titleSmall)
            when (update.phase) {
                UpdatePhase.AVAILABLE -> Text(stringResource(R.string.update_available, version))
                UpdatePhase.DOWNLOADING -> {
                    Text(stringResource(R.string.update_downloading, (update.progress * 100).toInt()))
                    LinearProgressIndicator(
                        progress = { update.progress.coerceIn(0f, 1f) },
                        modifier = Modifier.fillMaxWidth()
                    )
                }
                UpdatePhase.READY -> Text(stringResource(R.string.update_ready, version))
                UpdatePhase.NEEDS_PERMISSION -> Text(stringResource(R.string.update_permission))
                UpdatePhase.FAILED -> Text(updateErrorText(update.errorCode))
                else -> Unit
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                when (update.phase) {
                    UpdatePhase.AVAILABLE -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        Button(onClick = onUpdate) { Text(stringResource(R.string.update_action)) }
                    }
                    UpdatePhase.DOWNLOADING -> {
                        TextButton(onClick = onCancel) { Text(stringResource(R.string.update_cancel)) }
                    }
                    UpdatePhase.READY -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        Button(onClick = onInstall) { Text(stringResource(R.string.update_install)) }
                    }
                    UpdatePhase.NEEDS_PERMISSION -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        Button(onClick = onInstall) { Text(stringResource(R.string.update_open_settings)) }
                    }
                    UpdatePhase.FAILED -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        Button(onClick = onRetry) { Text(stringResource(R.string.update_retry)) }
                    }
                    else -> Unit
                }
            }
        }
    }
}

@Composable
private fun updateErrorText(code: String?): String = when (code) {
    "bad_download", "bad_update" -> stringResource(R.string.update_error_download)
    "install_unavailable", "install_settings_unavailable" -> stringResource(R.string.update_error_install)
    "not_paired", "unauthorized" -> stringResource(R.string.update_error_auth)
    "network", "timeout" -> stringResource(R.string.update_error_network)
    else -> stringResource(R.string.update_error_generic)
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
