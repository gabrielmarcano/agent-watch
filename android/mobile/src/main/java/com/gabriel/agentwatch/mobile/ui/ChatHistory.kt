package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map

private const val PANE_PAGE = 50

/**
 * One agent's finished turns (its host and pane), newest first: observed reactively from
 * [RelayRepository.state] ([UiState.historyFor]), which merges the global history and the cached
 * per-pane history. On [Lifecycle.State.STARTED], refreshes per-pane history via
 * [RelayRepository.loadPaneHistory].
 */
@Composable
fun rememberPaneHistory(agent: AgentKey): List<HistoryItem> {
    val items by remember(agent) {
        RelayRepository.state.map { it.historyFor(agent) }.distinctUntilChanged()
    }.collectAsStateWithLifecycle(emptyList())

    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(agent, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            RelayRepository.loadPaneHistory(agent, PANE_PAGE)
        }
    }
    return items
}
