package com.gabriel.agentwatch.tile

import androidx.wear.protolayout.LayoutElementBuilders.HORIZONTAL_ALIGN_CENTER
import androidx.wear.protolayout.LayoutElementBuilders.LayoutElement
import androidx.wear.protolayout.layout.column
import androidx.wear.protolayout.material3.MaterialScope
import androidx.wear.protolayout.material3.Typography
import androidx.wear.protolayout.material3.primaryLayout
import androidx.wear.protolayout.material3.text
import androidx.wear.protolayout.material3.textEdgeButton
import androidx.wear.protolayout.modifiers.clickable
import androidx.wear.protolayout.types.LayoutColor
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
import com.gabriel.agentwatch.model.key
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlin.time.Duration.Companion.minutes

/**
 * Quick Dictate tile (ProtoLayout Material 3): the target agent with its status and the fetch time,
 * and one edge button. The button opens [QuickDictateActivity] with the agent shown here, so the
 * tile and the activity can never disagree on the target; a blocked target opens the app instead.
 * One `GET /v1/agents` per request, no heavy parsing.
 */
class AgentQuickActionTileService : Material3TileService(allowDynamicTheme = false, defaultColorScheme = TileColors) {

    override suspend fun MaterialScope.tileResponse(requestParams: TileRequest): Tile {
        val prefs = Prefs(this@AgentQuickActionTileService)
        val snapshot = fetchAgentsForTile(prefs)
        val content = tileContent(prefs.isPaired, snapshot, prefs.pinnedTarget)
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
                    content.needingYou.takeIf { it > 0 }?.let { getString(R.string.tile_needing_you, it) to TileAmber },
                    asOf() to colorScheme.onSurfaceVariant
                )
                is TileContent.Target -> {
                    val agent = content.agent
                    val (status, color) = tileStatus(agent.status)
                    lines(
                        agent.label.ifBlank { agent.pane_id } to colorScheme.onSurface,
                        // Several hosts: which machine, on a line of its own (ARCHITECTURE.md §4b).
                        content.hostName?.let { it to colorScheme.onSurfaceVariant },
                        (if (content.macOnline) status else getString(R.string.notice_device_offline)) to
                            (if (content.macOnline) color else TileAmber),
                        content.othersNeedingYou.takeIf { it > 0 }?.let { getString(R.string.tile_others_need_you, it) to TileAmber },
                        asOf() to colorScheme.onSurfaceVariant,
                        titleFirst = true
                    )
                }
            }
        },
        bottomSlot = {
            val target = content as? TileContent.Target
            if (target != null && target.agent.status != "blocked") {
                textEdgeButton(onClick = clickable(launchInApp(QuickDictateActivity::class.java.name, target.agent.key), id = "dictate")) {
                    text(getString(R.string.dictate).layoutString)
                }
            } else {
                // Nothing to dictate to, or the target is waiting on a prompt: the app handles it.
                textEdgeButton(onClick = clickable(launchInApp(MainActivity::class.java.name, target?.agent?.key), id = "open")) {
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

    private fun asOf(): String =
        getString(R.string.tile_as_of, LocalTime.now().format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)))

}
