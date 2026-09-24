package com.gabriel.agentwatch.ui.screens

import android.app.Activity
import android.content.Intent
import android.os.Build
import android.speech.RecognizerIntent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayClient
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.LightBlue
import com.gabriel.agentwatch.ui.theme.Red400
import kotlinx.coroutines.launch

@Composable
fun PairingScreen(
    onPairedSuccess: () -> Unit
) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    val coroutineScope = rememberCoroutineScope()
    val listState = rememberScalingLazyListState()
    val focusRequester = remember { FocusRequester() }
    val lifecycleOwner = LocalLifecycleOwner.current

    var relayUrl by remember {
        mutableStateOf(prefs.relayUrl.ifBlank { "https://relay.example.com" })
    }
    var pairCode by remember { mutableStateOf("") }
    var isLoading by remember { mutableStateOf(false) }
    var errorMessage by remember { mutableStateOf<String?>(null) }

    // Speech input launcher for Relay URL
    val urlLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val spokenText = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
            if (!spokenText.isNullOrBlank()) {
                var clean = spokenText.trim().lowercase().replace(" ", "")
                if (!clean.startsWith("http://") && !clean.startsWith("https://")) {
                    clean = "https://$clean"
                }
                relayUrl = clean
            }
        }
    }

    // Speech input launcher for Pairing Code
    val codeLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val spokenText = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
            if (!spokenText.isNullOrBlank()) {
                val digitsOnly = spokenText.filter { it.isDigit() }
                pairCode = digitsOnly.take(6)
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
            contentPadding = PaddingValues(top = 28.dp, start = 12.dp, end = 12.dp, bottom = 28.dp)
        ) {
            // Header
            item {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(bottom = 6.dp)) {
                    Text(
                        text = "PAIR WATCH",
                        style = MaterialTheme.typography.caption1.copy(
                            letterSpacing = 1.5.sp,
                            fontWeight = FontWeight.ExtraBold
                        ),
                        color = LightBlue
                    )
                    Text(
                        text = "Connect to Agent Watch Relay",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp),
                        color = Color.White.copy(alpha = 0.5f)
                    )
                }
            }

            // Error message banner
            if (errorMessage != null) {
                item {
                    Text(
                        text = errorMessage ?: "",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.5.sp, fontWeight = FontWeight.Bold),
                        color = Red400,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.padding(vertical = 4.dp)
                    )
                }
            }

            // Relay URL Card
            item {
                Card(
                    onClick = {
                        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                            putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate Relay URL")
                        }
                        urlLauncher.launch(intent)
                    },
                    modifier = Modifier
                        .fillMaxWidth()
                        .border(BorderStroke(1.dp, Color(0x338AB4F8)), shape = RoundedCornerShape(16.dp))
                        .padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x188AB4F8),
                        endBackgroundColor = Color(0x048AB4F8)
                    )
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(
                            text = "RELAY URL",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp, fontWeight = FontWeight.Bold),
                            color = LightBlue
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        Text(
                            text = relayUrl.ifBlank { "Tap to set" },
                            style = MaterialTheme.typography.body2.copy(fontSize = 11.sp),
                            color = Color.White,
                            maxLines = 1
                        )
                    }
                }
            }

            // Pairing Code Card
            item {
                Card(
                    onClick = {
                        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                            putExtra(RecognizerIntent.EXTRA_PROMPT, "Say the 6-digit pairing code")
                        }
                        codeLauncher.launch(intent)
                    },
                    modifier = Modifier
                        .fillMaxWidth()
                        .border(BorderStroke(1.dp, Color(0x338AB4F8)), shape = RoundedCornerShape(16.dp))
                        .padding(vertical = 2.dp),
                    backgroundPainter = CardDefaults.cardBackgroundPainter(
                        startBackgroundColor = Color(0x188AB4F8),
                        endBackgroundColor = Color(0x048AB4F8)
                    )
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(
                            text = "6-DIGIT PAIRING CODE",
                            style = MaterialTheme.typography.caption2.copy(fontSize = 8.5.sp, fontWeight = FontWeight.Bold),
                            color = LightBlue
                        )
                        Spacer(modifier = Modifier.height(2.dp))
                        val formattedCode = if (pairCode.length == 6) {
                            "${pairCode.take(3)} · ${pairCode.takeLast(3)}"
                        } else if (pairCode.isNotEmpty()) {
                            pairCode
                        } else {
                            "Tap to dictate code"
                        }
                        Text(
                            text = formattedCode,
                            style = MaterialTheme.typography.body1.copy(fontSize = 14.sp, fontWeight = FontWeight.Bold),
                            color = if (pairCode.length == 6) BrightGreen else Color.White
                        )
                    }
                }
            }

            // Pair Button
            item {
                Spacer(modifier = Modifier.height(4.dp))
                Chip(
                    onClick = {
                        if (pairCode.length != 6) {
                            errorMessage = "Please enter all 6 digits"
                            return@Chip
                        }
                        if (relayUrl.isBlank()) {
                            errorMessage = "Relay URL cannot be empty"
                            return@Chip
                        }

                        isLoading = true
                        errorMessage = null

                        coroutineScope.launch {
                            val client = RelayClient(baseUrl = relayUrl)
                            val deviceName = "${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} ${Build.MODEL}"
                            val result = client.pair(code = pairCode, deviceName = deviceName)

                            result.fold(
                                onSuccess = { resp ->
                                    prefs.relayUrl = relayUrl
                                    prefs.deviceId = resp.device_id
                                    prefs.deviceToken = resp.device_token
                                    isLoading = false

                                    // Register FCM token if already obtained
                                    val fcm = prefs.fcmToken
                                    if (!fcm.isNullOrBlank()) {
                                        launch {
                                            val authedClient = RelayClient(relayUrl, resp.device_token)
                                            authedClient.registerPush(fcm)
                                            prefs.fcmRegisteredToken = fcm
                                        }
                                    }

                                    RelayRepository.resetClient()
                                    RelayRepository.start(context)
                                    onPairedSuccess()
                                },
                                onFailure = { error ->
                                    isLoading = false
                                    errorMessage = error.message ?: "Pairing failed"
                                }
                            )
                        }
                    },
                    enabled = !isLoading && pairCode.length == 6,
                    label = {
                        Text(
                            text = if (isLoading) "PAIRING..." else "PAIR WATCH",
                            fontWeight = FontWeight.Bold,
                            fontSize = 11.sp,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth()
                        )
                    },
                    colors = ChipDefaults.chipColors(
                        backgroundColor = if (pairCode.length == 6) BrightGreen else Color(0xFF2563EB)
                    ),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                )
            }
        }
    }
}
