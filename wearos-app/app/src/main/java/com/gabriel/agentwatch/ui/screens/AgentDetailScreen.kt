package com.gabriel.agentwatch.ui.screens

import android.app.Activity
import android.content.Intent
import android.speech.RecognizerIntent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.rotary.onRotaryScrollEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.wear.compose.foundation.lazy.ScalingLazyColumn
import androidx.wear.compose.foundation.lazy.rememberScalingLazyListState
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.approval.CommandFeedback
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.gabriel.agentwatch.ui.theme.Red400
import com.gabriel.agentwatch.ui.theme.statusColor
import kotlinx.coroutines.launch

@Composable
fun AgentDetailScreen(
    agent: AgentState,
    onHistoryClick: (paneId: String) -> Unit,
    onBackClick: () -> Unit
) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    val coroutineScope = rememberCoroutineScope()
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val lifecycleOwner = LocalLifecycleOwner.current

    var actionInFlight by remember { mutableStateOf(false) }
    var feedback by remember { mutableStateOf<CommandFeedback?>(null) }

    // Opening this screen pins this agent for quick dictation
    LaunchedEffect(agent.pane_id) {
        prefs.pinnedPaneId = agent.pane_id
    }

    // Voice dictation launcher
    val dictateLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val spokenText = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
            if (!spokenText.isNullOrBlank()) {
                actionInFlight = true
                feedback = null
                coroutineScope.launch {
                    val res = RelayRepository.prompt(
                        paneId = agent.pane_id,
                        text = spokenText,
                        expectedSeq = agent.state_change_seq
                    )
                    actionInFlight = false
                    res.fold(
                        onSuccess = { feedback = CommandFeedback.success("Prompt sent") },
                        onFailure = { err -> feedback = commandErrorFeedback(err) }
                    )
                }
            }
        }
    }

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                coroutineScope.launch {
                    kotlinx.coroutines.delay(50)
                    try { focusRequester.requestFocus() } catch (_: Exception) {}
                }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
        }
    }

    Scaffold(
        timeText = { TimeText() },
        vignette = { Vignette(vignettePosition = VignettePosition.TopAndBottom) },
        positionIndicator = { PositionIndicator(scalingLazyListState = listState) }
    ) {
        ScalingLazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .onRotaryScrollEvent { event ->
                    coroutineScope.launch {
                        listState.scrollBy(event.verticalScrollPixels)
                    }
                    true
                }
                .focusRequester(focusRequester)
                .focusable(),
            state = listState,
            horizontalAlignment = Alignment.CenterHorizontally,
            contentPadding = PaddingValues(top = 28.dp, start = 10.dp, end = 10.dp, bottom = 28.dp)
        ) {
            // Agent Label & Kind Header
            item {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(bottom = 4.dp)) {
                    Text(
                        text = agent.label.ifBlank { agent.pane_id }.uppercase(),
                        style = MaterialTheme.typography.caption1.copy(fontWeight = FontWeight.ExtraBold, letterSpacing = 1.sp),
                        color = Color.White,
                        textAlign = TextAlign.Center
                    )
                    val contextLine = buildString {
                        if (!agent.workspace.isNullOrBlank()) {
                            append(agent.workspace)
                        }
                        if (!agent.name.isNullOrBlank() && agent.name != agent.label) {
                            if (isNotEmpty()) append(" · ")
                            append(agent.name)
                        }
                    }
                    if (contextLine.isNotBlank()) {
                        Text(
                            text = contextLine,
                            style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp, fontWeight = FontWeight.SemiBold),
                            color = Color.White.copy(alpha = 0.6f),
                            textAlign = TextAlign.Center
                        )
                    }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = "● ",
                            color = statusColor(agent.status),
                            fontSize = 10.sp
                        )
                        Text(
                            text = "${agent.agent} · ${agent.status.uppercase()}",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp),
                            color = statusColor(agent.status)
                        )
                    }
                    if (!agent.cwd.isNullOrBlank()) {
                        val shortCwd = agent.cwd.split("/").takeLast(2).joinToString("/")
                        Text(
                            text = "~/$shortCwd",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp),
                            color = Color.White.copy(alpha = 0.5f)
                        )
                    }
                }
            }

            // Feedback banner (e.g. "Prompt changed — refreshed"); errors are always red
            feedback?.let { fb ->
                item {
                    Text(
                        text = fb.message,
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.5.sp, fontWeight = FontWeight.Bold),
                        color = if (fb.isError) Red400 else BrightGreen,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.padding(vertical = 2.dp)
                    )
                }
            }

            // Prompt Card if blocked
            if (agent.status == "blocked" && agent.prompt != null) {
                item {
                    PromptCard(
                        agent = agent,
                        isActionInFlight = actionInFlight,
                        onAnswerClick = { optionId ->
                            actionInFlight = true
                            feedback = null
                            coroutineScope.launch {
                                val res = RelayRepository.answer(
                                    paneId = agent.pane_id,
                                    optionId = optionId,
                                    expectedSeq = agent.state_change_seq,
                                    fingerprint = agent.prompt.fingerprint
                                )
                                actionInFlight = false
                                res.fold(
                                    onSuccess = { feedback = CommandFeedback.success("Sent answer") },
                                    onFailure = { err -> feedback = commandErrorFeedback(err) }
                                )
                            }
                        },
                        onCancelClick = {
                            actionInFlight = true
                            feedback = null
                            coroutineScope.launch {
                                val res = RelayRepository.cancel(
                                    paneId = agent.pane_id,
                                    expectedSeq = agent.state_change_seq
                                )
                                actionInFlight = false
                                res.fold(
                                    onSuccess = { feedback = CommandFeedback.success("Canceled") },
                                    onFailure = { err -> feedback = commandErrorFeedback(err) }
                                )
                            }
                        }
                    )
                }
            }

            // Voice Dictate Button
            item {
                val canDictate = agent.status != "blocked"
                Chip(
                    onClick = {
                        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                            putExtra(RecognizerIntent.EXTRA_PROMPT, "To: ${agent.label.ifBlank { agent.pane_id }}")
                        }
                        dictateLauncher.launch(intent)
                    },
                    enabled = canDictate && !actionInFlight,
                    label = {
                        Text(
                            text = "DICTATE",
                            fontWeight = FontWeight.Bold,
                            fontSize = 11.sp
                        )
                    },
                    secondaryLabel = {
                        Text(
                            text = "To: ${agent.label.ifBlank { agent.pane_id }}",
                            fontSize = 9.sp
                        )
                    },
                    icon = { MicrophoneIcon(modifier = Modifier.size(16.dp), color = Color.White) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0xFF2563EB)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            // History Button for this Agent
            item {
                Chip(
                    onClick = { onHistoryClick(agent.pane_id) },
                    label = {
                        Text(
                            text = "HISTORY",
                            fontSize = 10.sp,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        )
                    },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            // Back Button
            item {
                Chip(
                    onClick = onBackClick,
                    label = {
                        Text(
                            text = "BACK",
                            fontSize = 10.sp,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        )
                    },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x14FFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }
        }
    }
}
