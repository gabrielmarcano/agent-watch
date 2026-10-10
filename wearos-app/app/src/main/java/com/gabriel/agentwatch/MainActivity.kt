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
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.navArgument
import androidx.wear.compose.material3.AppScaffold
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.Text
import androidx.wear.compose.navigation.SwipeDismissableNavHost
import androidx.wear.compose.navigation.composable
import androidx.wear.compose.navigation.rememberSwipeDismissableNavController
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.hostNameFor
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.AuthState
import com.gabriel.agentwatch.network.NotificationIntents
import com.gabriel.agentwatch.network.PushRegistration
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.ui.components.rememberPaneHistory
import com.gabriel.agentwatch.ui.screens.AgentDetailScreen
import com.gabriel.agentwatch.ui.screens.AgentListScreen
import com.gabriel.agentwatch.ui.screens.DictationFlow
import com.gabriel.agentwatch.ui.screens.FullTextScreen
import com.gabriel.agentwatch.ui.screens.HistoryListScreen
import com.gabriel.agentwatch.ui.screens.PairingScreen
import com.gabriel.agentwatch.ui.screens.ResponseReaderScreen
import com.gabriel.agentwatch.ui.screens.SettingsScreen
import com.gabriel.agentwatch.ui.theme.AgentWatchTheme
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map

private object Routes {
    const val PAIRING = "pairing"
    const val AGENTS = "agents"
    const val AGENT = "agent/{host}/{paneId}"
    const val HISTORY = "history?host={host}&paneId={paneId}"
    const val READER = "reader/{historyId}"
    const val SETTINGS = "settings"
    const val FULL_TEXT = "text"
    const val DICTATION = "dictation/{host}/{paneId}"

    /** An agent's host in a path segment: a segment cannot be empty, so no host ("") is `_`, never a host id (contracts §1.6). */
    private const val NO_HOST = "_"

    private fun hostSegment(host: String) = if (host.isEmpty()) NO_HOST else Uri.encode(host)

    fun agent(key: AgentKey) = "agent/${hostSegment(key.host)}/${Uri.encode(key.paneId)}"
    fun history(key: AgentKey?) =
        if (key == null) "history" else "history?host=${hostSegment(key.host)}&paneId=${Uri.encode(key.paneId)}"
    fun reader(id: String) = "reader/${Uri.encode(id)}"
    fun dictation(key: AgentKey) = "dictation/${hostSegment(key.host)}/${Uri.encode(key.paneId)}"

    /** The agent a route's `host` and `paneId` arguments name, or null without a pane. */
    fun agentKey(arguments: Bundle?): AgentKey? {
        val paneId = arguments?.getString("paneId")?.let(Uri::decode)?.takeIf { it.isNotBlank() } ?: return null
        val host = arguments.getString("host")?.let(Uri::decode).orEmpty()
        return AgentKey(if (host == NO_HOST) "" else host, paneId)
    }
}

