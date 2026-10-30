package com.gabriel.agentwatch.mobile.ui

import androidx.activity.compose.PredictiveBackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.ExperimentalAnimationApi
import androidx.compose.animation.SharedTransitionLayout
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.mobile.BuildConfig
import com.gabriel.agentwatch.mobile.MobileApplication
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
import com.gabriel.agentwatch.update.UpdatePhase

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
    val chatState = remember(context) { MobileApplication.chatState(context) }
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
    // empty list or "device offline" (android/ARCHITECTURE.md §4).
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

    // WhatsApp's dynamic shrinking floating bar: scrolling down the list compacts the bottom bar
    // (hiding labels), scrolling up expands it again.
    var barCompact by remember { mutableStateOf(false) }
    LaunchedEffect(route) { barCompact = false }
    val barScroll = remember {
        object : NestedScrollConnection {
            override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
                if (source == NestedScrollSource.UserInput) {
                    if (available.y < -12f) barCompact = true
                    else if (available.y > 12f) barCompact = false
                }
                return Offset.Zero
            }
        }
    }

    val hasUpdate = update.phase == UpdatePhase.AVAILABLE ||
        update.phase == UpdatePhase.READY ||
        update.phase == UpdatePhase.NEEDS_PERMISSION

    Scaffold(
        // The screens below handle their own insets (the chat list's title, the chat's bar); adding
        // them here as well would push everything down twice.
        contentWindowInsets = WindowInsets(0),
        bottomBar = {
            if (route in TAB_ROUTES) {
                PhoneNavigationBar(
                    current = route,
                    waiting = state.agents.count { it.needsAnswer() },
                    hasUpdate = hasUpdate,
                    compact = barCompact,
                    onSelect = { route = it }
                )
            }
        }
    ) { padding ->
        Box(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .nestedScroll(barScroll)
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
                                    seen = chatSeen,
                                    chatState = chatState,
                                    notice = listStatus(state).notice,
                                    sharedScope = this@SharedTransitionLayout,
                                    visibilityScope = this,
                                    onBack = { route = Routes.CHATS },
                                    onMarkRead = {
                                        chatState.markRead(agent.key, agent.state_change_seq)
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
                            update = update,
                            chatStore = chatStore,
                            chatState = chatState,
                            onUpdate = { updateManager.download() },
                            onRetryUpdate = {
                                if (update.info != null) updateManager.download() else updateManager.check(manual = true)
                            },
                            onCancelUpdate = { updateManager.cancelDownload() },
                            onInstallUpdate = { updateManager.install() },
                            onDismissUpdate = { updateManager.dismiss() },
                            onCheckUpdates = { updateManager.check(manual = true) },
                            onForgetPhone = {
                                // Forgetting the phone: the token goes, the link restarts and the app
                                // asks for a new pairing code (the watch's rule).
                                RelayRepository.clearPairing(context)
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
                            hostFilter = machineFilter,
                            onHostFilter = { machineFilter = it },
                            chatStore = chatStore,
                            chatState = chatState,
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
                                chatState.markRead(agent.key, agent.state_change_seq)
                            },
                            onMarkUnread = { agent ->
                                chatState.markUnread(agent.key)
                            }
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

/** The bottom navigation: the three places the phone goes (docs/phases/7-android-ux.md § 2). */
@Composable
private fun PhoneNavigationBar(
    current: String,
    waiting: Int,
    hasUpdate: Boolean,
    compact: Boolean,
    onSelect: (String) -> Unit
) {
    // A floating, rounded bar with the selected tab on a soft tinted horizontal pill: matches
    // WhatsApp Android 2.26's floating bottom bar, shrinking into a compact pill on scroll down.
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(horizontal = if (compact) 36.dp else 12.dp, vertical = 8.dp),
        shape = RoundedCornerShape(32.dp),
        color = MaterialTheme.colorScheme.surfaceContainer.copy(alpha = 0.96f),
        tonalElevation = 3.dp,
        shadowElevation = 10.dp,
        border = androidx.compose.foundation.BorderStroke(
            1.dp,
            MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f)
        )
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            NavItem(
                selected = current == Routes.CHATS,
                icon = Icons.AutoMirrored.Filled.Chat,
                label = stringResource(R.string.tab_chats),
                badge = waiting,
                dot = false,
                compact = compact,
                onClick = { onSelect(Routes.CHATS) }
            )
            NavItem(
                selected = current == Routes.MACHINES,
                icon = Icons.Filled.Computer,
                label = stringResource(R.string.tab_machines),
                badge = 0,
                dot = false,
                compact = compact,
                onClick = { onSelect(Routes.MACHINES) }
            )
            NavItem(
                selected = current == Routes.SETTINGS,
                icon = Icons.Filled.Settings,
                label = stringResource(R.string.tab_settings),
                badge = 0,
                dot = hasUpdate,
                compact = compact,
                onClick = { onSelect(Routes.SETTINGS) }
            )
        }
    }
}

/** One tab of the floating bar: the icon on a soft horizontal pill when selected, and its bold name. */
@Composable
private fun RowScope.NavItem(
    selected: Boolean,
    icon: ImageVector,
    label: String,
    badge: Int,
    dot: Boolean,
    compact: Boolean,
    onClick: () -> Unit
) {
    val hapticView = rememberHapticView()
    val interaction = remember { MutableInteractionSource() }
    Column(
        modifier = Modifier
            .weight(1f)
            .pressScale(interaction, pressedScale = 0.92f)
            .clickable(
                interactionSource = interaction,
                indication = null,
                onClick = {
                    if (!selected) MobileHaptics.tick(hapticView)
                    onClick()
                }
            ),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Box(
            modifier = Modifier.width(56.dp).height(30.dp),
            contentAlignment = Alignment.Center
        ) {
            if (selected) {
                // WhatsApp 2.26's soft horizontal tinted pill behind the active tab icon.
                Box(
                    Modifier
                        .fillMaxSize()
                        .background(MaterialTheme.colorScheme.primaryContainer, RoundedCornerShape(50))
                )
            }
            Icon(
                icon,
                contentDescription = label,
                tint = if (selected) {
                    MaterialTheme.colorScheme.onPrimaryContainer
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
                modifier = Modifier.size(21.dp)
            )
            if (badge > 0) {
                WaitingBadge(badge, Modifier.align(Alignment.TopEnd))
            } else if (dot) {
                Box(
                    Modifier
                        .align(Alignment.TopEnd)
                        .size(10.dp)
                        .background(MaterialTheme.colorScheme.primary, CircleShape)
                )
            }
        }
        AnimatedVisibility(visible = !compact) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelSmall.copy(
                    fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium
                ),
                color = if (selected) {
                    MaterialTheme.colorScheme.onSurface
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
                maxLines = 1,
                modifier = Modifier.padding(top = 2.dp)
            )
        }
    }
}
