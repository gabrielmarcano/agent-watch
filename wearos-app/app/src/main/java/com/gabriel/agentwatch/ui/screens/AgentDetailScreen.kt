package com.gabriel.agentwatch.ui.screens

import android.app.Activity
import android.content.ActivityNotFoundException
import android.speech.RecognizerIntent
import android.view.HapticFeedbackConstants
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.foundation.lazy.rememberTransformingLazyColumnState
import androidx.wear.compose.material3.AlertDialog
import androidx.wear.compose.material3.AlertDialogDefaults
import androidx.wear.compose.material3.Card
import androidx.wear.compose.material3.ConfirmationDialogDefaults
import androidx.wear.compose.material3.EdgeButton
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.ListSubHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.SuccessConfirmationDialog
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.SwitchButton
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.confirmationDialogCurvedText
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.CommandFeedback
import com.gabriel.agentwatch.approval.SentAnswer
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.approval.isAwaitingUpdate
import com.gabriel.agentwatch.approval.needsConfirmation
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.RecognizerIntentFactory
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.ageText
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.statusStyle
import com.gabriel.agentwatch.util.MarkdownFormatter
import kotlinx.coroutines.launch

@Composable
fun AgentDetailScreen(
    agent: AgentState,
    lastReply: HistoryItem?,
    onReadReply: (HistoryItem) -> Unit,
    isTileTarget: Boolean,
    onTileTargetChange: (Boolean) -> Unit,
    onDictated: (text: String) -> Unit,
    onHistoryClick: () -> Unit,
    onViewAll: (text: String) -> Unit
) {
    val scope = rememberCoroutineScope()
    val view = LocalView.current

    var inFlight by remember { mutableStateOf(false) }
    var error by remember(agent.pane_id) { mutableStateOf<CommandFeedback?>(null) }
    var confirmation by remember { mutableStateOf<Int?>(null) }
    var askAlways by remember { mutableStateOf<PromptOption?>(null) }

    // After a successful answer or cancel the prompt stays locked until the relay reports a new seq or
    // fingerprint, or the agent leaves blocked. Prevents a double send.
    var sentAnswer by remember(agent.pane_id) { mutableStateOf<SentAnswer?>(null) }
    val awaitingUpdate = isAwaitingUpdate(agent, sentAnswer)
    LaunchedEffect(sentAnswer, awaitingUpdate) {
        if (sentAnswer != null && !awaitingUpdate) sentAnswer = null
    }
    // A new prompt makes the last error meaningless.
    LaunchedEffect(agent.state_change_seq, agent.prompt?.fingerprint) { error = null }

    fun send(successMessage: Int, command: suspend (SentAnswer) -> Result<Unit>) {
        val prompt = agent.prompt ?: return
        if (inFlight || awaitingUpdate) return
        // The seq and fingerprint of the prompt on screen at tap time.
        val target = SentAnswer(agent.pane_id, agent.state_change_seq, prompt.fingerprint)
        inFlight = true
        error = null
        scope.launch {
            command(target).fold(
                onSuccess = {
                    sentAnswer = target
                    view.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
                    confirmation = successMessage
                },
                onFailure = {
                    view.performHapticFeedback(HapticFeedbackConstants.REJECT)
                    error = commandErrorFeedback(it)
                }
            )
            inFlight = false
        }
    }

    fun answer(option: PromptOption) = send(
        when (option.role) {
            "allow_once", "allow_always" -> R.string.feedback_approved
            "deny" -> R.string.feedback_denied
            else -> R.string.feedback_answered
        }
    ) { t -> RelayRepository.answer(t.paneId, option.id, t.seq, t.fingerprint) }

    fun cancel() = send(R.string.feedback_canceled) { t ->
        RelayRepository.cancel(t.paneId, t.seq, fingerprint = t.fingerprint)
    }

    val dictate = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
                ?.takeIf { it.isNotBlank() }
                ?.let(onDictated)
        }
    }
    val dictationPrompt = stringResource(R.string.dictation_prompt, agent.label.ifBlank { agent.pane_id })
    val unavailable = stringResource(R.string.dictation_unavailable)

    // The last reply arrives a moment after the screen opens, and a prompt can appear or change: items
    // inserted above the anchor would push the name off screen, so go back to the top.
    val listState = rememberTransformingLazyColumnState()
    LaunchedEffect(lastReply?.id, agent.prompt?.fingerprint) {
        if (listState.anchorItemIndex <= 3) listState.scrollToItem(0)
    }

    val blocked = agent.status == "blocked"
    ScreenList(
        state = listState,
        edgeButton = if (blocked) null else {
            {
                EdgeButton(onClick = {
                    try {
                        dictate.launch(RecognizerIntentFactory.freeForm(dictationPrompt))
                    } catch (_: ActivityNotFoundException) {
                        error = CommandFeedback(unavailable, isError = true)
                    }
                }) {
                    ResIcon(R.drawable.ic_mic, null, MaterialTheme.colorScheme.onPrimary, Modifier.size(20.dp))
                    Spacer(Modifier.width(4.dp))
                    Text(stringResource(R.string.reply))
                }
            }
        }
    ) { spec ->
        // Narrower at the top of the round screen, where the chord is short.
        item(key = "header") { AgentHeader(agent, transformedItem(spec).padding(start = 18.dp, end = 18.dp, top = 6.dp, bottom = 6.dp)) }

        val prompt = agent.prompt
        if (blocked && prompt != null) {
            promptItems(
                prompt, spec,
                PromptControls(
                    locked = inFlight || awaitingUpdate,
                    sent = awaitingUpdate,
                    error = error,
                    onAnswer = { option -> if (option.needsConfirmation()) askAlways = option else answer(option) },
                    onCancel = ::cancel,
                    onViewAll = onViewAll
                )
            )
        } else {
            error?.let { fb ->
                item(key = "error") {
                    Text(
                        fb.message,
                        modifier = transformedItem(spec),
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.labelMedium,
                        textAlign = TextAlign.Center
                    )
                }
            }
        }

        // What the agent said last: the reason to open this screen, and what a reply answers.
        lastReply?.let { reply ->
            item(key = "reply-header") {
                ListSubHeader(
                    modifier = Modifier.fillMaxWidth().padding(top = 6.dp).transformedHeight(this, spec),
                    transformation = SurfaceTransformation(spec)
                ) {
                    Text(
                        listOf(stringResource(R.string.last_reply), ageText(reply.completed_at))
                            .filter { it.isNotBlank() }.joinToString(" · ")
                    )
                }
            }
            item(key = "reply") { LastReplyCard(reply, { onReadReply(reply) }, Modifier.fillMaxWidth().transformedHeight(this, spec), SurfaceTransformation(spec)) }
        }

        item(key = "tile-target") {
            SwitchButton(
                checked = isTileTarget,
                onCheckedChange = onTileTargetChange,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp).transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                secondaryLabel = { Text(stringResource(R.string.quick_dictate_target_hint), maxLines = 2) },
                label = { Text(stringResource(R.string.quick_dictate_target)) }
            )
        }
        item(key = "history") {
            FilledTonalButton(
                onClick = onHistoryClick,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_history, null, MaterialTheme.colorScheme.onSurface) },
                label = { Text(stringResource(R.string.agent_history)) }
            )
        }
    }

    AlertDialog(
        visible = askAlways != null,
        onDismissRequest = { askAlways = null },
        confirmButton = {
            AlertDialogDefaults.ConfirmButton(onClick = {
                askAlways?.let(::answer)
                askAlways = null
            })
        },
        title = { Text(stringResource(R.string.confirm_always_title)) },
        text = {
            Text(
                listOfNotNull(askAlways?.label, askAlways?.description?.takeIf { it.isNotBlank() }).joinToString(" "),
                textAlign = TextAlign.Center
            )
        }
    )

    val confirmationText = confirmation?.let { stringResource(it) }.orEmpty()
    val curvedStyle = ConfirmationDialogDefaults.curvedTextStyle
    SuccessConfirmationDialog(
        visible = confirmation != null,
        onDismissRequest = { confirmation = null },
        curvedText = { confirmationDialogCurvedText(confirmationText, curvedStyle) }
    )
}

