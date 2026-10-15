package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.approval.SentAnswer
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.approval.isAwaitingUpdate
import com.gabriel.agentwatch.approval.morePermissionOptions
import com.gabriel.agentwatch.approval.needsConfirmation
import com.gabriel.agentwatch.approval.permissionButtons
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.replyToSend
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.logic.ageOf
import com.gabriel.agentwatch.ui.logic.headTailPreview
import com.gabriel.agentwatch.ui.logic.shownStatus
import com.gabriel.agentwatch.ui.logic.tailPreview
import com.gabriel.agentwatch.util.MarkdownFormatter
import kotlinx.coroutines.launch
import java.time.Instant

// A phone line fits far more than a watch's card, so the preview shows more of the reply.
private const val REPLY_HEAD_CHARS = 300
private const val REPLY_TAIL_CHARS = 500
private const val SCREEN_TAIL_LINES = 12

/**
 * One agent: what it is doing, the prompt waiting for an answer, a reply box, and its last answer.
 *
 * Approvals follow the watch's rules (wearos-app/ARCHITECTURE.md §4b): roles decide the buttons, never
 * positions; the green Allow only ever maps to `allow_once`; `allow_always` asks for confirmation; an
 * `unknown` prompt shows its screen tail and offers only Cancel. Commands carry the `state_change_seq`
 * and prompt `fingerprint` shown at tap time, and the bridge re-validates them.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AgentDetailScreen(
    agent: AgentState,
    hostName: String?,
    lastReply: HistoryItem?,
    onBack: () -> Unit,
    onReadAll: (title: String, body: String, source: String, query: String) -> Unit,
    onHistory: () -> Unit
) {
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var sent by remember { mutableStateOf<SentAnswer?>(null) }
    var reply by rememberSaveable { mutableStateOf("") }
    var confirmAlways by remember { mutableStateOf<PromptOption?>(null) }

    val prompt = agent.prompt
    val locked = busy || isAwaitingUpdate(agent, sent)
    val emptyReply = stringResource(R.string.reply_empty)

    fun run(block: suspend () -> Result<Unit>, onOk: () -> Unit = {}) {
        scope.launch {
            busy = true
            error = null
            block().fold(
                onSuccess = { onOk() },
                onFailure = { throwable ->
                    val feedback = commandErrorFeedback(throwable)
                    error = feedback.message
                    if (feedback.refresh) RelayRepository.refresh()
                }
            )
            busy = false
        }
    }

    fun answer(option: PromptOption, prompt: PendingPrompt) {
        val seq = agent.state_change_seq
        run({ RelayRepository.answer(agent.key, option.id, seq, prompt.fingerprint) }) {
            sent = SentAnswer(agent.pane_id, seq, prompt.fingerprint, agent.host)
        }
    }

    fun cancelPrompt(prompt: PendingPrompt) {
        val seq = agent.state_change_seq
        run({ RelayRepository.cancel(agent.key, seq, prompt.fingerprint) }) {
            sent = SentAnswer(agent.pane_id, seq, prompt.fingerprint, agent.host)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        text = agent.label.ifBlank { agent.agent },
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.back)
                        )
                    }
                },
                actions = {
                    TextButton(onClick = onHistory) { Text(stringResource(R.string.agent_history)) }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            AgentFacts(agent, hostName)

            if (prompt != null) {
                val onOption: (PromptOption) -> Unit = { option ->
                    if (option.needsConfirmation()) confirmAlways = option else answer(option, prompt)
                }
                PromptCard(
                    prompt = prompt,
                    locked = locked,
                    onOption = onOption,
                    onCancel = { cancelPrompt(prompt) }
                )
            }

            Card {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(R.string.reply), style = MaterialTheme.typography.titleSmall)
                    OutlinedTextField(
                        value = reply,
                        onValueChange = { reply = it; error = null },
                        placeholder = { Text(stringResource(R.string.reply_hint)) },
                        enabled = !busy,
                        minLines = 3,
                        modifier = Modifier.fillMaxWidth()
                    )
                    Button(
                        onClick = {
                            val text = replyToSend(reply)
                            if (text == null) {
                                error = emptyReply
                            } else {
                                run({ RelayRepository.prompt(agent.key, text, agent.state_change_seq) }) { reply = "" }
                            }
                        },
                        enabled = !busy,
                        modifier = Modifier.align(Alignment.End)
                    ) {
                        Text(stringResource(R.string.send))
                    }
                }
            }

            LastReplyCard(
                reply = lastReply,
                onReadAll = onReadAll
            )

            error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
            }
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
private fun AgentFacts(agent: AgentState, hostName: String?) {
    val style = statusStyle(agent.shownStatus())
    val age = ageText(ageOf(agent.updated_at, Instant.now()))
    val statusLine = listOfNotNull(
        stringResource(style.label).takeIf { it.isNotBlank() },
        age.takeIf { it.isNotBlank() }
    ).joinToString(" · ")
    val background = backgroundLine(agent)

    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(statusLine, color = style.accent, style = MaterialTheme.typography.titleSmall)
        background?.let { Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall) }
        agent.workspace?.takeIf { it.isNotBlank() }?.let {
            Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall)
        }
        agent.title?.takeIf { it.isNotBlank() && it != agent.label }?.let {
            Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall)
        }
        hostName?.let {
            Text(it, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
private fun PromptCard(
    prompt: PendingPrompt,
    locked: Boolean,
    onOption: (PromptOption) -> Unit,
    onCancel: () -> Unit
) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                text = stringResource(
                    when (prompt.kind) {
                        "permission" -> R.string.prompt_permission
                        "question" -> R.string.prompt_question
                        else -> R.string.prompt_unknown
                    }
                ),
                color = OnSurfaceVariant,
                style = MaterialTheme.typography.labelLarge
            )
            if (prompt.title.isNotBlank()) {
                Text(prompt.title, style = MaterialTheme.typography.titleMedium)
            }
            prompt.detail?.takeIf { it.isNotBlank() }?.let {
                Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
            }
            when (prompt.kind) {
                "permission" -> PermissionActions(prompt, locked, onOption, onCancel)
                "question" -> QuestionActions(prompt, locked, onOption)
                else -> UnknownPromptActions(prompt, locked, onCancel)
            }
        }
    }
}

@Composable
private fun PermissionActions(
    prompt: PendingPrompt,
    locked: Boolean,
    onOption: (PromptOption) -> Unit,
    onCancel: () -> Unit
) {
    val buttons = permissionButtons(prompt)
    val deny = buttons.deny
    val allow = buttons.allow
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        // The positive action sits on the right, and only "allow_once" ever fills it.
        if (deny != null) {
            OutlinedButton(
                onClick = { onOption(deny) },
                enabled = !locked,
                modifier = Modifier.weight(1f)
            ) { Text(deny.label.ifBlank { stringResource(R.string.deny) }) }
        } else {
            OutlinedButton(onClick = onCancel, enabled = !locked, modifier = Modifier.weight(1f)) {
                Text(stringResource(R.string.cancel_prompt))
            }
        }
        allow?.let {
            Button(onClick = { onOption(it) }, enabled = !locked, modifier = Modifier.weight(1f)) {
                Text(it.label.ifBlank { stringResource(R.string.allow) })
            }
        }
    }

    val more = morePermissionOptions(prompt)
    if (more.isNotEmpty()) {
        Text(stringResource(R.string.more_options), color = OnSurfaceVariant, style = MaterialTheme.typography.labelMedium)
        more.forEach { option ->
            OutlinedButton(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(horizontalAlignment = Alignment.Start) {
                    Text(option.label)
                    option.description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = OnSurfaceVariant)
                    }
                }
            }
        }
    }
}

@Composable
private fun QuestionActions(prompt: PendingPrompt, locked: Boolean, onOption: (PromptOption) -> Unit) {
    // A question's options are its answers; Cancel is never offered for one.
    prompt.options.forEach { option ->
        val content: @Composable () -> Unit = {
            Column(horizontalAlignment = Alignment.Start) {
                Text(option.label)
                option.description?.takeIf { it.isNotBlank() }?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        if (option.role == "deny") {
            OutlinedButton(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) { content() }
        } else {
            Button(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) { content() }
        }
    }
}

@Composable
private fun UnknownPromptActions(prompt: PendingPrompt, locked: Boolean, onCancel: () -> Unit) {
    prompt.raw_tail?.takeIf { it.isNotBlank() }?.let {
        Text(
            text = tailPreview(it, SCREEN_TAIL_LINES).text,
            fontFamily = FontFamily.Monospace,
            style = MaterialTheme.typography.bodySmall
        )
    }
    Text(stringResource(R.string.answer_on_device), color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall)
    OutlinedButton(onClick = onCancel, enabled = !locked) { Text(stringResource(R.string.cancel_prompt)) }
}

@Composable
private fun LastReplyCard(reply: HistoryItem?, onReadAll: (title: String, body: String, source: String, query: String) -> Unit) {
    Card {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(stringResource(R.string.last_reply), style = MaterialTheme.typography.titleSmall)
            if (reply == null) {
                Text(stringResource(R.string.no_reply_yet), color = OnSurfaceVariant)
                return@Column
            }
            val clean = remember(reply.id) {
                MarkdownFormatter.clean(reply.response).replace(Regex("\n{2,}"), "\n")
            }
            val preview = remember(clean) { headTailPreview(clean, REPLY_HEAD_CHARS, REPLY_TAIL_CHARS) ?: clean }
            Text(
                text = preview,
                style = MaterialTheme.typography.bodyMedium,
                fontFamily = if (reply.source == "screen") FontFamily.Monospace else null,
                maxLines = 20,
                overflow = TextOverflow.Ellipsis
            )
            if (preview != clean) {
                TextButton(
                    onClick = {
                        onReadAll(
                            reply.label.ifBlank { reply.agent },
                            reply.response,
                            reply.source,
                            reply.query.orEmpty()
                        )
                    }
                ) { Text(stringResource(R.string.read_all)) }
            }
        }
    }
}
