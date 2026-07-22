package com.gabriel.agentwatch.ui.screens

import androidx.compose.animation.core.*
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.wear.compose.foundation.lazy.ScalingLazyColumn
import androidx.wear.compose.foundation.lazy.rememberScalingLazyListState
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.gabriel.agentwatch.util.MarkdownFormatter
import kotlinx.coroutines.launch

@Composable
fun MainAgentFeedScreen(
    state: AgentState,
    onSendCommand: (String) -> Unit,
    onVoiceInputClick: () -> Unit,
    serverIp: String,
    onConfigureIpClick: () -> Unit,
    onHistoryClick: () -> Unit,
    onStatusClick: () -> Unit
) {
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val coroutineScope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                coroutineScope.launch {
                    kotlinx.coroutines.delay(50)
                    try { focusRequester.requestFocus() } catch (e: Exception) {}
                }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
        }
    }

    // Breathing pulse transition for the status glow
    val infiniteTransition = rememberInfiniteTransition(label = "breathing")
    val breathingScale by infiniteTransition.animateFloat(
        initialValue = 0.85f,
        targetValue = 1.25f,
        animationSpec = infiniteRepeatable(
            animation = tween(1200, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse
        ),
        label = "scale"
    )
    val breathingAlpha by infiniteTransition.animateFloat(
        initialValue = 0.3f,
        targetValue = 0.85f,
        animationSpec = infiniteRepeatable(
            animation = tween(1200, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse
        ),
        label = "alpha"
    )

    // Project Directory Badge (Basename of CWD)
    val folderName = remember(state.cwd) {
        if (state.cwd.isNullOrEmpty()) ""
        else {
            val parts = state.cwd.split('/', '\\')
            parts.lastOrNull { it.isNotEmpty() } ?: ""
        }
    }

    // Conversation History List (Last 3 Responses)
    val historyList = remember(state.history, state.last_response) {
        if (state.history.isNotEmpty()) {
            state.history.toList()
        } else if (!state.last_response.isNullOrEmpty()) {
            listOf(
                HistoryItem(
                    id = "latest",
                    query = state.last_query,
                    response = state.last_response
                )
            )
        } else {
            emptyList()
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
                .focusRequester(focusRequester)
                .focusable(),
            state = listState,
            horizontalAlignment = Alignment.CenterHorizontally,
            contentPadding = PaddingValues(
                top = 28.dp,
                start = 10.dp,
                end = 10.dp,
                bottom = 28.dp
            )
        ) {
            // Stylized Brand Header
            item {
                AgentMonitorBrandHeader()
            }

            // Project Directory Badge
            if (folderName.isNotEmpty()) {
                item {
                    Spacer(modifier = Modifier.height(4.dp))
                    Box(
                        modifier = Modifier
                            .background(
                                color = Color(0x1F00D2FF),
                                shape = RoundedCornerShape(12.dp)
                            )
                            .border(BorderStroke(1.dp, Color(0x3300D2FF)), shape = RoundedCornerShape(12.dp))
                            .padding(horizontal = 8.dp, vertical = 2.dp)
                    ) {
                        Text(
                            text = "DIR / $folderName",
                            style = MaterialTheme.typography.caption2.copy(
                                fontSize = 9.5.sp,
                                fontWeight = FontWeight.Bold,
                                letterSpacing = 0.5.sp
                            ),
                            color = LightBlue
                        )
                    }
                }
            }

            // Interactive Status Indicator Badge
            item {
                val (statusColor, statusTitle, statusSubtitle) = when (state.status) {
                    "thinking" -> Triple(BrightYellow, "THINKING...", "Processing query")
                    "waiting_for_permission" -> Triple(Color(0xFFFF3B30), "NEEDS AUTH", "Action required")
                    "done" -> Triple(BrightGreen, "READY", "Task completed")
                    else -> Triple(Color.Gray, "IDLE", "Waiting for prompt")
                }

                Card(
                    onClick = onStatusClick,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 4.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x12FFFFFF),
                        endBackgroundColor = Color(0x04FFFFFF)
                    )
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(
                                contentAlignment = Alignment.Center,
                                modifier = Modifier.size(18.dp)
                            ) {
                                androidx.compose.foundation.Canvas(
                                    modifier = Modifier
                                        .size(14.dp)
                                        .graphicsLayer(
                                            scaleX = breathingScale,
                                            scaleY = breathingScale,
                                            alpha = breathingAlpha
                                        )
                                ) {
                                    drawCircle(color = statusColor.copy(alpha = 0.4f))
                                }
                                androidx.compose.foundation.Canvas(modifier = Modifier.size(6.dp)) {
                                    drawCircle(color = statusColor)
                                }
                            }
                            
                            Spacer(modifier = Modifier.width(6.dp))
                            
                            Column {
                                Text(
                                    text = statusTitle,
                                    style = MaterialTheme.typography.body2.copy(
                                        fontWeight = FontWeight.Bold,
                                        fontSize = 11.sp,
                                        letterSpacing = 0.8.sp
                                    ),
                                    color = statusColor
                                )
                                Text(
                                    text = statusSubtitle,
                                    style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp),
                                    color = Color.White.copy(alpha = 0.6f)
                                )
                            }
                        }

                        Text(
                            text = "INFO >",
                            style = MaterialTheme.typography.caption2.copy(
                                fontSize = 8.sp,
                                fontWeight = FontWeight.Bold
                            ),
                            color = LightBlue
                        )
                    }
                }
            }

            // Authorization Card (If waiting for permission)
            if (state.status == "waiting_for_permission") {
                item {
                    Card(
                        onClick = {},
                        modifier = Modifier
                            .fillMaxWidth()
                            .border(BorderStroke(1.dp, Color(0x4DFF3B30)), shape = RoundedCornerShape(24.dp))
                            .padding(vertical = 4.dp),
                        backgroundPainter = CardDefaults.cardBackgroundPainter(
                            startBackgroundColor = Color(0x22FF3B30),
                            endBackgroundColor = Color(0x08FF3B30)
                        )
                    ) {
                        Column(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalAlignment = Alignment.CenterHorizontally
                        ) {
                            Text(
                                text = "PERMISSION REQUIRED",
                                style = MaterialTheme.typography.caption2.copy(
                                    fontWeight = FontWeight.ExtraBold,
                                    fontSize = 9.sp,
                                    letterSpacing = 1.sp
                                ),
                                color = Color(0xFFFF3B30)
                            )
                            Spacer(modifier = Modifier.height(4.dp))
                            Text(
                                text = state.tool_name ?: "Unknown tool",
                                style = MaterialTheme.typography.body2.copy(
                                    fontWeight = FontWeight.Bold,
                                    fontSize = 13.sp
                                ),
                                color = Color.White,
                                textAlign = TextAlign.Center
                            )
                            state.tool_input?.get("command")?.let { cmd ->
                                Spacer(modifier = Modifier.height(2.dp))
                                Text(
                                    text = cmd.toString(),
                                    style = MaterialTheme.typography.caption2.copy(
                                        fontSize = 10.sp,
                                        lineHeight = 12.sp
                                    ),
                                    color = Color.White.copy(alpha = 0.7f),
                                    maxLines = 3,
                                    overflow = TextOverflow.Ellipsis,
                                    textAlign = TextAlign.Center
                                )
                            }
                        }
                    }
                }

                // Action Buttons
                item {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 6.dp),
                        horizontalArrangement = Arrangement.SpaceEvenly,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Button(
                            onClick = { onSendCommand("n") },
                            colors = ButtonDefaults.buttonColors(
                                backgroundColor = Color(0xFFFF3B30),
                                contentColor = Color.White
                            ),
                            modifier = Modifier.height(40.dp).width(64.dp)
                        ) {
                            Text("DENY", fontWeight = FontWeight.ExtraBold, fontSize = 12.sp)
                        }

                        Button(
                            onClick = { onSendCommand("y") },
                            colors = ButtonDefaults.buttonColors(
                                backgroundColor = Color(0xFF34C759),
                                contentColor = Color.Black
                            ),
                            modifier = Modifier.height(40.dp).width(64.dp)
                        ) {
                            Text("ALLOW", fontWeight = FontWeight.ExtraBold, fontSize = 12.sp)
                        }
                    }
                }
            }

            // Thinking State Spinner
            if (state.status == "thinking") {
                item {
                    Box(
                        modifier = Modifier.padding(vertical = 14.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(36.dp),
                            indicatorColor = BrightYellow,
                            trackColor = Color.White.copy(alpha = 0.1f),
                            strokeWidth = 3.dp
                        )
                    }
                }
                state.last_query?.let { query ->
                    item {
                        Card(
                            onClick = {},
                            modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                            backgroundPainter = CardDefaults.cardBackgroundPainter(
                                startBackgroundColor = Color(0x0DFFFFFF),
                                endBackgroundColor = Color(0x05FFFFFF)
                            )
                        ) {
                            Text(
                                text = "\"$query\"",
                                style = MaterialTheme.typography.caption2.copy(
                                    fontSize = 11.sp,
                                    lineHeight = 14.sp
                                ),
                                color = Color.White.copy(alpha = 0.8f),
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth()
                            )
                        }
                    }
                }
            }

            // Conversation History Button
            item {
                Button(
                    onClick = onHistoryClick,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 24.dp, vertical = 8.dp),
                    colors = ButtonDefaults.buttonColors(
                        backgroundColor = Color.DarkGray.copy(alpha = 0.5f)
                    ),
                    shape = RoundedCornerShape(16.dp)
                ) {
                    Text(
                        text = "HISTORY (${historyList.size})",
                        color = Color.White,
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold
                    )
                }
            }

            // High-Impact Obvious Voice Dictation Button
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Chip(
                    onClick = onVoiceInputClick,
                    label = { 
                        Text(
                            text = "VOICE DICTATION", 
                            fontWeight = FontWeight.Black,
                            color = Color.White,
                            fontSize = 12.sp,
                            letterSpacing = 0.8.sp
                        ) 
                    },
                    secondaryLabel = { 
                        Text(
                            text = "Tap to speak prompt", 
                            fontSize = 9.sp,
                            color = Color.White.copy(alpha = 0.8f)
                        ) 
                    },
                    icon = {
                        MicrophoneIcon(
                            modifier = Modifier.size(20.dp),
                            color = Color.White
                        )
                    },
                    colors = ChipDefaults.chipColors(
                        backgroundColor = Color(0xFF4285F4) // Medium Material Blue
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 4.dp)
                )
            }

            // Quick Actions Header
            item {
                Text(
                    text = "QUICK ACTIONS",
                    style = MaterialTheme.typography.caption2.copy(
                        fontWeight = FontWeight.Bold,
                        fontSize = 8.5.sp,
                        letterSpacing = 1.sp
                    ),
                    color = Color.White.copy(alpha = 0.4f),
                    modifier = Modifier.padding(top = 8.dp, bottom = 4.dp)
                )
            }

            item {
                Chip(
                    onClick = { onSendCommand("yes") },
                    label = { Text("Yes", textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth()) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x1AFFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            item {
                Chip(
                    onClick = { onSendCommand("no") },
                    label = { Text("No", textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth()) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x1AFFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            item {
                Chip(
                    onClick = { onSendCommand("continue") },
                    label = { Text("Continue", textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth()) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x1AFFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            // Redesigned Server Settings Card
            item {
                Spacer(modifier = Modifier.height(12.dp))
                Card(
                    onClick = onConfigureIpClick,
                    modifier = Modifier
                        .fillMaxWidth()
                        .border(BorderStroke(1.dp, Color(0x2BFFFFFF)), shape = RoundedCornerShape(20.dp))
                        .padding(vertical = 4.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x14FFFFFF),
                        endBackgroundColor = Color(0x04FFFFFF)
                    )
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.SpaceBetween,
                            modifier = Modifier.fillMaxWidth()
                        ) {
                            Text(
                                text = "SERVER CONNECTION",
                                style = MaterialTheme.typography.caption2.copy(
                                    fontWeight = FontWeight.ExtraBold,
                                    fontSize = 8.5.sp,
                                    letterSpacing = 1.sp
                                ),
                                color = Color.White.copy(alpha = 0.5f)
                            )
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                androidx.compose.foundation.Canvas(modifier = Modifier.size(5.dp)) {
                                    drawCircle(color = BrightGreen)
                                }
                                Spacer(modifier = Modifier.width(4.dp))
                                Text(
                                    text = "FCM V1",
                                    style = MaterialTheme.typography.caption2.copy(
                                        fontSize = 8.sp,
                                        fontWeight = FontWeight.Bold
                                    ),
                                    color = BrightGreen
                                )
                            }
                        }
                        Spacer(modifier = Modifier.height(4.dp))
                        Text(
                            text = serverIp,
                            style = MaterialTheme.typography.body2.copy(
                                fontSize = 11.sp,
                                fontWeight = FontWeight.Bold
                            ),
                            color = Color.White
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = "Tap to change host IP",
                            style = MaterialTheme.typography.caption2.copy(
                                fontSize = 8.5.sp
                            ),
                            color = LightBlue
                        )
                    }
                }
            }
        }
    }
}

/**
 * Custom Vector Canvas Microphone Icon.
 * Vector graphic drawn without external images or emojis.
 */
@Composable
fun MicrophoneIcon(
    modifier: Modifier = Modifier.size(18.dp),
    color: Color = Color.White
) {
    androidx.compose.foundation.Canvas(modifier = modifier) {
        val width = size.width
        val height = size.height
        val strokeWidth = 2.dp.toPx()

        // Capsule Body
        val capsuleWidth = width * 0.38f
        val capsuleHeight = height * 0.50f
        val capsuleLeft = (width - capsuleWidth) / 2f
        val capsuleTop = height * 0.08f
        drawRoundRect(
            color = color,
            topLeft = androidx.compose.ui.geometry.Offset(capsuleLeft, capsuleTop),
            size = androidx.compose.ui.geometry.Size(capsuleWidth, capsuleHeight),
            cornerRadius = androidx.compose.ui.geometry.CornerRadius(capsuleWidth / 2f)
        )

        // U-shaped Holder Arc
        val arcTop = height * 0.28f
        val arcHeight = height * 0.40f
        drawArc(
            color = color,
            startAngle = 0f,
            sweepAngle = 180f,
            useCenter = false,
            topLeft = androidx.compose.ui.geometry.Offset(width * 0.18f, arcTop),
            size = androidx.compose.ui.geometry.Size(width * 0.64f, arcHeight),
            style = androidx.compose.ui.graphics.drawscope.Stroke(width = strokeWidth)
        )

        // Vertical Stem
        val stemTop = arcTop + arcHeight
        val stemBottom = height * 0.88f
        drawLine(
            color = color,
            start = androidx.compose.ui.geometry.Offset(width / 2f, stemTop),
            end = androidx.compose.ui.geometry.Offset(width / 2f, stemBottom),
            strokeWidth = strokeWidth
        )

        // Base Line
        val baseWidth = width * 0.44f
        drawLine(
            color = color,
            start = androidx.compose.ui.geometry.Offset((width - baseWidth) / 2f, stemBottom),
            end = androidx.compose.ui.geometry.Offset((width + baseWidth) / 2f, stemBottom),
            strokeWidth = strokeWidth
        )
    }
}

/**
 * Status Help Modal Screen explaining all agent state definitions.
 */
@Composable
fun StatusHelpModal(
    onClose: () -> Unit
) {
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val coroutineScope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                coroutineScope.launch {
                    kotlinx.coroutines.delay(50)
                    try { focusRequester.requestFocus() } catch (e: Exception) {}
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
                .focusRequester(focusRequester)
                .focusable(),
            state = listState,
            horizontalAlignment = Alignment.CenterHorizontally,
            contentPadding = PaddingValues(
                top = 26.dp,
                start = 12.dp,
                end = 12.dp,
                bottom = 26.dp
            )
        ) {
            item {
                Text(
                    text = "STATUS EXPLANATIONS",
                    style = MaterialTheme.typography.caption1.copy(
                        fontWeight = FontWeight.ExtraBold,
                        letterSpacing = 1.2.sp,
                        fontSize = 11.sp
                    ),
                    color = Color(0xFF8AB4F8),
                    modifier = Modifier.padding(bottom = 6.dp)
                )
            }

            // IDLE Explanation
            item {
                Card(
                    onClick = {},
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x10FFFFFF),
                        endBackgroundColor = Color(0x04FFFFFF)
                    )
                ) {
                    Column {
                        Text(
                            text = "IDLE",
                            style = MaterialTheme.typography.caption2.copy(fontWeight = FontWeight.Bold),
                            color = Color.Gray
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = "The agent is waiting for a new prompt or session to start.",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 10.sp, lineHeight = 13.sp),
                            color = Color.White.copy(alpha = 0.8f)
                        )
                    }
                }
            }

            // THINKING Explanation
            item {
                Card(
                    onClick = {},
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x10FFB800),
                        endBackgroundColor = Color(0x03FFB800)
                    )
                ) {
                    Column {
                        Text(
                            text = "THINKING...",
                            style = MaterialTheme.typography.caption2.copy(fontWeight = FontWeight.Bold),
                            color = BrightYellow
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = "The agent is actively processing your prompt or running background commands.",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 10.sp, lineHeight = 13.sp),
                            color = Color.White.copy(alpha = 0.8f)
                        )
                    }
                }
            }

            // NEEDS AUTH Explanation
            item {
                Card(
                    onClick = {},
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x10FF3B30),
                        endBackgroundColor = Color(0x03FF3B30)
                    )
                ) {
                    Column {
                        Text(
                            text = "NEEDS AUTH",
                            style = MaterialTheme.typography.caption2.copy(fontWeight = FontWeight.Bold),
                            color = Color(0xFFFF3B30)
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = "The agent paused and needs your permission (ALLOW/DENY) to execute a terminal command.",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 10.sp, lineHeight = 13.sp),
                            color = Color.White.copy(alpha = 0.8f)
                        )
                    }
                }
            }

            // READY Explanation
            item {
                Card(
                    onClick = {},
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x1034C759),
                        endBackgroundColor = Color(0x0334C759)
                    )
                ) {
                    Column {
                        Text(
                            text = "READY",
                            style = MaterialTheme.typography.caption2.copy(fontWeight = FontWeight.Bold),
                            color = BrightGreen
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = "Task completed! The agent has finished writing its response and is ready for follow-up.",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 10.sp, lineHeight = 13.sp),
                            color = Color.White.copy(alpha = 0.8f)
                        )
                    }
                }
            }

            // Close Help Button
            item {
                Spacer(modifier = Modifier.height(6.dp))
                Chip(
                    onClick = onClose,
                    label = { Text("CLOSE HELP", fontSize = 11.sp, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth()) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                    modifier = Modifier.fillMaxWidth()
                )
            }
        }
    }
}

