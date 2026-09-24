package com.gabriel.agentwatch.ui.screens

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
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.wear.compose.foundation.lazy.ScalingLazyColumn
import androidx.wear.compose.foundation.lazy.rememberScalingLazyListState
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.mikepenz.markdown.m2.Markdown
import com.mikepenz.markdown.m2.markdownColor
import com.mikepenz.markdown.m2.markdownTypography
import kotlinx.coroutines.launch

@Composable
fun ResponseReaderScreen(
    item: HistoryItem,
    onBackClick: () -> Unit
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
            // Header / Agent label
            item {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(bottom = 4.dp)) {
                    Text(
                        text = item.label.uppercase(),
                        style = MaterialTheme.typography.caption1.copy(fontWeight = FontWeight.ExtraBold, letterSpacing = 1.sp),
                        color = LightBlue,
                        textAlign = TextAlign.Center
                    )
                    Text(
                        text = "${item.agent} · ${item.source}",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp),
                        color = Color.White.copy(alpha = 0.5f)
                    )
                }
            }

            // Query Card if available
            if (!item.query.isNullOrBlank()) {
                item {
                    Card(
                        onClick = {},
                        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                        backgroundPainter = CardDefaults.cardBackgroundPainter(
                            startBackgroundColor = Color(0x188AB4F8),
                            endBackgroundColor = Color(0x048AB4F8)
                        )
                    ) {
                        Column {
                            Text(
                                text = "QUERY",
                                style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp, fontWeight = FontWeight.Bold),
                                color = LightBlue
                            )
                            Spacer(modifier = Modifier.height(2.dp))
                            Text(
                                text = item.query,
                                style = MaterialTheme.typography.body2.copy(fontSize = 11.sp),
                                color = Color.White
                            )
                        }
                    }
                }
            }

            // Response Label
            item {
                Spacer(modifier = Modifier.height(2.dp))
                Text(
                    text = "RESPONSE",
                    style = MaterialTheme.typography.caption2.copy(fontWeight = FontWeight.ExtraBold, fontSize = 8.5.sp, letterSpacing = 1.sp),
                    color = BrightGreen
                )
            }

            // Response Body: Markdown if transcript, plain text if screen
            item {
                Card(
                    onClick = {},
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x1010B981),
                        endBackgroundColor = Color(0x0210B981)
                    )
                ) {
                    if (item.source == "transcript") {
                        Markdown(
                            content = item.response,
                            colors = markdownColor(
                                text = Color.White,
                                codeText = Color.LightGray,
                                codeBackground = Color(0xFF2B2B2B)
                            ),
                            typography = markdownTypography(
                                text = TextStyle(fontSize = 12.sp, lineHeight = 15.sp, color = Color.White),
                                code = TextStyle(fontSize = 10.sp, lineHeight = 13.sp),
                                h1 = TextStyle(fontSize = 16.sp, fontWeight = FontWeight.Bold),
                                h2 = TextStyle(fontSize = 14.sp, fontWeight = FontWeight.Bold),
                                h3 = TextStyle(fontSize = 13.sp, fontWeight = FontWeight.Bold),
                                paragraph = TextStyle(fontSize = 12.sp, lineHeight = 15.sp)
                            ),
                            modifier = Modifier.fillMaxWidth()
                        )
                    } else {
                        Text(
                            text = item.response,
                            style = MaterialTheme.typography.body2.copy(fontSize = 11.sp, lineHeight = 14.sp),
                            color = Color.White
                        )
                    }
                }
            }

            // Done Button
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Chip(
                    onClick = onBackClick,
                    label = {
                        Text(
                            text = "DONE READING",
                            fontSize = 11.sp,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        )
                    },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }
        }
    }
}
