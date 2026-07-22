package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.BorderStroke
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
import androidx.compose.ui.input.rotary.onRotaryScrollEvent
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
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.LightBlue
import kotlinx.coroutines.launch

@Composable
fun ServerConfigScreen(
    currentLocalIp: String,
    currentTailscaleIp: String,
    onDictateLocalIp: () -> Unit,
    onDictateTailscaleIp: () -> Unit,
    onSaveClick: () -> Unit,
    onCancelClick: () -> Unit
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
            contentPadding = PaddingValues(
                top = 28.dp,
                start = 12.dp,
                end = 12.dp,
                bottom = 28.dp
            )
        ) {
            // Screen Header
            item {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    modifier = Modifier.padding(bottom = 6.dp)
                ) {
                    Text(
                        text = "SERVER CONFIG",
                        style = MaterialTheme.typography.caption1.copy(
                            letterSpacing = 1.5.sp,
                            fontWeight = FontWeight.ExtraBold
                        ),
                        color = Color(0xFF8AB4F8)
                    )
                    Text(
                        text = "Configure Bridge Host IPs",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp),
                        color = Color.White.copy(alpha = 0.5f)
                    )
                }
            }

            // Local IP Card & Dictate Button
            item {
                Card(
                    onClick = onDictateLocalIp,
                    modifier = Modifier
                        .fillMaxWidth()
                        .border(BorderStroke(1.dp, Color(0x338AB4F8)), shape = RoundedCornerShape(20.dp))
                        .padding(vertical = 3.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x1A8AB4F8),
                        endBackgroundColor = Color(0x048AB4F8)
                    )
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(
                            text = "LOCAL NETWORK IP",
                            style = MaterialTheme.typography.caption2.copy(
                                fontWeight = FontWeight.Bold,
                                fontSize = 8.5.sp,
                                letterSpacing = 1.sp
                            ),
                            color = LightBlue
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = currentLocalIp.ifEmpty { "Not Set" },
                            style = MaterialTheme.typography.body2.copy(
                                fontSize = 12.sp,
                                fontWeight = FontWeight.Bold
                            ),
                            color = Color.White
                        )
                        Spacer(modifier = Modifier.height(4.dp))
                        Text(
                            text = "Tap to Dictate New Local IP",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp),
                            color = Color.White.copy(alpha = 0.6f)
                        )
                    }
                }
            }

            // Tailscale IP Card & Dictate Button
            item {
                Card(
                    onClick = onDictateTailscaleIp,
                    modifier = Modifier
                        .fillMaxWidth()
                        .border(BorderStroke(1.dp, Color(0x338AB4F8)), shape = RoundedCornerShape(20.dp))
                        .padding(vertical = 3.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x188AB4F8),
                        endBackgroundColor = Color(0x048AB4F8)
                    )
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(
                            text = "TAILSCALE MESH IP",
                            style = MaterialTheme.typography.caption2.copy(
                                fontWeight = FontWeight.Bold,
                                fontSize = 8.5.sp,
                                letterSpacing = 1.sp
                            ),
                            color = LightBlue
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = currentTailscaleIp.ifEmpty { "Not Set" },
                            style = MaterialTheme.typography.body2.copy(
                                fontSize = 12.sp,
                                fontWeight = FontWeight.Bold
                            ),
                            color = Color.White
                        )
                        Spacer(modifier = Modifier.height(4.dp))
                        Text(
                            text = "Tap to Dictate Tailscale IP",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp),
                            color = Color.White.copy(alpha = 0.6f)
                        )
                    }
                }
            }

            // Save & Reconnect Action Button
            item {
                Spacer(modifier = Modifier.height(6.dp))
                Chip(
                    onClick = onSaveClick,
                    label = { 
                        Text(
                            text = "SAVE & RECONNECT", 
                            fontWeight = FontWeight.Bold, 
                            fontSize = 11.sp,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        ) 
                    },
                    colors = ChipDefaults.chipColors(backgroundColor = Color(0xFF4285F4)),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }

            // Cancel Action Button
            item {
                Chip(
                    onClick = onCancelClick,
                    label = { 
                        Text(
                            text = "CANCEL", 
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
