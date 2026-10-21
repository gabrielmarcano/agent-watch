package com.gabriel.agentwatch.mobile.ui

import androidx.compose.animation.AnimatedVisibilityScope
import androidx.compose.animation.SharedTransitionScope
import androidx.compose.foundation.background
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Done
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.ChatFilter
import com.gabriel.agentwatch.mobile.ui.logic.chatStamp
import com.gabriel.agentwatch.mobile.ui.logic.isUnread
import com.gabriel.agentwatch.mobile.ui.logic.lastActivity
import com.gabriel.agentwatch.mobile.ui.logic.matches
import com.gabriel.agentwatch.mobile.ui.logic.matchesQuery
import com.gabriel.agentwatch.mobile.ui.logic.needsAnswer
import com.gabriel.agentwatch.mobile.ui.logic.previewLine
import com.gabriel.agentwatch.mobile.ui.logic.previewText
import com.gabriel.agentwatch.mobile.ui.logic.unmatchedPending
import com.gabriel.agentwatch.mobile.ui.logic.unreadCount
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.mobile.ui.theme.warningAccent
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
import kotlinx.coroutines.launch
import java.time.Instant

/**
 * The home screen: every agent as a chat, most recent first, the way a messaging app lists its
 * conversations. With several hosts, one chip filters by machine (the phone groups by host instead
 * of the watch's per-host pages).
 *
 * Each row is one conversation: the agent's mark, its name, the last thing it said (or the prompt
 * waiting), when, what it is doing, and the machine it runs on. A badge counts the turns that
 * arrived since the chat was last read ([ChatSeen]); a swipe marks it read.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatListScreen(
    state: UiState,
    update: UpdateState,
    hostFilter: String?,
    onHostFilter: (String?) -> Unit,
    mutedChats: MutedChats,
    sharedScope: SharedTransitionScope? = null,
    visibilityScope: AnimatedVisibilityScope? = null,
    onOpen: (AgentState) -> Unit,
    onNewSession: () -> Unit,
    onRefresh: suspend () -> Unit,
    onMarkRead: (AgentState) -> Unit,
    onUpdate: () -> Unit,
    onRetryUpdate: () -> Unit,
    onCancelUpdate: () -> Unit,
    onInstallUpdate: () -> Unit,
    onDismissUpdate: () -> Unit
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val seen = remember(context) { ChatSeen(context) }
    var filter by rememberSaveable { mutableStateOf(ChatFilter.ALL) }
    var query by rememberSaveable { mutableStateOf("") }
    var refreshing by remember { mutableStateOf(false) }
    var refreshError by remember { mutableStateOf<Throwable?>(null) }

    // A host the relay no longer lists cannot stay selected.
    LaunchedEffect(state.hosts) {
        if (hostFilter != null && state.hosts.none { it.id == hostFilter }) onHostFilter(null)
    }
    // Agents the phone lists for the first time start read; later turns count until the chat is read.
    LaunchedEffect(state.agents) { seen.prime(state.agents) }

    val onMachine = state.agents.filter { hostFilter == null || it.host == hostFilter }
    val chats = onMachine
        .filter { filter.matches(it) }
        .filter { matchesQuery(it, state.history, query) }
        .sortedByDescending { it.lastActivity(state.history) }

    val status = if (hostFilter == null) {
        listStatus(state)
    } else {
        val host = state.hosts.firstOrNull { it.id == hostFilter }
        listStatus(state, host?.online ?: false, host?.herdr_online ?: false, onMachine.isNotEmpty())
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.chats_title)) }
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = onNewSession) {
                Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.new_session))
            }
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
            SearchField(query = query, onQueryChange = { query = it })
            FilterRow(
                filter = filter,
                onFilter = { filter = it },
                counts = ChatFilter.entries.associateWith { f ->
                    onMachine.count { f.matches(it) }
                },
                hosts = state.hosts,
                selectedHost = hostFilter,
                onSelectHost = onHostFilter
            )
            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = {
                    scope.launch {
                        refreshing = true
                        try {
                            onRefresh()
                            refreshError = null
                        } catch (throwable: Throwable) {
                            refreshError = throwable
                        } finally {
                            refreshing = false
                        }
                    }
                }
            ) {
                when {
                    state.agents.isEmpty() -> EmptyList(
                        text = stringResource(R.string.no_agents),
                        action = stringResource(R.string.chat_empty_start),
                        onAction = onNewSession
                    )
                    chats.isEmpty() && query.isNotBlank() -> EmptyList(stringResource(R.string.chat_empty_search))
                    chats.isEmpty() && hostFilter != null -> EmptyList(stringResource(R.string.no_agents_host))
                    chats.isEmpty() -> EmptyList(stringResource(R.string.chat_empty_filter))
                    else -> LazyColumn(
                        modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(bottom = 88.dp)
                    ) {
                        items(chats, key = { "chat-${it.key.token}" }) { agent ->
                            ChatRow(
                                agent = agent,
                                history = state.history,
                                hostName = hostNameFor(state.hosts, agent.host),
                                unread = unreadCount(agent, state.history, seen.seenAt(agent.key)),
                                changed = isUnread(agent, seen.seen(agent.key)),
                                muted = mutedChats.isMuted(agent.key.token),
                                dimmed = status.dimmed,
                                onClick = { onOpen(agent) },
                                onMarkRead = { onMarkRead(agent) },
                                sharedScope = sharedScope,
                                visibilityScope = visibilityScope
                            )
                        }
                    }
                }
            }
        }
    }
}

/** The search field, pinned above the list: it filters the chats by name, machine or last message. */
@Composable
private fun SearchField(query: String, onQueryChange: (String) -> Unit) {
    OutlinedTextField(
        value = query,
        onValueChange = onQueryChange,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 4.dp),
        placeholder = { Text(stringResource(R.string.search_chats)) },
        leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
        trailingIcon = {
            if (query.isNotEmpty()) {
                IconButton(onClick = { onQueryChange("") }) {
                    Icon(Icons.Filled.Close, contentDescription = stringResource(R.string.search_clear))
                }
            }
        },
        singleLine = true,
        shape = RoundedCornerShape(24.dp)
    )
}

