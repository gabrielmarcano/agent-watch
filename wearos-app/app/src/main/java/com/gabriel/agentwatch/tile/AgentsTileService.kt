package com.gabriel.agentwatch.tile

import androidx.wear.protolayout.DimensionBuilders.dp
import androidx.wear.protolayout.DimensionBuilders.expand
import androidx.wear.protolayout.LayoutElementBuilders.HORIZONTAL_ALIGN_CENTER
import androidx.wear.protolayout.LayoutElementBuilders.LayoutElement
import androidx.wear.protolayout.layout.column
import androidx.wear.protolayout.layout.spacer
import androidx.wear.protolayout.material3.ButtonColors
import androidx.wear.protolayout.material3.ButtonStyle
import androidx.wear.protolayout.material3.MaterialScope
import androidx.wear.protolayout.material3.Typography
import androidx.wear.protolayout.material3.button
import androidx.wear.protolayout.material3.primaryLayout
import androidx.wear.protolayout.material3.text
import androidx.wear.protolayout.modifiers.clickable
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
import com.gabriel.agentwatch.model.AgentState
import kotlin.time.Duration.Companion.minutes

/**
 * Agents tile (ProtoLayout Material 3): the agents that need the user most (blocked, then the latest
 * to finish), each a button that opens its screen (its prompt or its last reply); the title counts
 * the rest and a tap elsewhere opens the full list. One `GET /v1/agents` per request, no heavy parsing.
 */
class AgentsTileService : Material3TileService(allowDynamicTheme = false, defaultColorScheme = TileColors) {

    override suspend fun MaterialScope.tileResponse(requestParams: TileRequest): Tile {
        val prefs = Prefs(this@AgentsTileService)
        val content = agentsTile(prefs.isPaired, fetchAgentsForTile(prefs), max = MAX_AGENTS)
        return tile(timeline(timelineEntry(layout(content))), freshness = 15.minutes)
    }

    private fun MaterialScope.layout(content: AgentsTile): LayoutElement {
        val more = (content as? AgentsTile.Agents)?.more ?: 0
        val title = if (more > 0) getString(R.string.tile_agents_title_more, more) else getString(R.string.tile_agents_title)
        return primaryLayout(
            titleSlot = { text(title.layoutString) },
            // No edge button: two agents need the height. A tap outside them opens the full list.
            onClick = clickable(launchInApp(MainActivity::class.java.name, null), id = "open"),
            mainSlot = {
                when (content) {
                    AgentsTile.NotPaired -> message(getString(R.string.dictation_not_paired))
                    AgentsTile.Unreachable -> message(getString(R.string.notice_relay_unreachable))
                    AgentsTile.DeviceOffline -> message(getString(R.string.notice_device_offline))
                    is AgentsTile.Agents ->
                        if (content.shown.isEmpty()) {
                            message(getString(R.string.no_agents))
                        } else {
                            column(
                                *content.shown.flatMapIndexed { i, agent ->
                                    listOfNotNull(if (i > 0) spacer(height = dp(4f)) else null, agentButton(agent))
                                }.toTypedArray(),
                                width = expand(),
                                horizontalAlignment = HORIZONTAL_ALIGN_CENTER
                            )
                        }
                }
            }
        )
    }

    /** One agent: its name, then its status and agent; a blocked one stands out in amber. */
    private fun MaterialScope.agentButton(agent: AgentState): LayoutElement {
        val (word, color) = tileStatus(agent.status)
        val blocked = agent.status == "blocked"
        val colors = if (blocked) {
            ButtonColors(containerColor = TileAmberContainer, iconColor = TileAmber, labelColor = TileOnAmberContainer, secondaryLabelColor = TileAmber)
        } else {
            ButtonColors(containerColor = TileColors.surfaceContainer, iconColor = color, labelColor = TileColors.onSurface, secondaryLabelColor = color)
        }
        val secondary = listOf(word, agent.agent).filter { it.isNotBlank() }.joinToString(" · ")
        return button(
            onClick = clickable(launchInApp(MainActivity::class.java.name, agent.pane_id), id = "agent-${agent.pane_id}"),
            labelContent = { text(agent.label.ifBlank { agent.pane_id }.layoutString, maxLines = 1) },
            secondaryLabelContent = { text(secondary.layoutString, maxLines = 1) },
            width = expand(),
            colors = colors,
            // Two agents (name and status) must fit under the title on a 192 dp screen.
            style = ButtonStyle.smallButtonStyle()
        )
    }

    private fun MaterialScope.message(text: String): LayoutElement =
        text(text.layoutString, typography = Typography.BODY_MEDIUM, maxLines = 3)

    private companion object {
        const val MAX_AGENTS = 2
    }
}
