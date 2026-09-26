package com.gabriel.agentwatch.tile

import androidx.wear.protolayout.ActionBuilders
import androidx.wear.protolayout.LayoutElementBuilders.HORIZONTAL_ALIGN_CENTER
import androidx.wear.protolayout.LayoutElementBuilders.LayoutElement
import androidx.wear.protolayout.layout.column
import androidx.wear.protolayout.material3.ColorScheme
import androidx.wear.protolayout.material3.MaterialScope
import androidx.wear.protolayout.material3.Typography
import androidx.wear.protolayout.material3.primaryLayout
import androidx.wear.protolayout.material3.text
import androidx.wear.protolayout.material3.textEdgeButton
import androidx.wear.protolayout.modifiers.clickable
import androidx.wear.protolayout.types.LayoutColor
import androidx.wear.protolayout.types.argb
import androidx.wear.protolayout.types.layoutString
import androidx.wear.tiles.Material3TileService
import androidx.wear.tiles.RequestBuilders.TileRequest
import androidx.wear.tiles.TileBuilders.Tile
import androidx.wear.tiles.tile
import androidx.wear.tiles.timeline
import androidx.wear.tiles.timelineEntry
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.gabriel.agentwatch.network.RelayClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlin.time.Duration.Companion.minutes

// The app's fixed palette (no dynamic colour: the status colours carry meaning).
private val TileColors = ColorScheme(
    primary = 0xFF9ECAFF.argb,
    onPrimary = 0xFF00315B.argb,
    primaryContainer = 0xFF13304F.argb,
    onPrimaryContainer = 0xFFD3E4FF.argb,
    onSurface = 0xFFF1F3F5.argb,
    onSurfaceVariant = 0xFFC3C8CF.argb,
    surfaceContainer = 0xFF1F2227.argb,
    background = 0xFF000000.argb,
)
private val Amber = 0xFFFFC857.argb
private val Green = 0xFF86DBA5.argb
private val Blue = 0xFF9ECAFF.argb

/**
 * Quick Dictate tile (ProtoLayout Material 3): the target agent with its status and the fetch time,
 * and one edge button. The button opens [QuickDictateActivity] with the `pane_id` shown here, so the
 * tile and the activity can never disagree on the target; a blocked target opens the app instead.
 * One `GET /v1/agents` per request, no heavy parsing.
 */
class AgentQuickActionTileService : Material3TileService(allowDynamicTheme = false, defaultColorScheme = TileColors) {

    override suspend fun MaterialScope.tileResponse(requestParams: TileRequest): Tile {
        val prefs = Prefs(this@AgentQuickActionTileService)
        val snapshot: Result<AgentsSnapshot>? = if (prefs.isPaired) {
            withContext(Dispatchers.IO) {
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
        } else {
            null
        }
        val content = tileContent(prefs.isPaired, snapshot, prefs.pinnedPaneId)
        return tile(timeline(timelineEntry(layout(content))), freshness = 15.minutes)
    }

    private fun MaterialScope.layout(content: TileContent): LayoutElement = primaryLayout(
        titleSlot = { text(getString(R.string.tile_title).layoutString) },
        mainSlot = {
            when (content) {
                TileContent.NotPaired -> message(getString(R.string.dictation_not_paired))
                TileContent.Unreachable -> message(getString(R.string.notice_relay_unreachable))
                is TileContent.NoTarget -> lines(
                    getString(R.string.tile_no_target) to colorScheme.onSurface,
                    content.needingYou.takeIf { it > 0 }?.let { getString(R.string.tile_needing_you, it) to Amber },
                    asOf() to colorScheme.onSurfaceVariant
                )
                is TileContent.Target -> {
                    val agent = content.agent
                    val (status, color) = statusWord(agent.status)
                    lines(
                        agent.label.ifBlank { agent.pane_id } to colorScheme.onSurface,
                        (if (content.macOnline) status else getString(R.string.notice_device_offline)) to
                            (if (content.macOnline) color else Amber),
                        content.othersNeedingYou.takeIf { it > 0 }?.let { getString(R.string.tile_others_need_you, it) to Amber },
                        asOf() to colorScheme.onSurfaceVariant,
                        titleFirst = true
                    )
                }
            }
        },
        bottomSlot = {
            val target = content as? TileContent.Target
            if (target != null && target.agent.status != "blocked") {
                textEdgeButton(onClick = clickable(launch(QuickDictateActivity::class.java.name, target.agent.pane_id), id = "dictate")) {
                    text(getString(R.string.dictate).layoutString)
                }
            } else {
                // Nothing to dictate to, or the target is waiting on a prompt: the app handles it.
                textEdgeButton(onClick = clickable(launch(MainActivity::class.java.name, target?.agent?.pane_id), id = "open")) {
                    text(getString(R.string.tile_open).layoutString)
                }
            }
        }
    )

    private fun MaterialScope.message(text: String): LayoutElement =
        text(text.layoutString, typography = Typography.BODY_MEDIUM, maxLines = 3)

    private fun MaterialScope.lines(vararg lines: Pair<String, LayoutColor>?, titleFirst: Boolean = false): LayoutElement =
        column(
            *lines.filterNotNull().mapIndexed { i, (value, color) ->
                text(
                    value.layoutString,
                    typography = if (titleFirst && i == 0) Typography.TITLE_MEDIUM else Typography.BODY_SMALL,
                    color = color,
                    maxLines = if (titleFirst && i == 0) 2 else 1
                )
            }.toTypedArray(),
            horizontalAlignment = HORIZONTAL_ALIGN_CENTER
        )

    private fun statusWord(status: String): Pair<String, LayoutColor> = when (status) {
        "blocked" -> getString(R.string.status_blocked) to Amber
        "done" -> getString(R.string.status_done) to Green
        "working" -> getString(R.string.status_working) to Blue
        "idle" -> getString(R.string.status_idle) to 0xFFC3C8CF.argb
        else -> getString(R.string.status_unknown) to 0xFFA5ACB4.argb
    }

    private fun asOf(): String =
        getString(R.string.tile_as_of, LocalTime.now().format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)))

    /** Opens [className] in this app (whatever its applicationId), with the pane when there is one. */
    private fun launch(className: String, paneId: String?): ActionBuilders.LaunchAction =
        ActionBuilders.LaunchAction.Builder()
            .setAndroidActivity(
                ActionBuilders.AndroidActivity.Builder()
                    .setPackageName(packageName)
                    .setClassName(className)
                    .apply {
                        if (!paneId.isNullOrBlank()) {
                            addKeyToExtraMapping(
                                QuickDictateActivity.EXTRA_PANE_ID,
                                ActionBuilders.AndroidStringExtra.Builder().setValue(paneId).build()
                            )
                        }
                    }
                    .build()
            )
            .build()
}
