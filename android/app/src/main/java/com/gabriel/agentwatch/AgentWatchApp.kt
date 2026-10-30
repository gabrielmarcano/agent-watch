package com.gabriel.agentwatch

import android.app.Application
import android.content.Context
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.network.AgentsUpdate
import com.gabriel.agentwatch.network.ApprovalNotifications
import com.gabriel.agentwatch.network.RelayObservers
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.network.SurfaceUpdates
import com.gabriel.agentwatch.network.UiState

/**
 * Wires the Wear OS side effects onto the shared [RelayRepository]. Set in `onCreate` so every
 * process that touches the repository (the app, a tile, the complication, Quick Dictate) has the
 * observers installed before [RelayRepository.init] runs.
 *
 * The phone app (`:mobile`) installs none: it shows nothing outside its own screen.
 */
class AgentWatchApp : Application() {
    override fun onCreate() {
        super.onCreate()
        RelayRepository.observers = object : RelayObservers {
            // An agent that is no longer blocked must not keep an approval notification around.
            override fun onAgentsUpdated(context: Context, update: AgentsUpdate) =
                ApprovalNotifications.onAgentsUpdated(context, update)

            // Complication and tile: refresh when what they show changes (throttled).
            override fun onState(context: Context, state: UiState, pinned: AgentKey?) =
                SurfaceUpdates.onState(context, state, pinned)

            // Auth cleared or revoked (401): drop active notifications.
            override fun onAuthCleared(context: Context) =
                ApprovalNotifications.cancelAll(context)
        }
    }
}
