package com.gabriel.agentwatch.ui.screens

import android.app.Activity
import android.content.ActivityNotFoundException
import android.speech.RecognizerIntent
import android.view.HapticFeedbackConstants
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material3.ConfirmationDialogDefaults
import androidx.wear.compose.material3.EdgeButton
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.OutlinedButton
import androidx.wear.compose.material3.SuccessConfirmationDialog
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.confirmationDialogCurvedText
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.CommandFeedback
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.RecognizerIntentFactory
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.theme.OnSurface
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.SurfaceLow
import kotlinx.coroutines.launch

/**
 * What the recognizer understood and where it goes, before anything is sent. Used from the agent
 * screen and from Quick Dictate (the tile).
 */
@Composable
fun DictationConfirmScreen(
    targetLabel: String,
    text: String,
    sending: Boolean,
    error: CommandFeedback?,
    onSend: () -> Unit,
    onSpeakAgain: () -> Unit
) {
    ScreenList(
        edgeButton = {
            EdgeButton(onClick = onSend, enabled = !sending) {
                ResIcon(R.drawable.ic_send, null, MaterialTheme.colorScheme.onPrimary, Modifier.size(20.dp))
                Spacer(Modifier.width(4.dp))
                Text(stringResource(if (sending) R.string.loading else R.string.send))
            }
        }
    ) { spec ->
        item(key = "target") {
            Text(
                stringResource(R.string.dictation_confirm_title, targetLabel),
                modifier = transformedItem(spec),
                style = MaterialTheme.typography.titleMedium,
                textAlign = TextAlign.Center,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis
            )
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
            OutlinedButton(
                onClick = onSpeakAgain,
                enabled = !sending,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_mic, null, OnSurfaceVariant, Modifier.size(20.dp)) },
                label = { Text(stringResource(R.string.speak_again)) }
            )
        }
    }
}

/**
 * Confirm-then-send for one dictated [text] to [target] (null: the agent is gone). Sends with the
 * target's `state_change_seq`, shows "Sent" and calls [onFinished]; errors stay on screen, mapped by
 * [commandErrorFeedback]. Shared by the agent screen and Quick Dictate.
 */
@Composable
fun DictationFlow(target: AgentState?, text: String, onTextChange: (String) -> Unit, onFinished: () -> Unit) {
    val scope = rememberCoroutineScope()
    val view = LocalView.current
    var sending by remember { mutableStateOf(false) }
    var sent by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<CommandFeedback?>(null) }
    val closed = stringResource(R.string.dictation_agent_closed)
    val unavailable = stringResource(R.string.dictation_unavailable)
    val label = target?.label?.ifBlank { target.pane_id }.orEmpty()
    val prompt = stringResource(R.string.dictation_prompt, label)

    val speak = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
                ?.takeIf { it.isNotBlank() }
                ?.let { onTextChange(it); error = null }
        }
    }

    DictationConfirmScreen(
        targetLabel = label,
        text = text,
        sending = sending,
        error = error ?: if (target == null) CommandFeedback(closed, isError = true) else null,
        onSend = {
            val agent = target ?: return@DictationConfirmScreen
            sending = true
            error = null
            scope.launch {
                RelayRepository.prompt(agent.pane_id, text, agent.state_change_seq).fold(
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
        onSpeakAgain = {
            try {
                speak.launch(RecognizerIntentFactory.freeForm(prompt))
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
