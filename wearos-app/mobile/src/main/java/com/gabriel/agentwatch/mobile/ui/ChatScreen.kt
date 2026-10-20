package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.approval.SentAnswer
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.approval.isAwaitingUpdate
import com.gabriel.agentwatch.approval.needsConfirmation
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.ChatEntry
import com.gabriel.agentwatch.mobile.ui.logic.chatDay
import com.gabriel.agentwatch.mobile.ui.logic.chatEntries
import com.gabriel.agentwatch.mobile.ui.logic.chatStamp
import com.gabriel.agentwatch.mobile.ui.logic.replyToSend
import com.gabriel.agentwatch.mobile.ui.logic.unmatchedPending
import com.gabriel.agentwatch.mobile.ui.theme.Blue
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.mobile.ui.theme.Surface as SurfaceColor
import com.gabriel.agentwatch.mobile.ui.theme.SurfaceHigh
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.logic.shownStatus
import com.gabriel.agentwatch.ui.logic.waitingOnBackground
import com.gabriel.agentwatch.util.readerBlocks
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.LocalDate

private val BUBBLE_MAX_WIDTH = 340.dp

/**
 * One agent's conversation, WhatsApp-style: the turns it has finished (question right, answer left,
 * markdown), the prompt waiting as an inline card with its real options, the agent's progress, and a
 * composer at the bottom.
 *
 * Approvals follow the watch's rules (wearos-app/ARCHITECTURE.md §4b): roles decide the buttons,
 * never positions; the green Allow only ever maps to `allow_once`; `allow_always` asks for
 * confirmation; an `unknown` prompt shows its screen tail and offers only Cancel. Commands carry the
 * `state_change_seq` and prompt `fingerprint` shown at tap time, and the bridge re-validates them.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(agent: AgentState, hosts: List<HostInfo>, onBack: () -> Unit) {
    val context = LocalContext.current
    val seen = remember(context) { ChatSeen(context) }
    val scope = rememberCoroutineScope()
    val history = rememberPaneHistory(agent.key)
    val pending = ChatOutbox.pending(agent.key)
    val openPending = remember(history, pending) { unmatchedPending(history, pending) }

    var busy by remember { mutableStateOf(false) }
    var answerError by remember { mutableStateOf<String?>(null) }
    var sendError by remember { mutableStateOf<String?>(null) }
    var sent by remember { mutableStateOf<SentAnswer?>(null) }
    var draft by rememberSaveable(agent.key.token) { mutableStateOf("") }
    var confirmAlways by remember { mutableStateOf<PromptOption?>(null) }

    val prompt = agent.prompt
    val locked = busy || isAwaitingUpdate(agent, sent)
    val emptyReply = stringResource(R.string.reply_empty)
    val style = statusStyle(agent.shownStatus())
    val hostName = hostNameFor(hosts, agent.host)

    // Opening the chat (and every change while it is open) is reading it.
    LaunchedEffect(agent.key, agent.state_change_seq) { seen.markSeen(agent.key, agent.state_change_seq) }
    // A message stops being "sent" once its turn arrives.
    LaunchedEffect(history, pending) {
        ChatOutbox.retain(agent.key, unmatchedPending(history, ChatOutbox.pending(agent.key)))
    }
    // An error belongs to the prompt it was about.
    LaunchedEffect(prompt?.fingerprint) { answerError = null }

    fun run(
        block: suspend () -> Result<Unit>,
        report: (String?) -> Unit,
        onOk: () -> Unit = {}
    ) {
        scope.launch {
            busy = true
            report(null)
            block().fold(
                onSuccess = { onOk() },
                onFailure = { throwable ->
                    val feedback = commandErrorFeedback(throwable)
                    report(feedback.message)
                    if (feedback.refresh) scope.launch { RelayRepository.refresh() }
                }
            )
            busy = false
        }
    }

    fun answer(option: PromptOption, waiting: PendingPrompt) {
        val seq = agent.state_change_seq
        run(
            block = { RelayRepository.answer(agent.key, option.id, seq, waiting.fingerprint) },
            report = { answerError = it }
        ) {
            sent = SentAnswer(agent.pane_id, seq, waiting.fingerprint, agent.host)
        }
    }

    fun cancelPrompt(waiting: PendingPrompt) {
        val seq = agent.state_change_seq
        run(
            block = { RelayRepository.cancel(agent.key, seq, waiting.fingerprint) },
            report = { answerError = it }
        ) {
            sent = SentAnswer(agent.pane_id, seq, waiting.fingerprint, agent.host)
        }
    }

    fun send() {
        if (busy || prompt != null) return
        val text = replyToSend(draft)
        if (text == null) {
            sendError = emptyReply
            return
        }
        run(
            block = { RelayRepository.prompt(agent.key, text, agent.state_change_seq) },
            report = { sendError = it }
        ) {
            ChatOutbox.add(agent.key, text)
            draft = ""
        }
    }

    val entries = remember(history, prompt, agent.status, openPending) {
        chatEntries(
            history = history,
            hasPrompt = prompt != null,
            working = agent.status == "working",
            pending = openPending
        )
    }
    val info = agentInfoLines(agent)
    val itemCount = entries.size + if (info.isEmpty()) 0 else 1

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(
                            text = agent.label.ifBlank { agent.agent },
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis
                        )
                        Text(
                            text = listOfNotNull(
                                stringResource(style.label).takeIf { it.isNotBlank() },
                                hostName
                            ).joinToString(" · "),
                            style = MaterialTheme.typography.labelSmall,
                            color = style.accent,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis
                        )
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back))
                    }
                }
            )
        },
        bottomBar = {
            Composer(
                draft = draft,
                onDraftChange = { draft = it; sendError = null },
                busy = busy,
                waiting = prompt != null,
                error = sendError,
                onSend = ::send
            )
        }
    ) { padding ->
        if (entries.isEmpty() && info.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(stringResource(R.string.chat_no_messages), color = OnSurfaceVariant)
            }
        } else {
            Conversation(
                entries = entries,
                info = info,
                agent = agent,
                prompt = prompt,
                locked = locked,
                answerError = answerError,
                itemCount = itemCount,
                modifier = Modifier.fillMaxSize().padding(padding),
                onOption = { option ->
                    if (option.needsConfirmation()) confirmAlways = option
                    else prompt?.let { answer(option, it) }
                },
                onCancel = { prompt?.let { cancelPrompt(it) } }
            )
        }
    }

    confirmAlways?.let { option ->
        AlertDialog(
            onDismissRequest = { confirmAlways = null },
            title = { Text(stringResource(R.string.confirm_always_title)) },
            text = { Text(stringResource(R.string.confirm_always_text)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmAlways = null
                    prompt?.let { answer(option, it) }
                }) { Text(stringResource(R.string.confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmAlways = null }) { Text(stringResource(R.string.cancel_prompt)) }
            }
        )
    }
}

@Composable
private fun Conversation(
    entries: List<ChatEntry>,
    info: List<String>,
    agent: AgentState,
    prompt: PendingPrompt?,
    locked: Boolean,
    answerError: String?,
    itemCount: Int,
    modifier: Modifier,
    onOption: (PromptOption) -> Unit,
    onCancel: () -> Unit
) {
    val listState = rememberLazyListState()
    var stickToBottom by remember { mutableStateOf(true) }

    // Follow new messages only while the user is at the end, like a messaging app.
    LaunchedEffect(listState) {
        snapshotFlow { listState.isScrollInProgress }.collect { scrolling ->
            if (scrolling) {
                val layout = listState.layoutInfo
                val last = layout.visibleItemsInfo.lastOrNull()?.index ?: -1
                stickToBottom = last >= layout.totalItemsCount - 2
            }
        }
    }
    LaunchedEffect(itemCount) {
        if (itemCount > 0 && stickToBottom) listState.animateScrollToItem(itemCount - 1)
    }

    LazyColumn(
        state = listState,
        modifier = modifier,
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        if (info.isNotEmpty()) {
            item(key = "info") {
                AgentInfo(info)
                Spacer(Modifier.height(4.dp))
            }
        }
        entries.forEachIndexed { index, entry ->
            item(key = "entry-$index") {
                when (entry) {
                    is ChatEntry.Day -> DaySeparator(entry.date)
                    is ChatEntry.Turn -> TurnRow(entry.item)
                    is ChatEntry.Pending -> SentBubble(entry.text)
                    ChatEntry.Prompt -> prompt?.let { waiting ->
                        Bubble(mine = false, container = MaterialTheme.colorScheme.surfaceVariant) {
                            PromptPanel(
                                prompt = waiting,
                                locked = locked,
                                onOption = onOption,
                                onCancel = onCancel
                            )
                            answerError?.let {
                                Text(
                                    text = it,
                                    color = MaterialTheme.colorScheme.error,
                                    style = MaterialTheme.typography.bodySmall
                                )
                            }
                        }
                    }
                    ChatEntry.Working -> WorkingBubble(
                        waiting = agent.waitingOnBackground() > 0
                    )
                }
            }
        }
    }
}

@Composable
private fun TurnRow(item: HistoryItem) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        item.query?.takeIf { it.isNotBlank() }?.let { QueryBubble(it) }
        AnswerBubble(item)
    }
}

@Composable
private fun QueryBubble(text: String) {
    Bubble(mine = true, container = MaterialTheme.colorScheme.primaryContainer) {
        Text(text, style = MaterialTheme.typography.bodyLarge)
    }
}

@Composable
private fun AnswerBubble(item: HistoryItem) {
    Bubble(mine = false, container = SurfaceHigh) {
        if (item.source == "screen") {
            // A terminal capture keeps its own spacing and never reflows; the bubble scrolls sideways.
            Box(Modifier.horizontalScroll(rememberScrollState())) {
                Text(
                    text = item.response,
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.bodySmall
                )
            }
        } else {
            SelectionContainer { MarkdownBlocks(remember(item.id) { readerBlocks(item.response, item.source) }) }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = chatStampText(chatStamp(item.completed_at, Instant.now())),
                style = MaterialTheme.typography.labelSmall,
                color = OnSurfaceVariant
            )
            if (item.source == "screen") {
                Text(
                    text = stringResource(R.string.chat_screen_source),
                    style = MaterialTheme.typography.labelSmall,
                    color = OnSurfaceVariant
                )
            }
        }
    }
}

@Composable
private fun SentBubble(text: String) {
    Bubble(mine = true, container = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.55f)) {
        Text(text, style = MaterialTheme.typography.bodyLarge)
        Text(
            text = stringResource(R.string.chat_sent),
            style = MaterialTheme.typography.labelSmall,
            color = OnSurfaceVariant
        )
    }
}

@Composable
private fun WorkingBubble(waiting: Boolean) {
    Bubble(mine = false, container = SurfaceHigh) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            CircularProgressIndicator(modifier = Modifier.size(14.dp), strokeWidth = 2.dp, color = Blue)
            Text(
                text = stringResource(if (waiting) R.string.chat_waiting_background else R.string.chat_working),
                style = MaterialTheme.typography.bodyMedium
            )
        }
    }
}

@Composable
private fun DaySeparator(date: LocalDate) {
    Box(Modifier.fillMaxWidth().padding(vertical = 8.dp), contentAlignment = Alignment.Center) {
        Text(
            text = chatDayText(chatDay(date, LocalDate.now())),
            style = MaterialTheme.typography.labelSmall,
            color = OnSurfaceVariant,
            modifier = Modifier
                .background(SurfaceColor, RoundedCornerShape(50))
                .padding(horizontal = 10.dp, vertical = 3.dp)
        )
    }
}

@Composable
private fun AgentInfo(lines: List<String>) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .background(SurfaceColor)
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp)
    ) {
        lines.forEach { Text(it, style = MaterialTheme.typography.bodySmall, color = OnSurfaceVariant) }
    }
}

/** What the chat says about the agent itself: where it runs. Empty when there is nothing to add. */
@Composable
private fun agentInfoLines(agent: AgentState): List<String> = buildList {
    agent.workspace?.takeIf { it.isNotBlank() }?.let { add(stringResource(R.string.chat_workspace, it)) }
    agent.cwd?.takeIf { it.isNotBlank() }?.let { add(stringResource(R.string.chat_folder, it)) }
    agent.title?.takeIf { it.isNotBlank() && it != agent.label }?.let { add(it) }
    backgroundLine(agent)?.let { add(it) }
}

