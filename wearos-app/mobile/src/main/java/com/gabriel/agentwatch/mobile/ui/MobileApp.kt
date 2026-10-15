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
import com.gabriel.agentwatch.model.hostNameFor
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
    const val AGENTS = "agents"
    const val AGENT = "agent"
    const val HISTORY = "history"
    const val TEXT = "text"
}

@Composable
fun MobileApp() {
    val state by RelayRepository.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    var route by rememberSaveable { mutableStateOf(Routes.AGENTS) }
    var routeHost by rememberSaveable { mutableStateOf("") }
    var routePane by rememberSaveable { mutableStateOf("") }
    var textTitle by rememberSaveable { mutableStateOf("") }
    var textBody by rememberSaveable { mutableStateOf("") }
    var textSource by rememberSaveable { mutableStateOf("") }
    var backRoute by rememberSaveable { mutableStateOf(Routes.AGENTS) }

    fun openText(title: String, body: String, source: String, from: String) {
        textTitle = title
        textBody = body
        textSource = source
        backRoute = from
        route = Routes.TEXT
    }

    fun openAgent(key: AgentKey) {
        routeHost = key.host
        routePane = key.paneId
        route = Routes.AGENT
    }

    BackHandler(enabled = route != Routes.AGENTS) {
        route = if (route == Routes.TEXT) backRoute else Routes.AGENTS
    }

    // Check the pairing first, at runtime: anything but PAIRED shows the pairing screen, never an
    // empty list or "device offline" (wearos-app/ARCHITECTURE.md §4).
    if (state.auth != AuthState.PAIRED) {
        PairingScreen(revoked = state.auth == AuthState.REVOKED) { route = Routes.AGENTS }
        return
    }

    when (route) {
        Routes.AGENT -> {
            val key = AgentKey(routeHost, routePane)
            val agent = state.agents.findAgent(key)
            if (agent == null) {
                // No snapshot yet (cold start, reconnecting) is not the same as a closed agent.
                TextScreen(
                    title = null,
                    body = stringResource(if (state.stale) R.string.notice_connecting else R.string.agent_closed),
                    monospace = false,
                    onBack = { route = Routes.AGENTS }
                )
            } else {
                val agentKey = agent.key
                AgentDetailScreen(
                    agent = agent,
                    hostName = hostNameFor(state.hosts, agent.host),
                    lastReply = state.history.firstOrNull { it.key == agentKey },
                    onBack = { route = Routes.AGENTS },
                    onReadAll = { title, body, source -> openText(title, body, source, Routes.AGENT) },
                    onHistory = { route = Routes.HISTORY }
                )
            }
        }

        Routes.HISTORY -> HistoryScreen(
            history = state.history,
            onBack = { route = Routes.AGENTS },
            onOpen = { item ->
                openText(
                    item.label.ifBlank { item.agent },
                    item.response,
                    item.source,
                    Routes.HISTORY
                )
            }
        )

        Routes.TEXT -> TextScreen(
            title = textTitle.ifBlank { null },
            body = textBody,
            monospace = textSource == "screen",
            onBack = { route = backRoute }
        )

        else -> AgentListScreen(
            state = state,
            onOpen = { openAgent(it.key) },
            onHistory = { route = Routes.HISTORY },
            onRefresh = { scope.launch { RelayRepository.refresh() } }
        )
    }
}
