package com.gabriel.agentwatch.tile

import android.content.Context
import androidx.concurrent.futures.CallbackToFutureAdapter
import androidx.wear.protolayout.ActionBuilders
import androidx.wear.protolayout.DeviceParametersBuilders
import androidx.wear.protolayout.DimensionBuilders.dp
import androidx.wear.protolayout.LayoutElementBuilders
import androidx.wear.protolayout.ModifiersBuilders
import androidx.wear.protolayout.ResourceBuilders
import androidx.wear.protolayout.TimelineBuilders
import androidx.wear.tiles.RequestBuilders
import androidx.wear.tiles.TileBuilders
import androidx.wear.tiles.TileService
import com.google.common.util.concurrent.ListenableFuture

class AgentQuickActionTileService : TileService() {
    private val RESOURCES_VERSION = "1"

    override fun onTileRequest(requestParams: RequestBuilders.TileRequest): ListenableFuture<TileBuilders.Tile> {
        val intentAction = ActionBuilders.LaunchAction.Builder()
            .setAndroidActivity(
                ActionBuilders.AndroidActivity.Builder()
                    .setPackageName("com.gabriel.agentwatch")
                    .setClassName("com.gabriel.agentwatch.tile.QuickDictateActivity")
                    .build()
            ).build()

        val buttonElement = LayoutElementBuilders.Box.Builder()
            .setWidth(dp(72f))
            .setHeight(dp(72f))
            .setModifiers(
                ModifiersBuilders.Modifiers.Builder()
                    .setClickable(
                        ModifiersBuilders.Clickable.Builder()
                            .setId("dictate")
                            .setOnClick(intentAction)
                            .build()
                    )
                    .setBackground(
                        ModifiersBuilders.Background.Builder()
                            .setColor(androidx.wear.protolayout.ColorBuilders.argb(0xFF1976D2.toInt()))
                            .setCorner(
                                ModifiersBuilders.Corner.Builder()
                                    .setRadius(dp(36f))
                                    .build()
                            )
                            .build()
                    )
                    .build()
            )
            .addContent(
                LayoutElementBuilders.Text.Builder()
                    .setText("🎤")
                    .setFontStyle(
                        LayoutElementBuilders.FontStyle.Builder()
                            .setSize(androidx.wear.protolayout.DimensionBuilders.sp(24f))
                            .build()
                    )
                    .build()
            )
            .build()

        val column = LayoutElementBuilders.Column.Builder()
            .addContent(buttonElement)
            .addContent(
                LayoutElementBuilders.Spacer.Builder()
                    .setHeight(dp(12f))
                    .build()
            )
            .addContent(
                LayoutElementBuilders.Text.Builder()
                    .setText("Dictate to Agent")
                    .setFontStyle(
                        LayoutElementBuilders.FontStyle.Builder()
                            .setSize(androidx.wear.protolayout.DimensionBuilders.sp(14f))
                            .setColor(androidx.wear.protolayout.ColorBuilders.argb(0xFFFFFFFF.toInt()))
                            .build()
                    )
                    .build()
            )
            .build()

        val rootBox = LayoutElementBuilders.Box.Builder()
            .setWidth(androidx.wear.protolayout.DimensionBuilders.expand())
            .setHeight(androidx.wear.protolayout.DimensionBuilders.expand())
            .setHorizontalAlignment(LayoutElementBuilders.HORIZONTAL_ALIGN_CENTER)
            .setVerticalAlignment(LayoutElementBuilders.VERTICAL_ALIGN_CENTER)
            .addContent(column)
            .build()

        val timeline = TimelineBuilders.Timeline.Builder()
            .addTimelineEntry(
                TimelineBuilders.TimelineEntry.Builder()
                    .setLayout(
                        LayoutElementBuilders.Layout.Builder()
                            .setRoot(rootBox)
                            .build()
                    ).build()
            ).build()

        val tile = TileBuilders.Tile.Builder()
            .setResourcesVersion(RESOURCES_VERSION)
            .setTileTimeline(timeline)
            .build()

        return CallbackToFutureAdapter.getFuture { completer ->
            completer.set(tile)
            "onTileRequest"
        }
    }

    override fun onTileResourcesRequest(requestParams: RequestBuilders.ResourcesRequest): ListenableFuture<ResourceBuilders.Resources> {
        val resources = ResourceBuilders.Resources.Builder()
            .setVersion(RESOURCES_VERSION)
            .build()
        return CallbackToFutureAdapter.getFuture { completer ->
            completer.set(resources)
            "onTileResourcesRequest"
        }
    }
}