@Composable
private fun Bubble(
    mine: Boolean,
    container: Color,
    content: @Composable ColumnScope.() -> Unit
) {
    Box(
        modifier = Modifier.fillMaxWidth(),
        contentAlignment = if (mine) Alignment.CenterEnd else Alignment.CenterStart
    ) {
        Column(
            modifier = Modifier
                .widthIn(max = BUBBLE_MAX_WIDTH)
                .clip(bubbleShape(mine))
                .background(container)
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
            content = content
        )
    }
}

private fun bubbleShape(mine: Boolean) = RoundedCornerShape(
    topStart = 16.dp,
    topEnd = 16.dp,
    bottomStart = if (mine) 16.dp else 4.dp,
    bottomEnd = if (mine) 4.dp else 16.dp
)

@Composable
private fun Composer(
    draft: String,
    onDraftChange: (String) -> Unit,
    busy: Boolean,
    waiting: Boolean,
    error: String?,
    onSend: () -> Unit
) {
    Surface(color = SurfaceColor, shadowElevation = 8.dp) {
        Column(Modifier.fillMaxWidth().imePadding().navigationBarsPadding()) {
            error?.let {
                Text(
                    text = it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)
                )
            }
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
                verticalAlignment = Alignment.Bottom
            ) {
                OutlinedTextField(
                    value = draft,
                    onValueChange = onDraftChange,
                    modifier = Modifier.weight(1f),
                    placeholder = {
                        Text(
                            stringResource(
                                if (waiting) R.string.chat_answer_first else R.string.chat_composer_hint
                            )
                        )
                    },
                    maxLines = 5,
                    shape = RoundedCornerShape(24.dp),
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                    keyboardActions = KeyboardActions(onSend = { onSend() })
                )
                Spacer(Modifier.width(8.dp))
                FilledIconButton(
                    onClick = onSend,
                    enabled = !busy && !waiting && draft.isNotBlank(),
                    modifier = Modifier.size(48.dp)
                ) {
                    Icon(
                        Icons.AutoMirrored.Filled.Send,
                        contentDescription = stringResource(R.string.send)
                    )
                }
            }
        }
    }
}
