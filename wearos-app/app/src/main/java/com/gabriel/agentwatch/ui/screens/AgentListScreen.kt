package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.rotary.onRotaryScrollEvent
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.wear.compose.foundation.lazy.ScalingLazyColumn
import androidx.wear.compose.foundation.lazy.items
import androidx.wear.compose.foundation.lazy.rememberScalingLazyListState
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.network.Connection
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.gabriel.agentwatch.ui.theme.Red400
import com.gabriel.agentwatch.ui.theme.statusColor
import kotlinx.coroutines.launch

@Composable
fun AgentListScreen(
    uiState: UiState,
    onAgentClick: (paneId: String) -> Unit,
    onHistoryClick: () -> Unit,
    onSettingsClick: () -> Unit
) {
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val coroutineScope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current

    val groupedAgents = remember(uiState.agents) {
        uiState.agents.groupBy { it.workspace?.ifBlank { null } ?: it.workspace_id }
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
            // Header: Status indicator
            item {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(bottom = 4.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        val statusDotColor = when (uiState.connection) {
                            is Connection.Live -> BrightGreen
                            is Connection.Connecting -> BrightYellow
                            is Connection.Offline -> Red400
                        }
                        Text(
                            text = "●",
                            color = statusDotColor,
                            fontSize = 10.sp,
                            modifier = Modifier.padding(end = 4.dp)
                        )
                        Text(
                            text = "AGENTS (${uiState.agents.size})",
                            style = MaterialTheme.typography.caption1.copy(
                                fontWeight = FontWeight.ExtraBold,
                                letterSpacing = 1.sp
                            ),
                            color = Color.White
                        )
                    }
                }
            }

            // Warning Banners (Host Offline / Herdr Stopped)
            if (!uiState.hostOnline) {
                item {
                    Text(
                        text = "⚠ Mac is offline",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.5.sp, fontWeight = FontWeight.Bold),
                        color = BrightYellow,
                        modifier = Modifier.padding(vertical = 2.dp)
                    )
                }
            } else if (!uiState.herdrOnline) {
                item {
                    Text(
                        text = "⚠ Herdr stopped",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.5.sp, fontWeight = FontWeight.Bold),
                        color = BrightYellow,
                        modifier = Modifier.padding(vertical = 2.dp)
                    )
                }
            }

            // Agent Chips grouped by workspace
            if (uiState.agents.isEmpty()) {
                item {
                    Text(
                        text = if (uiState.connection is Connection.Connecting) "Connecting..." else "No active agents",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 11.sp),
                        color = Color.White.copy(alpha = 0.5f),
                        modifier = Modifier.padding(vertical = 12.dp)
                    )
                }
            } else {
                groupedAgents.forEach { (workspaceName, agentsInWorkspace) ->
                    item(key = "ws_$workspaceName") {
                        ListHeader(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(top = 8.dp, bottom = 2.dp)
                        ) {
                            Text(
                                text = workspaceName.uppercase(),
                                style = MaterialTheme.typography.caption2.copy(
                                    fontWeight = FontWeight.Bold,
                                    letterSpacing = 0.8.sp
                                ),
                                color = Color.White.copy(alpha = 0.65f),
                                textAlign = TextAlign.Start,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(horizontal = 4.dp)
                            )
                        }
                    }
                    items(agentsInWorkspace, key = { it.pane_id }) { agent ->
                        AgentChip(
                            agent = agent,
                            onClick = { onAgentClick(agent.pane_id) }
                        )
                    }
                }
            }

            // History Button
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Chip(
                    onClick = onHistoryClick,
                    label = {
                        Text(
                            text = "HISTORY",
                            fontSize = 11.sp,
                            fontWeight = FontWeight.Bold,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        )
                    },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            // Settings / Pair Button
            item {
                Chip(
                    onClick = onSettingsClick,
                    label = {
                        Text(
                            text = "SETTINGS",
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

@Composable
private fun AgentChip(
    agent: AgentState,
    onClick: () -> Unit
) {
    val isBlocked = agent.status == "blocked"
    val color = statusColor(agent.status)

    // Status-specific background and border tint
    val (backgroundColor, borderColor) = when (agent.status) {
        "blocked" -> Pair(BrightYellow.copy(alpha = 0.2f), BrightYellow.copy(alpha = 0.7f))
        "working" -> Pair(LightBlue.copy(alpha = 0.15f), LightBlue.copy(alpha = 0.5f))
        "done" -> Pair(BrightGreen.copy(alpha = 0.12f), BrightGreen.copy(alpha = 0.4f))
        else -> Pair(Color(0x22FFFFFF), Color.Transparent)
    }

    Chip(
        onClick = onClick,
        icon = {
            Box(
                modifier = Modifier.size(16.dp),
                contentAlignment = Alignment.Center
            ) {
                if (isBlocked) {
                    Text(
                        text = "⚠",
                        color = BrightYellow,
                        fontSize = 13.sp,
                        fontWeight = FontWeight.Bold
                    )
                } else {
                    Box(
                        modifier = Modifier
                            .size(7.dp)
                            .background(color, CircleShape)
                    )
                }
            }
        },
        label = {
            Text(
                text = agent.label.ifBlank { agent.name ?: agent.pane_id },
                fontWeight = FontWeight.Bold,
                fontSize = 12.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        },
        secondaryLabel = {
            val secondaryText = buildAnnotatedString {
                if (!agent.name.isNullOrBlank() && agent.name != agent.label) {
                    append("${agent.name} · ")
                }
                append("${agent.agent} · ")
                withStyle(SpanStyle(color = color, fontWeight = FontWeight.Bold)) {
                    append(agent.status.uppercase())
                }
            }
            Text(
                text = secondaryText,
                fontSize = 9.5.sp,
                color = Color.White.copy(alpha = 0.65f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis
            )
        },
        colors = ChipDefaults.chipColors(
            backgroundColor = backgroundColor
        ),
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 2.dp)
            .then(
                if (borderColor != Color.Transparent) {
                    Modifier.border(1.dp, borderColor, RoundedCornerShape(18.dp))
                } else {
                    Modifier
                }
            )
    )
}