class MainActivity : ComponentActivity() {
    private val prefs by lazy { Prefs(this) }
    private var pendingDeepLink by mutableStateOf<AgentKey?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        pendingDeepLink = NotificationIntents.agentOf(intent)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
                Log.d("MainActivity", "Notification permission granted: $granted")
            }.launch(android.Manifest.permission.POST_NOTIFICATIONS)
        }
        fetchAndRegisterFcmToken()

        setContent {
            AgentWatchTheme {
                AppScaffold {
                    AgentWatchNavigation(
                        startPaired = remember { prefs.isPaired },
                        prefs = prefs,
                        deepLink = pendingDeepLink,
                        onDeepLinkHandled = { pendingDeepLink = null }
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        NotificationIntents.agentOf(intent)?.let { pendingDeepLink = it }
    }

    override fun onStart() {
        super.onStart()
        RelayRepository.start(this) // SSE only while in the foreground (battery)
    }

    override fun onStop() {
        super.onStop()
        RelayRepository.stop()
    }

    private fun fetchAndRegisterFcmToken() {
        try {
            com.google.firebase.messaging.FirebaseMessaging.getInstance().token
                .addOnCompleteListener { task ->
                    if (task.isSuccessful) {
                        // Stores it and registers it with the relay; recorded only on success, retried on failure.
                        task.result?.takeIf { it.isNotBlank() }?.let { PushRegistration.onToken(this, it) }
                    } else {
                        Log.w("FCM", "Fetching FCM registration token failed", task.exception)
                    }
                }
        } catch (e: Exception) {
            Log.e("FCM", "Error initializing FirebaseMessaging token", e)
        }
    }
}

/**
 * One agent from the live state with its host's name (only when the relay knows several hosts),
 * recomposing only when that agent or the name changes (not on every SSE event).
 */
@Composable
private fun rememberAgent(key: AgentKey): Pair<AgentState, String?>? {
    fun pick(s: UiState) = s.agents.findAgent(key)?.let { it to hostNameFor(s.hosts, it.host) }
    val flow = remember(key) { RelayRepository.state.map(::pick).distinctUntilChanged() }
    // The current value only seeds the first frame; the collection keeps it up to date.
    val initial = remember(key) { pick(RelayRepository.state.value) }
    val agent by flow.collectAsStateWithLifecycle(initial)
    return agent
}

@Composable
private fun AgentWatchNavigation(
    startPaired: Boolean,
    prefs: Prefs,
    deepLink: AgentKey?,
    onDeepLinkHandled: () -> Unit
) {
    val nav = rememberSwipeDismissableNavController()
    val authFlow = remember { RelayRepository.state.map { it.auth }.distinctUntilChanged() }
    val auth by authFlow.collectAsStateWithLifecycle(remember { RelayRepository.state.value.auth })

    // Checked at runtime, not only at start: a 401 anywhere (REVOKED) or an unpair (UNPAIRED) sends the
    // user to pairing, never to "Mac offline" or an empty list.
    LaunchedEffect(auth) {
        if (auth != AuthState.PAIRED && nav.currentDestination?.route != Routes.PAIRING) {
            nav.navigate(Routes.PAIRING) { popUpTo(nav.graph.id) { inclusive = true } }
        }
    }
    LaunchedEffect(deepLink, auth) {
        if (deepLink != null && auth == AuthState.PAIRED) {
            onDeepLinkHandled()
            nav.navigate(Routes.agent(deepLink)) { popUpTo(Routes.AGENTS) }
        }
    }

    // Values too long for a route argument, handed to the next screen.
    var fullText by remember { mutableStateOf("") }
    var dictatedText by remember { mutableStateOf("") }
    // A pane's history can hold items the global state lacks: the reader gets the one tapped.
    var readerItem by remember { mutableStateOf<HistoryItem?>(null) }
    val openReader: (HistoryItem) -> Unit = { item ->
        readerItem = item
        nav.navigate(Routes.reader(item.id))
    }

    val agentArguments = remember {
        listOf(
            navArgument("host") { type = NavType.StringType },
            navArgument("paneId") { type = NavType.StringType }
        )
    }

    SwipeDismissableNavHost(navController = nav, startDestination = if (startPaired) Routes.AGENTS else Routes.PAIRING) {
        composable(Routes.PAIRING) {
            PairingScreen(revoked = auth == AuthState.REVOKED, onPaired = { nav.showAgents() })
        }
        composable(Routes.AGENTS) {
            val state by RelayRepository.state.collectAsStateWithLifecycle()
            AgentListScreen(
                state = state,
                onAgentClick = { nav.navigate(Routes.agent(it)) },
                onHistoryClick = { nav.navigate(Routes.history(null)) },
                onSettingsClick = { nav.navigate(Routes.SETTINGS) }
            )
        }
        composable(Routes.AGENT, arguments = agentArguments) { entry ->
            val key = Routes.agentKey(entry.arguments) ?: AgentKey("", "")
            val found = rememberAgent(key)
            var pinned by remember { mutableStateOf(prefs.pinnedTarget) }
            if (found == null) {
                // No snapshot yet (cold start, reconnecting) is not the same as a closed agent.
                val stale by remember { RelayRepository.state.map { it.stale }.distinctUntilChanged() }
                    .collectAsStateWithLifecycle(remember { RelayRepository.state.value.stale })
                Message(stringResource(if (stale) R.string.notice_connecting else R.string.agent_closed))
            } else {
                val (agent, hostName) = found
                // The agent found, with its host, even when the route had none (an intent from before hosts).
                val agentKey = agent.key
                AgentDetailScreen(
                    agent = agent,
                    hostName = hostName,
                    lastReply = rememberPaneHistory(agentKey).firstOrNull(),
                    onReadReply = openReader,
                    isTileTarget = pinned == agentKey,
                    onTileTargetChange = { on ->
                        prefs.pinnedTarget = if (on) agentKey else null
                        pinned = prefs.pinnedTarget
                    },
                    onDictated = { text ->
                        dictatedText = text
                        nav.navigate(Routes.dictation(agentKey))
                    },
                    onHistoryClick = { nav.navigate(Routes.history(agentKey)) },
                    onViewAll = { text ->
                        fullText = text
                        nav.navigate(Routes.FULL_TEXT)
                    }
                )
            }
        }
        composable(Routes.DICTATION, arguments = agentArguments) { entry ->
            val found = rememberAgent(Routes.agentKey(entry.arguments) ?: AgentKey("", ""))
            DictationFlow(
                target = found?.first,
                hostName = found?.second,
                text = dictatedText,
                onTextChange = { dictatedText = it },
                onFinished = { nav.popBackStack() }
            )
        }
        composable(Routes.FULL_TEXT) { FullTextScreen(fullText) }
        composable(
            Routes.HISTORY,
            arguments = listOf(
                navArgument("host") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("paneId") { type = NavType.StringType; nullable = true; defaultValue = null }
            )
        ) { entry ->
            val key = Routes.agentKey(entry.arguments)
            val state by RelayRepository.state.collectAsStateWithLifecycle()
            HistoryListScreen(
                agent = key,
                agentLabel = key?.let { state.agents.findAgent(it)?.label },
                historyItems = if (key == null) state.history else rememberPaneHistory(key),
                onSelectHistoryItem = openReader
            )
        }
        composable(Routes.READER, arguments = listOf(navArgument("historyId") { type = NavType.StringType })) { entry ->
            val id = Uri.decode(entry.arguments?.getString("historyId").orEmpty())
            val state by RelayRepository.state.collectAsStateWithLifecycle()
            val item = state.history.find { it.id == id } ?: readerItem?.takeIf { it.id == id }
            when {
                item != null -> ResponseReaderScreen(item)
                state.stale && state.history.isEmpty() -> Message(stringResource(R.string.notice_connecting))
                else -> Message(stringResource(R.string.item_not_found))
            }
        }
        composable(Routes.SETTINGS) {
            SettingsScreen(onPairAgain = { nav.navigate(Routes.PAIRING) })
        }
    }
}

private fun NavHostController.showAgents() = navigate(Routes.AGENTS) { popUpTo(graph.id) { inclusive = true } }

@Composable
private fun Message(text: String) {
    Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(text, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
    }
}
