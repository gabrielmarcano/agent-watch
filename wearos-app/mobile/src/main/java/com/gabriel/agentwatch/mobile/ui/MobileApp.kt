package com.gabriel.agentwatch.mobile.ui

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.gabriel.agentwatch.mobile.BuildConfig
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.push.MobileNotifications
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.AgentsUpdate
import com.gabriel.agentwatch.network.AuthState
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.update.UpdateManager
import kotlinx.coroutines.launch

/**
 * The app's screens. No navigation library: a route string plus the agent's host and pane, all
 * saveable, is enough for four screens. The pairing screen is not a route: it replaces everything
 * while the phone holds no accepted device token (the same rule the watch follows).
 */
private object Routes {
    const val CHATS = "chats"
    const val CHAT = "chat"
    const val HISTORY = "history"
    const val TEXT = "text"
    const val NEW_SESSION = "new-session"
    const val STARTING = "starting"
}

@Composable
fun MobileApp(
    deepLink: AgentKey? = null,
    onDeepLinkHandled: () -> Unit = {}
) {
    val state by RelayRepository.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    // The reading habit (what each chat was last read at) is shared by the list and the chat screen.
    val chatSeen = remember(context) { ChatSeen(context) }
    val updateManager = remember {
        UpdateManager(context, app = "phone", currentVersionCode = BuildConfig.VERSION_CODE.toLong())
    }
    val update by updateManager.state.collectAsStateWithLifecycle()
    DisposableEffect(updateManager) {
        onDispose { updateManager.close() }
    }
    // The one-time "allow this source" screen is a different activity: refresh when we come back.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { updateManager.refreshInstallPermission() }

    var route by rememberSaveable { mutableStateOf(Routes.CHATS) }
    var routeHost by rememberSaveable { mutableStateOf("") }
    var routePane by rememberSaveable { mutableStateOf("") }
    var textTitle by rememberSaveable { mutableStateOf("") }
    var textBody by rememberSaveable { mutableStateOf("") }
    var textSource by rememberSaveable { mutableStateOf("") }
    var textQuery by rememberSaveable { mutableStateOf("") }
    var backRoute by rememberSaveable { mutableStateOf(Routes.CHATS) }
    var startingHost by rememberSaveable { mutableStateOf("") }
    var startingPane by rememberSaveable { mutableStateOf("") }
    var startingLabel by rememberSaveable { mutableStateOf("") }

    fun openText(title: String, body: String, source: String, query: String, from: String) {
        textTitle = title
        textBody = body
        textSource = source
        textQuery = query
        backRoute = from
        route = Routes.TEXT
    }

    fun openChat(key: AgentKey) {
        // Opening a chat is reading its notification: drop it (and refresh the group summary).
        MobileNotifications.cancel(context, key)
        routeHost = key.host
        routePane = key.paneId
        route = Routes.CHAT
    }

    BackHandler(enabled = route != Routes.CHATS) {
        route = if (route == Routes.TEXT) backRoute else Routes.CHATS
    }

    // Check the pairing first, at runtime: anything but PAIRED shows the pairing screen, never an
    // empty list or "device offline" (wearos-app/ARCHITECTURE.md §4).
    if (state.auth != AuthState.PAIRED) {
        PairingScreen(revoked = state.auth == AuthState.REVOKED) { route = Routes.CHATS }
        return
    }

    // One relay call on start (and after pairing): the metadata is small, and an update is worth
    // showing at once. An offline start fails silently; the card appears only when a build exists.
    LaunchedEffect(state.auth) {
        updateManager.check()
    }

    // A notification tap names the chat; it waits here until the device is paired, so a cold start
    // from a notification lands on the right conversation.
    LaunchedEffect(deepLink) {
        if (deepLink != null) {
            onDeepLinkHandled()
            openChat(deepLink)
        }
    }
    // The relay may report an answered prompt while the app is open: drop its stale approval.
    LaunchedEffect(state.agents) {
        MobileNotifications.dismissStale(context, AgentsUpdate.All(state.agents))
    }

    when (route) {
        Routes.CHAT -> {
            val agent = state.agents.findAgent(AgentKey(routeHost, routePane))
            if (agent == null) {
                // No snapshot yet (cold start, reconnecting) is not the same as a closed agent.
                TextScreen(
                    title = null,
                    text = stringResource(if (state.stale) R.string.notice_connecting else R.string.agent_closed),
                    source = "transcript",
                    query = null,
                    onBack = { route = Routes.CHATS }
                )
            } else {
                ChatScreen(
                    agent = agent,
                    hosts = state.hosts,
                    onBack = { route = Routes.CHATS }
                )
            }
        }

        Routes.HISTORY -> HistoryScreen(
            history = state.history,
            onBack = { route = Routes.CHATS },
            onOpen = { item ->
                openText(
                    item.label.ifBlank { item.agent },
                    item.response,
                    item.source,
                    item.query.orEmpty(),
                    Routes.HISTORY
                )
            }
        )

        Routes.TEXT -> TextScreen(
            title = textTitle.ifBlank { null },
            text = textBody,
            source = textSource,
            query = textQuery.ifBlank { null },
            onBack = { route = backRoute }
        )

        Routes.NEW_SESSION -> NewSessionScreen(
            hosts = state.hosts,
            onBack = { route = Routes.CHATS },
            onCreated = { created ->
                startingHost = created.host
                startingPane = created.pane_id
                startingLabel = created.label
                route = Routes.STARTING
            }
        )

        Routes.STARTING -> {
            // The bridge starts the agent before answering, so the next list usually has it; open
            // its chat the moment it does, instead of leaving the owner on a spinner.
            val created = state.agents.findAgent(AgentKey(startingHost, startingPane))
            LaunchedEffect(created?.key) {
                if (created != null) openChat(created.key)
            }
            StartingSessionScreen(label = startingLabel, onBack = { route = Routes.CHATS })
        }

        else -> ChatListScreen(
            state = state,
            update = update,
            onOpen = { openChat(it.key) },
            onHistory = { route = Routes.HISTORY },
            onNewSession = { route = Routes.NEW_SESSION },
            onRefresh = { RelayRepository.refresh() },
            onMarkRead = { agent ->
                // The swipe marks the chat read without opening it: the turns up to now count as seen.
                chatSeen.markRead(agent.key, agent.state_change_seq)
            },
            onUpdate = { updateManager.download() },
            onRetryUpdate = {
                if (update.info != null) updateManager.download() else updateManager.check(manual = true)
            },
            onCancelUpdate = { updateManager.cancelDownload() },
            onInstallUpdate = { updateManager.install() },
            onDismissUpdate = { updateManager.dismiss() }
        )
    }
}
