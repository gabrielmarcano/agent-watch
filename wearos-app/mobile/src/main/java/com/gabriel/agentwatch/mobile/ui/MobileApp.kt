package com.gabriel.agentwatch.mobile.ui

import androidx.activity.compose.PredictiveBackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.ExperimentalAnimationApi
import androidx.compose.animation.SharedTransitionLayout
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.Settings
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.mobile.BuildConfig
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.push.MobileNotifications
import com.gabriel.agentwatch.mobile.ui.logic.needsAnswer
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.AgentsUpdate
import com.gabriel.agentwatch.network.AuthState
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.logic.listStatus
import com.gabriel.agentwatch.update.UpdateManager

/**
 * The app's screens: a bottom navigation with the four places the phone goes (chats, machines,
 * history, settings) and the full-screen routes on top of them (a conversation, a new session, a
 * text). No navigation library: a route string plus the agent's host and pane, all saveable, is
 * enough.
 *
 * The pairing screen is not a route: it replaces everything while the phone holds no accepted
 * device token (the same rule the watch follows).
 */
private object Routes {
    const val CHATS = "chats"
    const val MACHINES = "machines"
    const val SETTINGS = "settings"
    const val CHAT = "chat"
    const val NEW_SESSION = "new-session"
    const val STARTING = "starting"
}

/** The routes the bottom navigation shows. */
private val TAB_ROUTES = listOf(Routes.CHATS, Routes.MACHINES, Routes.SETTINGS)