/** The filters (All, Needs you, Working, Done) and the machine, in one scrolling row. */
@Composable
private fun FilterRow(
    filter: ChatFilter,
    onFilter: (ChatFilter) -> Unit,
    counts: Map<ChatFilter, Int>,
    hosts: List<HostInfo>,
    selectedHost: String?,
    onSelectHost: (String?) -> Unit
) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        items(ChatFilter.entries.toList(), key = { it.name }) { f ->
            val count = counts[f] ?: 0
            FilterChip(
                selected = filter == f,
                onClick = { onFilter(f) },
                label = {
                    Text(
                        text = when (f) {
                            ChatFilter.ALL -> stringResource(R.string.filter_all)
                            ChatFilter.NEEDS_YOU -> stringResource(R.string.section_needs_you)
                            ChatFilter.WORKING -> stringResource(R.string.status_working)
                            ChatFilter.DONE -> stringResource(R.string.status_done)
                        } + if (f == ChatFilter.NEEDS_YOU && count > 0) " · $count" else ""
                    )
                }
            )
        }
        if (hosts.size > 1) {
            item(key = "machine") {
                MachineChip(hosts = hosts, selected = selectedHost, onSelect = onSelectHost)
            }
        }
    }
}

/** One chip for the machine, with the list of hosts under it: the filter row stays one line. */
@Composable
private fun MachineChip(hosts: List<HostInfo>, selected: String?, onSelect: (String?) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val name = hosts.firstOrNull { it.id == selected }?.name?.ifBlank { null }
        ?: hosts.firstOrNull { it.id == selected }?.id
        ?: stringResource(R.string.filter_all)
    Box {
        FilterChip(
            selected = selected != null,
            onClick = { open = true },
            label = { Text(stringResource(R.string.filter_machine, name)) }
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            DropdownMenuItem(
                text = { Text(stringResource(R.string.filter_all)) },
                onClick = {
                    onSelect(null)
                    open = false
                }
            )
            hosts.forEach { host ->
                DropdownMenuItem(
                    text = {
                        Text(
                            text = host.name.ifBlank { host.id },
                            color = if (host.online) {
                                MaterialTheme.colorScheme.onSurface
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            }
                        )
                    },
                    onClick = {
                        onSelect(host.id)
                        open = false
                    }
                )
            }
        }
    }
}

