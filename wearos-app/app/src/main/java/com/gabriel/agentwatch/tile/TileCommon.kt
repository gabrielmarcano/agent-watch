package com.gabriel.agentwatch.tile

import android.content.Context
import androidx.wear.protolayout.ActionBuilders
import androidx.wear.protolayout.material3.ColorScheme
import androidx.wear.protolayout.types.LayoutColor
import androidx.wear.protolayout.types.argb
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.network.RelayClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

// The app's fixed palette for the tiles (no dynamic colour: the status colours carry meaning).
internal val TileColors = ColorScheme(
    primary = 0xFF9ECAFF.argb,
    onPrimary = 0xFF00315B.argb,
    primaryContainer = 0xFF13304F.argb,
    onPrimaryContainer = 0xFFD3E4FF.argb,
    onSurface = 0xFFF1F3F5.argb,
    onSurfaceVariant = 0xFFC3C8CF.argb,
    surfaceContainer = 0xFF1F2227.argb,
    background = 0xFF000000.argb,
)
internal val TileAmber = 0xFFFFC857.argb
internal val TileAmberContainer = 0xFF4A3800.argb
internal val TileOnAmberContainer = 0xFFFFE8B0.argb
internal val TileGreen = 0xFF86DBA5.argb
internal val TileBlue = 0xFF9ECAFF.argb
internal val TileGrey = 0xFFC3C8CF.argb

/** The status word and its colour, as the app shows them. */
internal fun Context.tileStatus(status: String): Pair<String, LayoutColor> = when (status) {
    "blocked" -> getString(R.string.status_blocked) to TileAmber
    "done" -> getString(R.string.status_done) to TileGreen
    "working" -> getString(R.string.status_working) to TileBlue
    "idle" -> getString(R.string.status_idle) to TileGrey
    else -> getString(R.string.status_unknown) to 0xFFA5ACB4.argb
}

/** One `GET /v1/agents` for a tile, capped at 3 s; null when not paired. */
internal suspend fun fetchAgentsForTile(prefs: Prefs): Result<AgentsSnapshot>? {
    if (!prefs.isPaired) return null
    return withContext(Dispatchers.IO) {
        withTimeoutOrNull(3_000L) {
            try {
                RelayClient(prefs.relayUrl, prefs.deviceToken).agents()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Result.failure(e)
            }
        }
    }
}

/** Opens [className] in this app (whatever its applicationId), with the agent (pane and host) when there is one. */
internal fun Context.launchInApp(className: String, agent: AgentKey?): ActionBuilders.LaunchAction =
    ActionBuilders.LaunchAction.Builder()
        .setAndroidActivity(
            ActionBuilders.AndroidActivity.Builder()
                .setPackageName(packageName)
                .setClassName(className)
                .apply {
                    if (agent != null && agent.paneId.isNotBlank()) {
                        addKeyToExtraMapping(
                            QuickDictateActivity.EXTRA_PANE_ID,
                            ActionBuilders.AndroidStringExtra.Builder().setValue(agent.paneId).build()
                        )
                        addKeyToExtraMapping(
                            QuickDictateActivity.EXTRA_HOST,
                            ActionBuilders.AndroidStringExtra.Builder().setValue(agent.host).build()
                        )
                    }
                }
                .build()
        )
        .build()