@OptIn(ExperimentalAnimationApi::class)
@Composable
fun MobileApp(
    deepLink: AgentKey? = null,
    onDeepLinkHandled: () -> Unit = {}
) {
    val state by RelayRepository.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
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
    var startingHost by rememberSaveable { mutableStateOf("") }
    var startingPane by rememberSaveable { mutableStateOf("") }
    var startingLabel by rememberSaveable { mutableStateOf("") }
    var machineFilter by rememberSaveable { mutableStateOf<String?>(null) }
    var sessionHost by rememberSaveable { mutableStateOf("") }
    val chatSeen = remember(context) { ChatSeen(context) }
    val chatStore = remember(context) { ChatStore(context) }
    val prefs = remember(context) { Prefs(context) }

    fun openChat(key: AgentKey) {
        // Opening a chat is reading its notification: drop it (and refresh the group summary).
        MobileNotifications.cancel(context, key)
        routeHost = key.host
        routePane = key.paneId
        route = Routes.CHAT
    }

    // The system's back gesture: the conversation, the text and the other tabs all go back to the
    // chats (the watch's rule: the list is the home). The gesture's progress drives the screen
    // back a little, as a predictive back should.
    var backProgress by remember { mutableFloatStateOf(0f) }
    PredictiveBackHandler(enabled = route != Routes.CHATS) { progress ->
        try {
            progress.collect { event -> backProgress = event.progress }
            // The gesture completed: go back. (A cancelled gesture throws and leaves us here.)
            route = Routes.CHATS
        } finally {
            backProgress = 0f
        }
    }

    // Check the pairing first, at runtime: anything but PAIRED shows the pairing screen, never an
    // empty list or "device offline" (wearos-app/ARCHITECTURE.md §4).
    if (state.auth != AuthState.PAIRED) {
        PairingScreen(revoked = state.auth == AuthState.REVOKED) {
            route = Routes.CHATS
        }
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

    Scaffold(
        bottomBar = {
            if (route in TAB_ROUTES) {
                PhoneNavigationBar(
                    current = route,
                    waiting = state.agents.count { it.needsAnswer() },
                    onSelect = { route = it }
                )
            }
        }
    ) { padding ->
        Box(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .graphicsLayer {
                    val back = backProgress.coerceIn(0f, 1f)
                    alpha = 1f - 0.25f * back
                    scaleX = 1f - 0.04f * back
                    scaleY = 1f - 0.04f * back
                }
        ) {
            SharedTransitionLayout {
                AnimatedContent(
                    targetState = route,
                    transitionSpec = {
                        if (targetState == Routes.CHAT) {
                            (slideInHorizontally { it } + fadeIn()) togetherWith fadeOut()
                        } else {
                            fadeIn() togetherWith fadeOut()
                        }
                    },
                    label = "route"
                ) { target ->
                    when (target) {
                        Routes.CHAT -> {
                            val agent = state.agents.findAgent(AgentKey(routeHost, routePane))
                            if (agent == null) {
                                // No snapshot yet (cold start, reconnecting) is not the same as a closed agent.
                                TextScreen(
                                    title = null,
                                    text = stringResource(
                                        if (state.stale) R.string.notice_connecting else R.string.agent_closed
                                    ),
                                    source = "transcript",
                                    query = null,
                                    onBack = { route = Routes.CHATS }
                                )
                            } else {
                                ChatScreen(
                                    agent = agent,
                                    hosts = state.hosts,
                                    chatStore = chatStore,
                                    notice = listStatus(state).notice,
                                    sharedScope = this@SharedTransitionLayout,
                                    visibilityScope = this,
                                    onBack = { route = Routes.CHATS },
                                    onMarkRead = {
                                        chatSeen.markRead(agent.key, agent.state_change_seq)
                                    }
                                )
                            }
                        }

                        Routes.MACHINES -> MachinesScreen(
                            state = state,
                            onOpenChats = { host ->
                                machineFilter = host
                                route = Routes.CHATS
                            },
                            onNewSession = { host ->
                                sessionHost = host
                                route = Routes.NEW_SESSION
                            }
                        )

                        Routes.SETTINGS -> SettingsScreen(
                            state = state,
                            chatStore = chatStore,
                            onCheckUpdates = { updateManager.check(manual = true) },
                            onForgetPhone = {
                                // Forgetting the phone: the token goes, the link restarts and the app
                                // asks for a new pairing code (the watch's rule).
                                prefs.clearAuth()
                                RelayRepository.restart(context)
                                route = Routes.CHATS
                            }
                        )

                        Routes.NEW_SESSION -> NewSessionScreen(
                            hosts = state.hosts,
                            startHost = sessionHost,
                            onBack = { route = Routes.CHATS },
                            onCreated = { created ->
                                startingHost = created.host
                                startingPane = created.pane_id
                                startingLabel = created.label
                                route = Routes.STARTING
                            }
                        )

                        Routes.STARTING -> {
                            // The bridge starts the agent before answering, so the next list usually has
                            // it; open its chat the moment it does, instead of leaving the owner on a spinner.
                            val created = state.agents.findAgent(AgentKey(startingHost, startingPane))
                            LaunchedEffect(created?.key) {
                                if (created != null) openChat(created.key)
                            }
                            StartingSessionScreen(label = startingLabel, onBack = { route = Routes.CHATS })
                        }

                        else -> ChatListScreen(
                            state = state,
                            update = update,
                            hostFilter = machineFilter,
                            onHostFilter = { machineFilter = it },
                            chatStore = chatStore,
                            sharedScope = this@SharedTransitionLayout,
                            visibilityScope = this,
                            onOpen = { openChat(it.key) },
                            onNewSession = {
                                sessionHost = ""
                                route = Routes.NEW_SESSION
                            },
                            onRefresh = { RelayRepository.refresh() },
                            onMarkRead = { agent ->
                                // The swipe marks the chat read without opening it: the turns up to now
                                // count as seen.
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
            }
        }
    }
}

/**
 * The count of chats waiting for the owner, on the Chats tab: a messaging app shows it on the
 * conversation list's tab.
 */
@Composable
private fun WaitingBadge(count: Int, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .defaultMinSize(minWidth = 16.dp, minHeight = 16.dp)
            .background(MaterialTheme.colorScheme.primary, CircleShape)
            .padding(horizontal = 4.dp),
        contentAlignment = Alignment.Center
    ) {
        Text(
            text = if (count > 99) "99+" else count.toString(),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onPrimary
        )
    }
}

/** The bottom navigation: the four places the phone goes (docs/phases/7-android-ux.md § 2). */
@Composable
private fun PhoneNavigationBar(current: String, waiting: Int, onSelect: (String) -> Unit) {
    NavigationBar {
        NavigationBarItem(
            selected = current == Routes.CHATS,
            onClick = { onSelect(Routes.CHATS) },
            icon = {
                Box {
                    Icon(Icons.AutoMirrored.Filled.Chat, contentDescription = null)
                    if (waiting > 0) {
                        WaitingBadge(waiting, Modifier.align(Alignment.TopEnd))
                    }
                }
            },
            label = { Text(stringResource(R.string.tab_chats)) }
        )
        NavigationBarItem(
            selected = current == Routes.MACHINES,
            onClick = { onSelect(Routes.MACHINES) },
            icon = { Icon(Icons.Filled.Computer, contentDescription = null) },
            label = { Text(stringResource(R.string.tab_machines)) }
        )
        NavigationBarItem(
            selected = current == Routes.SETTINGS,
            onClick = { onSelect(Routes.SETTINGS) },
            icon = { Icon(Icons.Filled.Settings, contentDescription = null) },
            label = { Text(stringResource(R.string.tab_settings)) }
        )
    }
}