/** Name first, anchored under the clock; then one status line and one line of context (the agent's own title, the workspace). Kept short so the prompt or the last reply shows on open. */
@Composable
private fun AgentHeader(agent: AgentState, modifier: Modifier) {
    val style = statusStyle(agent.status)
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            agent.label.ifBlank { agent.pane_id },
            style = MaterialTheme.typography.titleMedium,
            textAlign = TextAlign.Center,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis
        )
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
            ResIcon(style.icon, null, style.accent, Modifier.size(16.dp))
            Spacer(Modifier.width(4.dp))
            val statusLine = listOfNotNull(
                stringResource(style.label),
                agent.agent.takeIf { it.isNotBlank() },
                ageText(agent.updated_at).takeIf { it.isNotBlank() }
            ).joinToString(" · ")
            Text(statusLine, color = style.accent, style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        val context = listOfNotNull(
            agent.title?.takeIf { it.isNotBlank() && it != agent.label },
            agent.workspace?.takeIf { it.isNotBlank() && it != agent.label }
        ).joinToString(" · ")
        if (context.isNotBlank()) {
            Text(context, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

private const val REPLY_PREVIEW_LINES = 8

/** The last reply as plain text (markdown stripped), cut at [REPLY_PREVIEW_LINES]; tapping opens the reader. */
@Composable
private fun LastReplyCard(reply: HistoryItem, onClick: () -> Unit, modifier: Modifier, transformation: SurfaceTransformation) {
    val preview = remember(reply.response) { MarkdownFormatter.clean(reply.response).replace(Regex("\n{2,}"), "\n") }
    Card(onClick = onClick, modifier = modifier, transformation = transformation) {
        reply.query?.takeIf { it.isNotBlank() }?.let { query ->
            Text(query, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Text(preview, style = MaterialTheme.typography.bodyMedium, maxLines = REPLY_PREVIEW_LINES, overflow = TextOverflow.Ellipsis)
        Text(stringResource(R.string.read_all), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelSmall)
    }
}
