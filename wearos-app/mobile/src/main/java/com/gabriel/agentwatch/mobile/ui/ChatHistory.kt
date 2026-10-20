package com.gabriel.agentwatch.mobile.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.gabriel.agentwatch.data.mergeHistory
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.key
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map

private const val PANE_PAGE = 50

/**
 * One agent's finished turns (its host and pane), newest first: the live state's items (SSE and the
 * global page) merged with `GET /v1/history?host=&pane_id=`, fetched each time the chat starts
 * (opened, or returned to from another screen or the background), so a turn whose SSE event was
 * missed still shows. The watch's `rememberPaneHistory` with the phone's larger page.
 */
@Composable
fun rememberPaneHistory(agent: AgentKey): List<HistoryItem> {
    val live by remember(agent) {
        RelayRepository.state.map { s -> s.history.filter { it.key == agent } }.distinctUntilChanged()
    }.collectAsStateWithLifecycle(emptyList())
    var fetched by remember(agent) { mutableStateOf<List<HistoryItem>>(emptyList()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(agent, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            RelayRepository.getClient()?.history(agent, PANE_PAGE)?.onSuccess { fetched = it }
        }
    }
    return remember(live, fetched) { mergeHistory(live, fetched) }
}