@Composable
private fun ChatRow(
    agent: AgentState,
    history: List<HistoryItem>,
    hostName: String?,
    unread: Int,
    changed: Boolean,
    muted: Boolean,
    dimmed: Boolean,
    onClick: () -> Unit,
    onMarkRead: () -> Unit,
    sharedScope: SharedTransitionScope? = null,
    visibilityScope: AnimatedVisibilityScope? = null
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

    // A swipe to the right marks the chat read; the row springs back, it is never dismissed.
    val scope = rememberCoroutineScope()
    val offset = remember { Animatable(0f) }
    val threshold = with(LocalDensity.current) { 96.dp.toPx() }
    val dragState = rememberDraggableState { delta ->
        scope.launch { offset.snapTo((offset.value + delta).coerceIn(0f, threshold * 2f)) }
    }

    Box {
        // What the swipe shows behind the row.
        Row(
            modifier = Modifier
                .matchParentSize()
                .background(MaterialTheme.colorScheme.primaryContainer)
                .padding(start = 24.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                Icons.Filled.Done,
                contentDescription = stringResource(R.string.mark_read),
                tint = MaterialTheme.colorScheme.onPrimaryContainer
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = stringResource(R.string.mark_read),
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onPrimaryContainer
            )
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .alpha(if (dimmed) 0.5f else 1f)
                .background(MaterialTheme.colorScheme.background)
                .graphicsLayer { translationX = offset.value }
                .draggable(
                    orientation = Orientation.Horizontal,
                    state = dragState,
                    onDragStopped = {
                        if (offset.value >= threshold) onMarkRead()
                        scope.launch { offset.animateTo(0f, spring(dampingRatio = Spring.DampingRatioNoBouncy)) }
                    }
                )
                .clickable(onClick = onClick)
                .padding(start = 16.dp, end = 16.dp, top = 10.dp, bottom = 10.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            AgentAvatar(agent, modifier = sharedFace(agent.key.token, sharedScope, visibilityScope))
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = agent.label.ifBlank { agent.agent },
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = if (unread > 0) FontWeight.Bold else FontWeight.Normal,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f)
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = time,
                        style = MaterialTheme.typography.labelSmall,
                        color = if (changed) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        }
                    )
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = preview,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f)
                    )
                    if (unread > 0) {
                        Spacer(Modifier.width(8.dp))
                        UnreadBadge(unread)
                    }
                }
                Text(
                    text = if (muted) {
                        stringResource(R.string.chat_muted)
                    } else {
                        statusLine
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = if (muted) {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    } else {
                        style.accent
                    },
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
    }
    HorizontalDivider(
        modifier = Modifier.padding(start = 78.dp, end = 16.dp),
        color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f)
    )
}

/** The badge with the number of unread turns, a messaging app's count instead of a dot. */
@Composable
private fun UnreadBadge(count: Int) {
    val label = stringResource(R.string.chat_unread_count, count)
    Box(
        modifier = Modifier
            .defaultMinSize(minWidth = 20.dp, minHeight = 20.dp)
            .background(MaterialTheme.colorScheme.primary, CircleShape)
            .padding(horizontal = 6.dp)
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = if (count > 99) "99+" else count.toString(),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onPrimary
        )
    }
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
private fun EmptyList(text: String, action: String? = null, onAction: (() -> Unit)? = null) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            Text(text, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (action != null && onAction != null) {
                Button(onClick = onAction) { Text(action) }
            }
        }
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
                        TextButton(onClick = onRetry) { Text(stringResource(R.string.update_retry)) }
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
    Box(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Text(
            text = when (notice) {
                ListNotice.CONNECTING -> stringResource(R.string.notice_connecting)
                ListNotice.RELAY_UNREACHABLE -> stringResource(R.string.notice_relay_unreachable)
                ListNotice.MAC_OFFLINE -> stringResource(R.string.notice_device_offline)
                ListNotice.HERDR_STOPPED -> stringResource(R.string.notice_herdr_stopped)
            },
            color = warningAccent(),
            style = MaterialTheme.typography.labelLarge
        )
    }
}
