package com.gabriel.agentwatch.complication

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import android.util.Log
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.LongTextComplicationData
import androidx.wear.watchface.complications.data.MonochromaticImage
import androidx.wear.watchface.complications.data.MonochromaticImageComplicationData
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.SuspendingComplicationDataSourceService
import com.gabriel.agentwatch.MainActivity
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayClient
import kotlinx.coroutines.CancellationException

/**
 * The most urgent agent on the watch face, from one `GET /v1/agents`: short text (count + status),
 * long text (the agent and its status) or the glyph alone. A tap opens that agent (the first blocked
 * one, or the latest to finish), otherwise the list.
 */
class AgentStatusComplicationService : SuspendingComplicationDataSourceService() {

    companion object {
        private const val TAG = "Complication"
        private val SUPPORTED = setOf(ComplicationType.SHORT_TEXT, ComplicationType.LONG_TEXT, ComplicationType.MONOCHROMATIC_IMAGE)
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData? {
        if (type !in SUPPORTED) return null
        return build(type, ComplicationContent(ComplicationKind.NEEDS_YOU, 1, null, getString(R.string.app_name), "claude"))
    }

    override suspend fun onComplicationRequest(request: ComplicationRequest): ComplicationData? {
        if (request.complicationType !in SUPPORTED) return null
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
        return build(request.complicationType, complicationContent(prefs.isPaired, result))
    }

    /** What each complication type shows for one state. */
    private data class Texts(
        val short: String,
        val title: String?,
        val longTitle: String,
        val longText: String,
        val icon: Int,
        val description: String
    )

    private fun texts(c: ComplicationContent): Texts {
        val n = c.count
        val app = getString(R.string.app_name)
        // "Blocked · claude" for one agent, "Blocked · +1 more" for several.
        fun status(word: Int) = if (n > 1) {
            getString(R.string.complication_status_more, getString(word), n - 1)
        } else {
            getString(R.string.complication_status_agent, getString(word), c.agent.orEmpty().ifBlank { app })
        }
        fun plural(id: Int) = resources.getQuantityString(id, n, n)
        return when (c.kind) {
            ComplicationKind.NOT_PAIRED -> Texts(
                getString(R.string.complication_pair), null, app, getString(R.string.dictation_not_paired),
                R.drawable.ic_link, getString(R.string.cd_complication_not_paired)
            )
            ComplicationKind.UNREACHABLE -> Texts(
                getString(R.string.complication_off), getString(R.string.complication_relay), app, getString(R.string.notice_relay_unreachable),
                R.drawable.ic_cloud_off, getString(R.string.cd_complication_unreachable)
            )
            ComplicationKind.DEVICE_OFFLINE -> Texts(
                getString(R.string.complication_off), getString(R.string.complication_device), app, getString(R.string.notice_device_offline),
                R.drawable.ic_computer, getString(R.string.cd_complication_device_offline)
            )
            ComplicationKind.NEEDS_YOU -> Texts(
                "$n", getString(R.string.status_blocked), c.label ?: app, status(R.string.status_blocked),
                R.drawable.ic_agent_alert, plural(R.plurals.cd_complication_needs_you)
            )
            ComplicationKind.DONE -> Texts(
                "$n", getString(R.string.status_done), c.label ?: app, status(R.string.status_done),
                R.drawable.ic_agent, plural(R.plurals.cd_complication_done)
            )
            ComplicationKind.WORKING -> Texts(
                "$n", getString(R.string.status_working), app, plural(R.plurals.complication_working),
                R.drawable.ic_agent, plural(R.plurals.cd_complication_working)
            )
            ComplicationKind.IDLE -> Texts(
                "$n", getString(R.string.status_idle), app, plural(R.plurals.complication_idle),
                R.drawable.ic_agent, plural(R.plurals.cd_complication_idle)
            )
            ComplicationKind.NO_AGENTS -> Texts(
                "0", getString(R.string.complication_agents), app, getString(R.string.no_agents),
                R.drawable.ic_agent, getString(R.string.cd_complication_no_agents)
            )
        }
    }

    private fun build(type: ComplicationType, content: ComplicationContent): ComplicationData {
        val t = texts(content)
        val image = MonochromaticImage.Builder(Icon.createWithResource(this, t.icon)).build()
        val description = PlainComplicationText.Builder(t.description).build()
        val tap = tapAction(content.paneId)
        return when (type) {
            ComplicationType.LONG_TEXT -> LongTextComplicationData.Builder(PlainComplicationText.Builder(t.longText).build(), description)
                .setTitle(PlainComplicationText.Builder(t.longTitle).build())
                .setMonochromaticImage(image)
                .setTapAction(tap)
                .build()
            ComplicationType.MONOCHROMATIC_IMAGE -> MonochromaticImageComplicationData.Builder(image, description)
                .setTapAction(tap)
                .build()
            else -> ShortTextComplicationData.Builder(PlainComplicationText.Builder(t.short).build(), description)
                .apply { t.title?.let { setTitle(PlainComplicationText.Builder(it).build()) } }
                .setMonochromaticImage(image)
                .setTapAction(tap)
                .build()
        }
    }

    /** Opens [paneId]'s screen (its prompt or last reply) when there is one, else the list. */
    private fun tapAction(paneId: String?): PendingIntent {
        val intent = Intent(this, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
        if (!paneId.isNullOrBlank()) intent.putExtra("pane_id", paneId)
        return PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
    }
}
