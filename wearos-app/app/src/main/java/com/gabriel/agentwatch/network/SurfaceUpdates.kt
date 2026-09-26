package com.gabriel.agentwatch.network

import android.content.ComponentName
import android.content.Context
import android.os.SystemClock
import android.util.Log
import androidx.wear.tiles.TileService
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceUpdateRequester
import com.gabriel.agentwatch.complication.AgentStatusComplicationService
import com.gabriel.agentwatch.model.resolveTargetAgent
import com.gabriel.agentwatch.tile.AgentQuickActionTileService
import com.gabriel.agentwatch.tile.AgentsTileService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * What the complication (host offline / blocked / working / done counts) and the tiles (dictation target
 * label, the most urgent agents) render, reduced to the fields that change their output. Equal
 * signatures need no refresh.
 */
data class SurfaceSignature(
    val paired: Boolean,
    val hostOnline: Boolean,
    val blocked: Int,
    val working: Int,
    val agents: Int,
    val targetLabel: String?,
    val done: Int = 0
)

fun surfaceSignature(state: UiState, pinnedPaneId: String?): SurfaceSignature = SurfaceSignature(
    paired = state.auth == AuthState.PAIRED,
    hostOnline = state.hostOnline,
    blocked = state.agents.count { it.status == "blocked" },
    working = state.agents.count { it.status == "working" },
    agents = state.agents.size,
    targetLabel = resolveTargetAgent(state.agents, pinnedPaneId)?.label,
    done = state.agents.count { it.status == "done" }
)

/**
 * At most one update request per [minIntervalMs]; changes inside the window coalesce into a single
 * trailing request at its end. Not thread-safe: the owner serialises calls.
 */
class UpdateThrottle(private val minIntervalMs: Long) {
    private var lastRequestAt: Long? = null
    private var trailingPending = false

    /** Something changed at [now]: request after the returned delay (0 = now), or null if one is already scheduled. */
    fun onChange(now: Long): Long? {
        if (trailingPending) return null
        val last = lastRequestAt
        val wait = if (last == null) 0L else last + minIntervalMs - now
        return if (wait <= 0L) {
            lastRequestAt = now
            0L
        } else {
            trailingPending = true
            wait
        }
    }

    /** The trailing request scheduled by [onChange] was sent at [now]. */
    fun onTrailingFired(now: Long) {
        trailingPending = false
        lastRequestAt = now
    }
}

/**
 * Asks the watch face complication and the tile to re-fetch when agent state changes, instead of
 * waiting for their 15-minute period. Fed by the repository's state and by pushes.
 */
object SurfaceUpdates {
    private const val TAG = "SurfaceUpdates"
    private const val MIN_INTERVAL_MS = 10_000L

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val throttle = UpdateThrottle(MIN_INTERVAL_MS)
    private var lastSignature: SurfaceSignature? = null

    /** The repository state changed: request an update only if what the surfaces show changed. */
    fun onState(context: Context, state: UiState, pinnedPaneId: String?) {
        val signature = surfaceSignature(state, pinnedPaneId)
        synchronized(this) {
            if (signature == lastSignature) return
            lastSignature = signature
        }
        request(context)
    }

    /** Something the surfaces show probably changed (a push arrived): request an update, throttled. */
    fun request(context: Context) {
        val appContext = context.applicationContext
        val wait = synchronized(this) { throttle.onChange(SystemClock.elapsedRealtime()) } ?: return
        if (wait == 0L) {
            send(appContext)
        } else {
            scope.launch {
                delay(wait)
                synchronized(this@SurfaceUpdates) { throttle.onTrailingFired(SystemClock.elapsedRealtime()) }
                send(appContext)
            }
        }
    }

    private fun send(context: Context) {
        try {
            ComplicationDataSourceUpdateRequester
                .create(context, ComponentName(context, AgentStatusComplicationService::class.java))
                .requestUpdateAll()
        } catch (e: RuntimeException) {
            Log.w(TAG, "Complication update request failed: ${e.javaClass.simpleName}")
        }
        try {
            TileService.getUpdater(context).requestUpdate(AgentQuickActionTileService::class.java)
            TileService.getUpdater(context).requestUpdate(AgentsTileService::class.java)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Tile update request failed: ${e.javaClass.simpleName}")
        }
    }
}