/**
 * Premium Brand Header for Agent Monitor on Wear OS.
 * Glassmorphic badge with custom vector prompt chevron mark.
 */
@Composable
fun AgentMonitorBrandHeader() {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = 4.dp)
    ) {
        Box(
            modifier = Modifier
                .background(
                    color = Color(0x1F8AB4F8),
                    shape = RoundedCornerShape(16.dp)
                )
                .border(BorderStroke(1.dp, Color(0x4D8AB4F8)), shape = RoundedCornerShape(16.dp))
                .padding(horizontal = 12.dp, vertical = 4.dp)
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.Center
            ) {
                // High-tech terminal prompt symbol (>_)
                androidx.compose.foundation.Canvas(modifier = Modifier.size(10.dp)) {
                    val stroke = 2.dp.toPx()
                    val path = androidx.compose.ui.graphics.Path().apply {
                        moveTo(0f, 0f)
                        lineTo(size.width * 0.6f, size.height * 0.5f)
                        lineTo(0f, size.height)
                    }
                    drawPath(
                        path = path,
                        color = Color(0xFF8AB4F8),
                        style = androidx.compose.ui.graphics.drawscope.Stroke(width = stroke)
                    )
                }
                Spacer(modifier = Modifier.width(6.dp))
                Text(
                    text = "AGENT MONITOR",
                    style = MaterialTheme.typography.caption1.copy(
                        letterSpacing = 1.8.sp,
                        fontWeight = FontWeight.Black,
                        fontSize = 12.sp
                    ),
                    color = Color.White
                )
            }
        }
    }
}

