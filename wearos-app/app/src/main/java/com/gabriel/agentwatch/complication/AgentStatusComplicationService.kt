package com.gabriel.agentwatch.complication

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import android.util.Log
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.MonochromaticImage
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.SuspendingComplicationDataSourceService
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayClient
import kotlinx.coroutines.CancellationException

/** Most urgent agent status on the watch face: one `GET /v1/agents`, an icon and a count per state. */
class AgentStatusComplicationService : SuspendingComplicationDataSourceService() {

    companion object {
        private const val TAG = "Complication"
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData? {
        if (type != ComplicationType.SHORT_TEXT) return null
        return build(ComplicationContent(ComplicationKind.NEEDS_YOU, 1))
    }

    override suspend fun onComplicationRequest(request: ComplicationRequest): ComplicationData? {
        if (request.complicationType != ComplicationType.SHORT_TEXT) return null
        val prefs = Prefs(this)
        val result = if (prefs.isPaired) {
            try {
                RelayClient(prefs.relayUrl, prefs.deviceToken).agents()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Result.failure(e) // e.g. a malformed relay URL
            }
        } else {
            null
        }
        result?.exceptionOrNull()?.let { Log.w(TAG, "Complication fetch failed: ${it.message}") }
        return build(complicationContent(prefs.isPaired, result))
    }

    private fun build(content: ComplicationContent): ComplicationData {
        val n = content.count
        val (text, icon, description) = when (content.kind) {
            ComplicationKind.NOT_PAIRED -> Triple(getString(R.string.complication_pair), R.drawable.ic_link, getString(R.string.cd_complication_not_paired))
            ComplicationKind.UNREACHABLE -> Triple(getString(R.string.complication_unreachable), R.drawable.ic_cloud_off, getString(R.string.cd_complication_unreachable))
            ComplicationKind.MAC_OFFLINE -> Triple(getString(R.string.complication_mac_off), R.drawable.ic_computer, getString(R.string.cd_complication_mac_offline))
            ComplicationKind.NEEDS_YOU -> Triple("$n", R.drawable.ic_status_blocked, resources.getQuantityString(R.plurals.cd_complication_needs_you, n, n))
            ComplicationKind.DONE -> Triple("$n", R.drawable.ic_status_done, resources.getQuantityString(R.plurals.cd_complication_done, n, n))
            ComplicationKind.WORKING -> Triple("$n", R.drawable.ic_status_working, resources.getQuantityString(R.plurals.cd_complication_working, n, n))
            ComplicationKind.IDLE -> Triple("$n", R.drawable.ic_status_idle, resources.getQuantityString(R.plurals.cd_complication_idle, n, n))
            ComplicationKind.NO_AGENTS -> Triple("0", R.drawable.ic_status_idle, getString(R.string.cd_complication_no_agents))
        }
        val tap = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return ShortTextComplicationData.Builder(
            text = PlainComplicationText.Builder(text).build(),
            contentDescription = PlainComplicationText.Builder(description).build()
        )
            .setTapAction(tap)
            .setMonochromaticImage(MonochromaticImage.Builder(Icon.createWithResource(this, icon)).build())
            .build()
    }
}
