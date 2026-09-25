package com.gabriel.agentwatch

import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.navigation.NavType
import androidx.navigation.navArgument
import androidx.wear.compose.material.CircularProgressIndicator
import androidx.wear.compose.material.CompactChip
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import androidx.wear.compose.navigation.SwipeDismissableNavHost
import androidx.wear.compose.navigation.composable
import androidx.wear.compose.navigation.rememberSwipeDismissableNavController
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.PushRegistration
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.screens.*
import com.gabriel.agentwatch.ui.theme.AgentWatchTheme

class MainActivity : ComponentActivity() {
    private val prefs by lazy { Prefs(this) }
    private var pendingDeepLinkPaneId by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        pendingDeepLinkPaneId = intent?.getStringExtra("pane_id")

        // Request notification permission for Android 13+
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                registerForActivityResult(ActivityResultContracts.RequestPermission()) { isGranted ->
                    Log.d("MainActivity", "Notification permission granted: $isGranted")
                }.launch(android.Manifest.permission.POST_NOTIFICATIONS)
            }
        }

        fetchAndRegisterFcmToken()

        setContent {
            AgentWatchTheme {
                val navController = rememberSwipeDismissableNavController()
                val uiState by RelayRepository.state.collectAsState()

                val startDestination = remember {
                    if (!prefs.isPaired) "pairing" else "agents"
                }

                val currentTargetPaneId = pendingDeepLinkPaneId
                LaunchedEffect(currentTargetPaneId) {
                    if (!currentTargetPaneId.isNullOrBlank() && prefs.isPaired) {
                        pendingDeepLinkPaneId = null
                        navController.navigate("agent/${Uri.encode(currentTargetPaneId)}") {
                            popUpTo("agents") { inclusive = false }
                        }
                    }
                }

                SwipeDismissableNavHost(
                    navController = navController,
                    startDestination = startDestination
                ) {
                    composable("pairing") {
                        PairingScreen(
                            onPairedSuccess = {
                                navController.navigate("agents") {
                                    popUpTo("pairing") { inclusive = true }
                                }
                            }
                        )
                    }

                    composable("agents") {
                        AgentListScreen(
                            uiState = uiState,
                            onAgentClick = { paneId ->
                                navController.navigate("agent/${Uri.encode(paneId)}")
                            },
                            onHistoryClick = {
                                navController.navigate("history")
                            },
                            onSettingsClick = {
                                navController.navigate("pairing")
                            }
                        )
                    }

                    composable(
                        route = "agent/{paneId}",
                        arguments = listOf(navArgument("paneId") { type = NavType.StringType })
                    ) { backStackEntry ->
                        val encodedPaneId = backStackEntry.arguments?.getString("paneId") ?: ""
                        val paneId = Uri.decode(encodedPaneId)
                        val agent = uiState.agents.find { it.pane_id == paneId }

                        if (agent != null) {
                            AgentDetailScreen(
                                agent = agent,
                                onHistoryClick = { pId ->
                                    navController.navigate("history?paneId=${Uri.encode(pId)}")
                                },
                                onBackClick = { navController.popBackStack() }
                            )
                        } else if (uiState.agents.isEmpty()) {
                            Box(
                                modifier = Modifier.fillMaxSize(),
                                contentAlignment = Alignment.Center
                            ) {
                                CircularProgressIndicator()
                            }
                        } else {
                            Box(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .padding(16.dp),
                                contentAlignment = Alignment.Center
                            ) {
                                Column(
                                    horizontalAlignment = Alignment.CenterHorizontally,
                                    verticalArrangement = Arrangement.Center
                                ) {
                                    Text(
                                        text = "Agent closed",
                                        style = MaterialTheme.typography.body2,
                                        textAlign = TextAlign.Center
                                    )
                                    Spacer(modifier = Modifier.height(8.dp))
                                    CompactChip(
                                        label = { Text("Back to list") },
                                        onClick = { navController.popBackStack() }
                                    )
                                }
                            }
                        }
                    }

                    composable(
                        route = "history?paneId={paneId}",
                        arguments = listOf(
                            navArgument("paneId") {
                                type = NavType.StringType
                                nullable = true
                                defaultValue = null
                            }
                        )
                    ) { backStackEntry ->
                        val encodedPaneId = backStackEntry.arguments?.getString("paneId")
                        val paneId = encodedPaneId?.let { Uri.decode(it) }

                        HistoryListScreen(
                            paneId = paneId,
                            historyItems = uiState.history,
                            onSelectHistoryItem = { item ->
                                navController.navigate("reader/${Uri.encode(item.id)}")
                            },
                            onBackClick = { navController.popBackStack() }
                        )
                    }

                    composable("history") {
                        HistoryListScreen(
                            paneId = null,
                            historyItems = uiState.history,
                            onSelectHistoryItem = { item ->
                                navController.navigate("reader/${Uri.encode(item.id)}")
                            },
                            onBackClick = { navController.popBackStack() }
                        )
                    }

                    composable(
                        route = "reader/{historyId}",
                        arguments = listOf(navArgument("historyId") { type = NavType.StringType })
                    ) { backStackEntry ->
                        val historyId = Uri.decode(backStackEntry.arguments?.getString("historyId") ?: "")
                        val item = uiState.history.find { it.id == historyId }

                        if (item != null) {
                            ResponseReaderScreen(
                                item = item,
                                onBackClick = { navController.popBackStack() }
                            )
                        } else if (uiState.history.isEmpty()) {
                            Box(
                                modifier = Modifier.fillMaxSize(),
                                contentAlignment = Alignment.Center
                            ) {
                                CircularProgressIndicator()
                            }
                        } else {
                            Box(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .padding(16.dp),
                                contentAlignment = Alignment.Center
                            ) {
                                Column(
                                    horizontalAlignment = Alignment.CenterHorizontally,
                                    verticalArrangement = Arrangement.Center
                                ) {
                                    Text(
                                        text = "Item not found",
                                        style = MaterialTheme.typography.body2,
                                        textAlign = TextAlign.Center
                                    )
                                    Spacer(modifier = Modifier.height(8.dp))
                                    CompactChip(
                                        label = { Text("Back") },
                                        onClick = { navController.popBackStack() }
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        val paneId = intent.getStringExtra("pane_id")
        if (!paneId.isNullOrBlank()) {
            pendingDeepLinkPaneId = paneId
        }
    }

    override fun onStart() {
        super.onStart()
        // Save battery: start SSE when foregrounded
        RelayRepository.start(this)
    }

    override fun onStop() {
        super.onStop()
        // Save battery: disconnect SSE when app goes to background
        RelayRepository.stop()
    }

    private fun fetchAndRegisterFcmToken() {
        try {
            com.google.firebase.messaging.FirebaseMessaging.getInstance().token
                .addOnCompleteListener { task ->
                    if (task.isSuccessful) {
                        val token = task.result
                        if (!token.isNullOrBlank()) {
                            // Stores it and registers it with the relay; recorded only on success, retried on failure.
                            PushRegistration.onToken(this, token)
                        }
                    } else {
                        Log.w("FCM", "Fetching FCM registration token failed", task.exception)
                    }
                }
        } catch (e: Exception) {
            Log.e("FCM", "Error initializing FirebaseMessaging token", e)
        }
    }
}