/**
 * Full-screen comfortable Reader View for long assistant responses.
 * Clean, readable typography with parsed Markdown formatting and rotary scroll support.
 */
@Composable
fun ResponseReaderScreen(
    item: HistoryItem,
    onBackClick: () -> Unit,
    onVoiceInputClick: () -> Unit
) {
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val coroutineScope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                coroutineScope.launch {
                    kotlinx.coroutines.delay(50)
                    try { focusRequester.requestFocus() } catch (e: Exception) {}
                }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
        }
    }

    val cleanedText = remember(item.response) {
        MarkdownFormatter.clean(item.response)
    }

    Scaffold(
        timeText = { TimeText() },
        vignette = { Vignette(vignettePosition = VignettePosition.TopAndBottom) },
        positionIndicator = { PositionIndicator(scalingLazyListState = listState) }
    ) {
        ScalingLazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .focusRequester(focusRequester)
                .focusable(),
            state = listState,
            horizontalAlignment = Alignment.CenterHorizontally,
            contentPadding = PaddingValues(
                top = 26.dp,
                start = 12.dp,
                end = 12.dp,
                bottom = 26.dp
            )
        ) {
            // Back Navigation Button
            item {
                Chip(
                    onClick = onBackClick,
                    label = { Text("< BACK TO FEED", fontSize = 11.sp, fontWeight = FontWeight.Bold) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(bottom = 6.dp)
                )
            }

            // Query Context Card (if present)
            if (!item.query.isNullOrEmpty()) {
                item {
                    Card(
                        onClick = {},
                        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                        backgroundPainter = CardDefaults.cardBackgroundPainter(
                            startBackgroundColor = Color(0x0DFFFFFF),
                            endBackgroundColor = Color(0x05FFFFFF)
                        )
                    ) {
                        Column {
                            Text(
                                text = "PROMPT",
                                style = MaterialTheme.typography.caption2.copy(
                                    fontSize = 8.sp,
                                    fontWeight = FontWeight.Bold,
                                    letterSpacing = 1.sp
                                ),
                                color = LightBlue
                            )
                            Spacer(modifier = Modifier.height(2.dp))
                            Text(
                                text = item.query,
                                style = MaterialTheme.typography.caption2.copy(
                                    fontSize = 10.5.sp,
                                    lineHeight = 13.sp
                                ),
                                color = Color.White.copy(alpha = 0.8f)
                            )
                        }
                    }
                }
            }

            // Reader Title
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = "AGENT RESPONSE",
                    style = MaterialTheme.typography.caption2.copy(
                        fontWeight = FontWeight.ExtraBold,
                        fontSize = 9.sp,
                        letterSpacing = 1.sp
                    ),
                    color = BrightGreen
                )
            }

            // Full Parsed Text Body
            item {
                Card(
                    onClick = {},
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 4.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x1000D2FF),
                        endBackgroundColor = Color(0x0200D2FF)
                    )
                ) {
                    Text(
                        text = if (cleanedText.isNotEmpty()) cleanedText else "No response body available.",
                        style = MaterialTheme.typography.body2.copy(
                            fontSize = 12.sp,
                            lineHeight = 16.sp,
                            fontWeight = FontWeight.Normal
                        ),
                        color = Color.White,
                        textAlign = TextAlign.Start
                    )
                }
            }

            // Dictate Follow-Up Button inside Reader
            item {
                Spacer(modifier = Modifier.height(6.dp))
                Chip(
                    onClick = onVoiceInputClick,
                    label = { Text("VOICE DICTATION", fontWeight = FontWeight.Bold, fontSize = 11.sp) },
                    icon = { MicrophoneIcon(modifier = Modifier.size(18.dp), color = Color.White) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0xFF4285F4)),
                    modifier = Modifier.fillMaxWidth()
                )
            }

            // Bottom Back Button
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Chip(
                    onClick = onBackClick,
                    label = { Text("DONE READING", fontSize = 11.sp, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth()) },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x1AFFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }
        }
    }
}
