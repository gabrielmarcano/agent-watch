package com.gabriel.agentwatch.ui.components

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
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map

private const val PANE_PAGE = 20

/**
 * One pane's finished turns, newest first: the live state's items (SSE and the global page, which
 * holds only the newest 20 of all panes) merged with `GET /v1/history?pane_id=`, fetched each time the
 * screen starts (opened, or returned to from another screen or from the background), so a turn whose
 * SSE event was missed still shows.
 */
@Composable
fun rememberPaneHistory(paneId: String): List<HistoryItem> {
    val live by remember(paneId) {
        RelayRepository.state.map { s -> s.history.filter { it.pane_id == paneId } }.distinctUntilChanged()
    }.collectAsStateWithLifecycle(emptyList())
    var fetched by remember(paneId) { mutableStateOf<List<HistoryItem>>(emptyList()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(paneId, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            RelayRepository.getClient()?.history(paneId, PANE_PAGE)?.onSuccess { fetched = it }
        }
    }
    return remember(live, fetched) { mergeHistory(live, fetched) }
}
