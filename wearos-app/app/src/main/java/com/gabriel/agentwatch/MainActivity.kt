package com.gabriel.agentwatch

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.speech.RecognizerIntent
import android.util.Log
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.*
import androidx.wear.compose.navigation.SwipeDismissableNavHost
import androidx.wear.compose.navigation.composable
import androidx.wear.compose.navigation.currentBackStackEntryAsState
import androidx.wear.compose.navigation.rememberSwipeDismissableNavController
import com.gabriel.agentwatch.network.SseClient
import com.gabriel.agentwatch.ui.screens.MainAgentFeedScreen
import com.gabriel.agentwatch.ui.screens.ResponseReaderScreen
import com.gabriel.agentwatch.ui.screens.ServerConfigScreen
import com.gabriel.agentwatch.ui.screens.StatusHelpModal
import com.gabriel.agentwatch.ui.theme.AgentWatchTheme

class MainActivity : ComponentActivity() {

    private var sseClient: SseClient? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Request notification permission for Android 13+ (Tiramisu)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val requestPermissionLauncher = registerForActivityResult(
                ActivityResultContracts.RequestPermission()
            ) { isGranted: Boolean ->
                if (isGranted) {
                    Log.d("MainActivity", "Notification permission granted")
                } else {
                    Log.e("MainActivity", "Notification permission denied")
                }
            }
            if (checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) !=
                PackageManager.PERMISSION_GRANTED
            ) {
                requestPermissionLauncher.launch(android.Manifest.permission.POST_NOTIFICATIONS)
            }
        }

        setContent {
            AgentWatchTheme {
                val context = this
                val sharedPreferences = remember {
                    context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)
                }

                // Retrieve saved server IPs
                var localIp by remember {
                    mutableStateOf(sharedPreferences.getString("local_ip", "192.168.1.20") ?: "192.168.1.20")
                }
                var tailscaleIp by remember {
                    mutableStateOf(sharedPreferences.getString("tailscale_ip", "100.64.0.1") ?: "100.64.0.1")
                }

                var editingField by remember { mutableStateOf(0) }
                var tempLocalIpInput by remember { mutableStateOf(localIp) }
                var tempTailscaleIpInput by remember { mutableStateOf(tailscaleIp) }

                // Manage SSE client lifecycle reacting to serverIp changes
                val stateFlow = remember(localIp, tailscaleIp) {
                    sseClient?.stopListening()
                    val client = SseClient(localIp, tailscaleIp)
                    sseClient = client
                    client.startListening()
                    client.stateFlow
                }

                val agentState by stateFlow.collectAsState()

                // Speech-to-text launcher for dictating prompts
                val voiceLauncher = rememberLauncherForActivityResult(
                    contract = ActivityResultContracts.StartActivityForResult()
                ) { result ->
                    if (result.resultCode == Activity.RESULT_OK) {
                        val spokenText = result.data?.getStringArrayListExtra(
                            RecognizerIntent.EXTRA_RESULTS
                        )?.firstOrNull()
                        if (!spokenText.isNullOrEmpty()) {
                            sseClient?.sendInputCommand(spokenText) { success ->
                                runOnUiThread {
                                    if (success) {
                                        Toast.makeText(context, "Prompt sent!", Toast.LENGTH_SHORT).show()
                                    } else {
                                        Toast.makeText(context, "Failed to send prompt", Toast.LENGTH_SHORT).show()
                                    }
                                }
                            }
                        }
                    }
                }

                // Speech-to-text launcher for setting the Server IP Address
                val ipInputLauncher = rememberLauncherForActivityResult(
                    contract = ActivityResultContracts.StartActivityForResult()
                ) { result ->
                    if (result.resultCode == Activity.RESULT_OK) {
                        val spokenText = result.data?.getStringArrayListExtra(
                            RecognizerIntent.EXTRA_RESULTS
                        )?.firstOrNull()
                        if (!spokenText.isNullOrEmpty()) {
                            val cleanedIp = spokenText.replace(" ", "").replace(",", ".").trim()
                            if (editingField == 0) {
                                tempLocalIpInput = cleanedIp
                            } else {
                                tempTailscaleIpInput = cleanedIp
                            }
                        }
                    }
                }

                val navController = rememberSwipeDismissableNavController()

                SwipeDismissableNavHost(
                    navController = navController,
                    startDestination = "main"
                ) {
                    composable("main") {
                        MainAgentFeedScreen(
                            state = agentState,
                            onSendCommand = { command ->
                                sseClient?.sendInputCommand(command)
                            },
                            onVoiceInputClick = {
                                val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                                    putExtra(
                                        RecognizerIntent.EXTRA_LANGUAGE_MODEL,
                                        RecognizerIntent.LANGUAGE_MODEL_FREE_FORM
                                    )
                                    putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate your response")
                                }
                                voiceLauncher.launch(intent)
                            },
                            serverIp = localIp,
                            onConfigureIpClick = {
                                tempLocalIpInput = localIp
                                tempTailscaleIpInput = tailscaleIp
                                navController.navigate("server_config")
                            },
                            onSelectHistoryItem = { item ->
                                navController.navigate("reader/${item.id}")
                            },
                            onStatusClick = {
                                navController.navigate("status_help")
                            }
                        )
                    }

                    composable("server_config") {
                        ServerConfigScreen(
                            currentLocalIp = tempLocalIpInput,
                            currentTailscaleIp = tempTailscaleIpInput,
                            onDictateLocalIp = {
                                editingField = 0
                                val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                                    putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                                    putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate Local IP")
                                }
                                ipInputLauncher.launch(intent)
                            },
                            onDictateTailscaleIp = {
                                editingField = 1
                                val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                                    putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                                    putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate Tailscale IP")
                                }
                                ipInputLauncher.launch(intent)
                            },
                            onSaveClick = {
                                sharedPreferences.edit().apply {
                                    putString("local_ip", tempLocalIpInput)
                                    putString("tailscale_ip", tempTailscaleIpInput)
                                    apply()
                                }
                                localIp = tempLocalIpInput
                                tailscaleIp = tempTailscaleIpInput
                                navController.popBackStack()
                                Toast.makeText(context, "IPs Saved & Reconnecting", Toast.LENGTH_SHORT).show()
                            },
                            onCancelClick = {
                                navController.popBackStack()
                            }
                        )
                    }

                    composable("status_help") {
                        StatusHelpModal(
                            onClose = { navController.popBackStack() }
                        )
                    }

                    composable("reader/{itemId}") { backStackEntry ->
                        val itemId = backStackEntry.arguments?.getString("itemId")
                        val item = agentState.history.find { it.id == itemId }
                            ?: if (itemId == "latest" && agentState.last_response != null) {
                                com.gabriel.agentwatch.model.HistoryItem(
                                    id = "latest",
                                    query = agentState.last_query,
                                    response = agentState.last_response
                                )
                            } else {
                                com.gabriel.agentwatch.model.HistoryItem(
                                    id = itemId ?: "latest",
                                    query = agentState.last_query,
                                    response = agentState.last_response ?: ""
                                )
                            }

                        ResponseReaderScreen(
                            item = item,
                            onBackClick = { navController.popBackStack() },
                            onVoiceInputClick = {
                                val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                                    putExtra(
                                        RecognizerIntent.EXTRA_LANGUAGE_MODEL,
                                        RecognizerIntent.LANGUAGE_MODEL_FREE_FORM
                                    )
                                    putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate your response")
                                }
                                voiceLauncher.launch(intent)
                            }
                        )
                    }
                }
            }
        }
    }

    override fun onResume() {
        super.onResume()
        sseClient?.startListening()
    }

    override fun onPause() {
        super.onPause()
        sseClient?.stopListening()
    }

    override fun onDestroy() {
        super.onDestroy()
        sseClient?.stopListening()
    }
}
