package com.gabriel.agentwatch.mobile.ui

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.findAgent
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.AuthState
import com.gabriel.agentwatch.network.RelayRepository
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
}

@Composable
fun MobileApp() {
    val state by RelayRepository.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    var route by rememberSaveable { mutableStateOf(Routes.CHATS) }
    var routeHost by rememberSaveable { mutableStateOf("") }
    var routePane by rememberSaveable { mutableStateOf("") }
    var textTitle by rememberSaveable { mutableStateOf("") }
    var textBody by rememberSaveable { mutableStateOf("") }
    var textSource by rememberSaveable { mutableStateOf("") }
    var textQuery by rememberSaveable { mutableStateOf("") }
    var backRoute by rememberSaveable { mutableStateOf(Routes.CHATS) }

    fun openText(title: String, body: String, source: String, query: String, from: String) {
        textTitle = title
        textBody = body
        textSource = source
        textQuery = query
        backRoute = from
        route = Routes.TEXT
    }

    fun openChat(key: AgentKey) {
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

        else -> ChatListScreen(
            state = state,
            onOpen = { openChat(it.key) },
            onHistory = { route = Routes.HISTORY },
            onRefresh = { scope.launch { RelayRepository.refresh() } }
        )
    }
}
