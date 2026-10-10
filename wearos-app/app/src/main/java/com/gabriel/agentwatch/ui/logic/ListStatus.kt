package com.gabriel.agentwatch.ui.logic

import com.gabriel.agentwatch.network.Connection
import com.gabriel.agentwatch.network.UiState

/** The one line above the agent list that says why it may not be current. */
enum class ListNotice { CONNECTING, RELAY_UNREACHABLE, MAC_OFFLINE, HERDR_STOPPED }

/** @property dimmed the agents shown are the last known ones, not live. */
data class ListStatus(val notice: ListNotice?, val dimmed: Boolean)

/**
 * What the list says about its freshness. Call only while paired: `auth` decides pairing first.
 *
 * While [UiState.stale], the host flags are not current either (before the first snapshot they are
 * `false` by default), so the relay link is reported, never "Mac offline". [previous] is the notice
 * shown before this state: a retry after a failure (Offline → Connecting) keeps saying the relay is
 * unreachable instead of flickering back to "Connecting".
 */
fun listStatus(state: UiState, previous: ListNotice? = null): ListStatus =
    listStatus(state, state.hostOnline, state.herdrOnline, state.agents.isNotEmpty(), previous)

/**
 * [listStatus] for one page of the list: [hostOnline] and [herdrOnline] are that page's host's flags
 * and [hasAgents] whether the page shows any agent. The relay link (stale) is the same for every page.
 */
fun listStatus(
    state: UiState,
    hostOnline: Boolean,
    herdrOnline: Boolean,
    hasAgents: Boolean,
    previous: ListNotice? = null
): ListStatus {
    if (state.stale) {
        val notice = when {
            state.connection is Connection.Offline -> ListNotice.RELAY_UNREACHABLE
            previous == ListNotice.RELAY_UNREACHABLE -> ListNotice.RELAY_UNREACHABLE
            else -> ListNotice.CONNECTING
        }
        return ListStatus(notice, dimmed = hasAgents)
    }
    return when {
        !hostOnline -> ListStatus(ListNotice.MAC_OFFLINE, dimmed = hasAgents)
        !herdrOnline -> ListStatus(ListNotice.HERDR_STOPPED, dimmed = hasAgents)
        else -> ListStatus(null, dimmed = false)
    }
}
