package com.gabriel.agentwatch.ui.screens

import android.content.ActivityNotFoundException
import android.view.HapticFeedbackConstants
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material3.ConfirmationDialogDefaults
import androidx.wear.compose.material3.EdgeButton
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.ButtonDefaults
import androidx.wear.compose.material3.CompactButton
import androidx.wear.compose.material3.SuccessConfirmationDialog
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.confirmationDialogCurvedText
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.CommandFeedback
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.PromptInput
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.logic.InputResult
import com.gabriel.agentwatch.ui.logic.inputFeedback
import com.gabriel.agentwatch.ui.theme.OnSurface
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.SurfaceLow
import kotlinx.coroutines.launch

/**
 * The text entered (spoken, typed or handwritten) and where it goes, before anything is sent. Used
 * from the agent screen and from Quick Dictate (the tile). [targetHost]: the target's host name, on a
 * line of its own, only when the relay knows several hosts.
 */
@Composable
fun DictationConfirmScreen(
    targetLabel: String,
    targetHost: String?,
    text: String,
    sending: Boolean,
    error: CommandFeedback?,
    onSend: () -> Unit,
    onEnterAgain: () -> Unit
) {
    ScreenList(
        edgeButton = {
            EdgeButton(onClick = onSend, enabled = !sending) {
                // While sending, the longer label goes alone so it never wraps on the round screen.
                if (!sending) {
                    ResIcon(R.drawable.ic_send, null, MaterialTheme.colorScheme.onPrimary, Modifier.size(20.dp))
                    Spacer(Modifier.width(4.dp))
                }
                Text(stringResource(if (sending) R.string.loading else R.string.send), maxLines = 1)
            }
        }
    ) { spec ->
        // Kept short so Send stays on screen for a typical dictation.
        item(key = "target") {
            Text(
                stringResource(R.string.dictation_prompt, targetLabel),
                modifier = transformedItem(spec).padding(horizontal = 24.dp),
                color = OnSurfaceVariant,
                style = MaterialTheme.typography.labelMedium,
                textAlign = TextAlign.Center,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        }
        targetHost?.let { host ->
            item(key = "host") {
                Text(
                    host,
                    modifier = transformedItem(spec).padding(horizontal = 24.dp),
                    color = OnSurfaceVariant,
                    style = MaterialTheme.typography.labelSmall,
                    textAlign = TextAlign.Center,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
        item(key = "text") {
            Text(
                text,
                modifier = transformedItem(spec)
                    .background(SurfaceLow, RoundedCornerShape(12.dp))
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                color = OnSurface,
                style = MaterialTheme.typography.bodyLarge
            )
        }
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
        item(key = "again") {
            CompactButton(
                onClick = onEnterAgain,
                enabled = !sending,
                modifier = Modifier.transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                colors = ButtonDefaults.filledTonalButtonColors(),
                icon = { ResIcon(R.drawable.ic_edit, null, OnSurfaceVariant, Modifier.size(18.dp)) },
                label = { Text(stringResource(R.string.enter_again)) }
            )
        }
    }
}

/**
 * Confirm-then-send for one dictated [text] to [target] (null: the agent is gone), on its host. Sends
 * with the target's `state_change_seq`, shows "Sent" and calls [onFinished]; errors stay on screen,
 * mapped by [commandErrorFeedback]. Shared by the agent screen and Quick Dictate. [hostName]: the
 * target's host, shown only when the relay knows several hosts.
 */
@Composable
fun DictationFlow(
    target: AgentState?,
    hostName: String?,
    text: String,
    onTextChange: (String) -> Unit,
    onFinished: () -> Unit
) {
    val scope = rememberCoroutineScope()
    val view = LocalView.current
    var sending by remember { mutableStateOf(false) }
    var sent by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<CommandFeedback?>(null) }
    val closed = stringResource(R.string.dictation_agent_closed)
    val unavailable = stringResource(R.string.dictation_unavailable)
    val label = target?.label?.ifBlank { target.pane_id }.orEmpty()
    val prompt = stringResource(R.string.dictation_prompt, label)

    // Enter again: the new text replaces the old one; a result without text keeps the old one and
    // says why (only backing out is silent).
    val context = LocalContext.current
    val reenter = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        when (val input = PromptInput.result(result.resultCode, result.data)) {
            is InputResult.Text -> { onTextChange(input.text); error = null }
            else -> inputFeedback(input)?.let {
                view.performHapticFeedback(HapticFeedbackConstants.REJECT)
                error = CommandFeedback(context.getString(it), isError = true)
            }
        }
    }

    DictationConfirmScreen(
        targetLabel = label,
        targetHost = hostName,
        text = text,
        sending = sending,
        error = error ?: if (target == null) CommandFeedback(closed, isError = true) else null,
        onSend = {
            val agent = target ?: return@DictationConfirmScreen
            sending = true
            error = null
            scope.launch {
                RelayRepository.prompt(agent.key, text, agent.state_change_seq).fold(
                    onSuccess = {
                        view.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
                        sent = true
                    },
                    onFailure = {
                        view.performHapticFeedback(HapticFeedbackConstants.REJECT)
                        error = commandErrorFeedback(it)
                    }
                )
                sending = false
            }
        },
        onEnterAgain = {
            try {
                reenter.launch(PromptInput.intent(prompt))
            } catch (_: ActivityNotFoundException) {
                error = CommandFeedback(unavailable, isError = true)
            }
        }
    )

    val sentText = stringResource(R.string.feedback_sent)
    val curvedStyle = ConfirmationDialogDefaults.curvedTextStyle
    SuccessConfirmationDialog(
        visible = sent,
        onDismissRequest = {
            sent = false
            onFinished()
        },
        curvedText = { confirmationDialogCurvedText(sentText, curvedStyle) }
    )
}
