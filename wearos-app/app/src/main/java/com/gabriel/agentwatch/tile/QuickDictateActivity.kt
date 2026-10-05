package com.gabriel.agentwatch.tile

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material3.AppScaffold
import androidx.wear.compose.material3.CircularProgressIndicator
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.ScreenScaffold
import androidx.wear.compose.material3.Text
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.RelayClient
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.PromptInput
import com.gabriel.agentwatch.ui.logic.InputResult
import com.gabriel.agentwatch.ui.logic.inputFeedback
import com.gabriel.agentwatch.ui.screens.DictationFlow
import com.gabriel.agentwatch.ui.theme.AgentWatchTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Quick Dictate, opened by the tile with the `pane_id` it displayed: fetch that agent (fresh seq),
 * take the prompt (voice, keyboard or handwriting), show it, send on confirmation. Errors stay on screen, mapped by
 * [commandErrorFeedback]; nothing is sent to an agent other than the one the tile named.
 */
class QuickDictateActivity : ComponentActivity() {

    companion object {
        const val EXTRA_PANE_ID = "pane_id"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        RelayRepository.init(this) // commands and 401 handling go through the process-wide engine
        val launchPaneId = intent?.getStringExtra(EXTRA_PANE_ID)
        setContent {
            AgentWatchTheme {
                AppScaffold { QuickDictate(Prefs(this@QuickDictateActivity), launchPaneId, onFinish = ::finish) }
            }
        }
    }
}

private sealed interface Phase {
    data object Loading : Phase
    data class Message(val text: String) : Phase
    data class Listening(val target: AgentState) : Phase
    data class Confirm(val target: AgentState, val text: String) : Phase
}

@Composable
private fun QuickDictate(prefs: Prefs, launchPaneId: String?, onFinish: () -> Unit) {
    var phase by remember { mutableStateOf<Phase>(Phase.Loading) }
    val notPaired = stringResource(R.string.dictation_not_paired)
    val closed = stringResource(R.string.dictation_agent_closed)
    val noTarget = stringResource(R.string.dictation_no_target)
    val unavailable = stringResource(R.string.dictation_unavailable)
    val context = LocalContext.current

    LaunchedEffect(Unit) {
        if (!prefs.isPaired) {
            phase = Phase.Message(notPaired)
            return@LaunchedEffect
        }
        val result = withContext(Dispatchers.IO) { RelayClient(prefs.relayUrl, prefs.deviceToken).agents() }
        phase = result.fold(
            onSuccess = { snapshot ->
                when (val target = dictationTarget(snapshot.agents, launchPaneId, prefs.pinnedPaneId)) {
                    is DictationTarget.Found -> Phase.Listening(target.agent)
                    DictationTarget.Closed -> Phase.Message(closed)
                    DictationTarget.None -> Phase.Message(noTarget)
                }
            },
            onFailure = { Phase.Message(commandErrorFeedback(it).message) }
        )
    }

    when (val p = phase) {
        Phase.Loading -> Centered {
            CircularProgressIndicator(Modifier.size(36.dp))
            Text(stringResource(R.string.loading), style = MaterialTheme.typography.bodyMedium)
        }
        is Phase.Message -> MessageWithOpenApp(p.text)
        is Phase.Listening -> {
            val prompt = stringResource(R.string.dictation_prompt, p.target.label.ifBlank { p.target.pane_id })
            // Backing out closes Quick Dictate; any other result without text says why.
            val listen = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
                when (val input = PromptInput.result(result.resultCode, result.data)) {
                    is InputResult.Text -> phase = Phase.Confirm(p.target, input.text)
                    else -> inputFeedback(input)?.let { phase = Phase.Message(context.getString(it)) } ?: onFinish()
                }
            }
            LaunchedEffect(p) {
                try {
                    listen.launch(PromptInput.intent(prompt))
                } catch (_: ActivityNotFoundException) {
                    phase = Phase.Message(unavailable)
                }
            }
            Centered { CircularProgressIndicator(Modifier.size(36.dp)) }
        }
        is Phase.Confirm -> DictationFlow(
            target = p.target,
            text = p.text,
            onTextChange = { phase = p.copy(text = it) },
            onFinished = onFinish
        )
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    ScreenScaffold {
        Column(
            Modifier.fillMaxSize().padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally
        ) { content() }
    }
}

@Composable
private fun MessageWithOpenApp(text: String) {
    val context = LocalContext.current
    Centered {
        Text(text, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
        FilledTonalButton(
            onClick = {
                context.startActivity(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                (context as? Activity)?.finish()
            },
            modifier = Modifier.fillMaxWidth(),
            label = { Text(stringResource(R.string.open_app)) }
        )
    }
}
