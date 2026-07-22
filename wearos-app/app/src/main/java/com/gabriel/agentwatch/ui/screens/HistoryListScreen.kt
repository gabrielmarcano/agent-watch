package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.foundation.lazy.ScalingLazyColumn
import androidx.wear.compose.foundation.lazy.rememberScalingLazyListState
import androidx.wear.compose.foundation.rotary.RotaryScrollableDefaults
import androidx.wear.compose.foundation.rotary.rotaryScrollable
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.gabriel.agentwatch.util.MarkdownFormatter
import kotlinx.coroutines.launch

@Composable
fun HistoryListScreen(
    state: AgentState,
    onSelectHistoryItem: (HistoryItem) -> Unit
) {
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val coroutineScope = rememberCoroutineScope()
    
    val historyList = remember(state.history, state.last_response) {
        if (state.history.isNotEmpty()) {
            state.history.toList().reversed()
        } else if (!state.last_response.isNullOrEmpty()) {
            listOf(
                HistoryItem(
                    id = "latest",
                    query = state.last_query,
                    response = state.last_response,
                    timestamp = state.timestamp
                )
            )
        } else {
            emptyList()
        }
    }

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

    ScalingLazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .rotaryScrollable(RotaryScrollableDefaults.behavior(listState), focusRequester)
            .focusable(),
        state = listState,
        contentPadding = PaddingValues(top = 32.dp, bottom = 48.dp, start = 8.dp, end = 8.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        item {
            Text(
                text = "CONVERSATION HISTORY",
                style = MaterialTheme.typography.caption2.copy(
                    fontWeight = FontWeight.Bold,
                    fontSize = 10.sp,
                    letterSpacing = 1.sp
                ),
                color = Color.White.copy(alpha = 0.6f),
                modifier = Modifier.padding(bottom = 8.dp)
            )
        }

        if (historyList.isEmpty()) {
            item {
                Text(
                    text = "No history available",
                    color = Color.Gray,
                    fontSize = 12.sp,
                    modifier = Modifier.padding(32.dp)
                )
            }
        } else {
            items(historyList.size) { index ->
                val historyItem = historyList[index]
                val cleanedResponse = remember(historyItem.response) {
                    MarkdownFormatter.truncate(historyItem.response, 120)
                }

                Card(
                    onClick = { onSelectHistoryItem(historyItem) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 4.dp, vertical = 4.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0xFF1E1E1E),
                        endBackgroundColor = Color(0xFF1A1A1A)
                    )
                ) {
                    Column {
                        if (!historyItem.query.isNullOrEmpty()) {
                            Text(
                                text = "Q: ${historyItem.query}",
                                color = LightBlue,
                                fontSize = 11.sp,
                                fontWeight = FontWeight.Medium,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis
                            )
                            Spacer(modifier = Modifier.height(4.dp))
                        }
                        Text(
                            text = cleanedResponse,
                            color = Color.White.copy(alpha = 0.9f),
                            fontSize = 12.sp,
                            maxLines = 4,
                            overflow = TextOverflow.Ellipsis,
                            lineHeight = 15.sp
                        )
                        Spacer(modifier = Modifier.height(6.dp))
                        Text(
                            text = "READ FULL ->",
                            color = LightBlue,
                            fontSize = 9.sp,
                            fontWeight = FontWeight.Bold
                        )
                    }
                }
            }
        }
    }
}
