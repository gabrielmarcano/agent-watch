package com.gabriel.agentwatch.mobile.push

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.Person
import androidx.core.app.RemoteInput
import androidx.core.graphics.drawable.IconCompat
import com.gabriel.agentwatch.mobile.MainActivity
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.network.AgentsUpdate
import com.gabriel.agentwatch.network.NotificationButton
import com.gabriel.agentwatch.network.PushMessage
import com.gabriel.agentwatch.network.ShownApproval
import com.gabriel.agentwatch.network.approvalsToDismiss
import com.gabriel.agentwatch.network.blockedButtons
import com.gabriel.agentwatch.network.shouldDismissOnResolved
import com.gabriel.agentwatch.ui.logic.agentBrand
import com.gabriel.agentwatch.util.MarkdownFormatter

/**
 * The phone's notifications, built locally from data-only FCM pushes (contracts §4.1). They are
 * chat-style: one conversation per `(host, pane_id)`, grouped under a summary, and `localOnly` so
 * Android never bridges them to the watch (which gets its own push).
 *
 * Approving actions carry `setAuthenticationRequired(true)` (API 31+); the receiver also refuses
 * them while the phone is locked, so no build can approve without an unlock.
 */
object MobileNotifications {
    const val CHATS = "agent_chats"
    const val APPROVALS = "agent_approvals"
    const val FEEDBACK = "agent_feedback"

    private const val GROUP = "com.gabriel.agentwatch.mobile.agent_chats"
    private const val SUMMARY_ID = 9998
    private const val DIGEST_ID = 9999
    /** Feedback that must not replace a still-actionable chat (locked approval, empty reply). */
    private const val KEEP_CHAT_FEEDBACK_ID = 9997
    private const val TAG = "Notifications"

    /** Notification extras that tag what a shown notification is (read back via `activeNotifications`). */
    const val EXTRA_KIND = "aw_kind"
    const val EXTRA_PANE = "aw_pane"
    const val EXTRA_HOST = "aw_host"
    const val EXTRA_SEQ = "aw_seq"
    const val EXTRA_LABEL = "aw_label"
    const val KIND_APPROVAL = "approval"
    const val KIND_DONE = "done"
    const val KIND_DIGEST = "digest"
    const val KIND_FEEDBACK = "feedback"

    /** How long a success feedback notification stays before the system removes it. */
    private const val SUCCESS_FEEDBACK_MS = 3_000L

    fun ensure(context: Context) {
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        // A chat replies with a light tick; an approval gets a short double pulse (the app's own
        // taps are quieter still). The importance of a channel is fixed when it is created, so a
        // channel whose importance changed is replaced (the owner's own setting for it is lost).
        channel(context, manager, CHATS, R.string.notification_channel_chats, R.string.notification_channel_chats_description,
            NotificationManager.IMPORTANCE_DEFAULT, longArrayOf(0, 120))
        channel(context, manager, APPROVALS, R.string.notification_channel_approvals, R.string.notification_channel_approvals_description,
            NotificationManager.IMPORTANCE_HIGH, longArrayOf(0, 250, 120, 250), lights = true)
        channel(context, manager, FEEDBACK, R.string.notification_channel_feedback, R.string.notification_channel_feedback_description,
            NotificationManager.IMPORTANCE_LOW, null)
    }

    private fun channel(
        context: Context,
        manager: NotificationManager,
        id: String,
        nameRes: Int,
        descriptionRes: Int,
        importance: Int,
        vibration: LongArray?,
        lights: Boolean = false
    ) {
        val existing = manager.getNotificationChannel(id)
        if (existing != null && existing.importance != importance) manager.deleteNotificationChannel(id)
        manager.createNotificationChannel(
            NotificationChannel(id, context.getString(nameRes), importance).apply {
                description = context.getString(descriptionRes)
                if (vibration != null) {
                    enableVibration(true)
                    vibrationPattern = vibration
                }
                if (lights) enableLights(true)
            }
        )
    }

    /** A chat the owner muted or hid posts no notification (the phone's own, local one). */
    private fun muted(context: Context, agent: AgentKey): Boolean =
        com.gabriel.agentwatch.mobile.MobileApplication.chatState(context).shouldSuppressPush(agent)

    /** One chat per `(host, pane)`: a new push for it replaces the old notification. */
    fun idFor(agent: AgentKey): Int = agent.token.hashCode() and 0x7FFFFFFF

    /** The agent an intent names (its extras); null without a pane. A missing host is "" (pre-hosts intents). */
    fun agentOf(intent: Intent?): AgentKey? {
        val paneId = intent?.getStringExtra(EXTRA_PANE)?.takeIf { it.isNotBlank() } ?: return null
        return AgentKey(intent.getStringExtra(EXTRA_HOST).orEmpty(), paneId)
    }

    /** Opens the app on [agent]'s chat, or on the chat list for a digest or the summary. */
    fun openApp(context: Context, agent: AgentKey?): PendingIntent {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            if (agent != null) {
                putExtra(EXTRA_PANE, agent.paneId)
                putExtra(EXTRA_HOST, agent.host)
                data = Uri.parse(intentUri(agent, "open"))
            }
        }
        val requestCode = if (agent == null) SUMMARY_ID else requestCode(agent, 0)
        return PendingIntent.getActivity(
            context, requestCode, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }

    /** A button or delete broadcast for [agent], unique per action and option. */
    private fun receiverAction(
        context: Context,
        agent: AgentKey?,
        action: String,
        slot: Int,
        mutable: Boolean = false,
        optionId: String? = null,
        extras: Intent.() -> Unit
    ): PendingIntent {
        val intent = Intent(context, MobileNotificationActionReceiver::class.java).apply {
            this.action = action
            data = Uri.parse(
                if (agent == null) "agentwatch-mobile://notification/$action"
                else intentUri(agent, action, optionId)
            )
            if (agent != null) {
                putExtra(EXTRA_PANE, agent.paneId)
                putExtra(EXTRA_HOST, agent.host)
            }
            extras()
        }
        val mutability = if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE
        val requestCode = if (agent == null) SUMMARY_ID else requestCode(agent, slot)
        return PendingIntent.getBroadcast(
            context, requestCode, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or mutability
        )
    }

    private fun requestCode(agent: AgentKey, slot: Int): Int = idFor(agent) * 8 + slot

    private fun intentUri(agent: AgentKey, action: String, optionId: String? = null): String {
        val path = if (optionId.isNullOrBlank()) action else "$action/${Uri.encode(optionId)}"
        val base = "agentwatch-mobile://notification/$path/${Uri.encode(agent.paneId)}"
        return if (agent.host.isEmpty()) base else "$base?host=${Uri.encode(agent.host)}"
    }

    private fun extras(kind: String, agent: AgentKey?, seq: Long = 0L, label: String = ""): Bundle = Bundle().apply {
        putString(EXTRA_KIND, kind)
        if (agent != null) {
            putString(EXTRA_PANE, agent.paneId)
            putString(EXTRA_HOST, agent.host)
        }
        putLong(EXTRA_SEQ, seq)
        putString(EXTRA_LABEL, label)
    }

    private fun base(
        context: Context,
        kind: String,
        agent: AgentKey,
        seq: Long,
        label: String,
        smallIcon: Int,
        channel: String = CHATS
    ): NotificationCompat.Builder = NotificationCompat.Builder(context, channel)
        .setSmallIcon(smallIcon)
        .setAutoCancel(true)
        .setLocalOnly(true)
        .setGroup(GROUP)
        .setContentIntent(openApp(context, agent))
        .setDeleteIntent(
            receiverAction(context, agent, MobileNotificationActions.REFRESH, slot = 7) {}
        )
        .addExtras(extras(kind, agent, seq, label))

    private fun person(context: Context, label: String, agent: String): Person =
        Person.Builder()
            .setName(label.ifBlank { context.getString(R.string.app_name) })
            .setIcon(IconCompat.createWithResource(context, agentBrand(agent).icon))
            .setImportant(true)
            .build()

    fun showBlocked(context: Context, message: PushMessage.Blocked) {
        if (muted(context, message.key)) return
        ensure(context)
        val shown = person(context, message.label, message.agent)
        val style = NotificationCompat.MessagingStyle(
            Person.Builder().setName(context.getString(R.string.notification_you)).build()
        )
            .setConversationTitle(message.label)
            .addMessage(message.body, System.currentTimeMillis(), shown)

        val builder = base(
            context, KIND_APPROVAL, message.key, message.seq, message.label,
            com.gabriel.agentwatch.core.R.drawable.ic_agent_alert, channel = APPROVALS
        )
            .setStyle(style)
            .setContentTitle(message.label)
            .setContentText(message.body)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
        if (message.hostName.isNotBlank()) builder.setSubText(message.hostName)

        val isQuestion = message.kind == "question"
        for (button in blockedButtons(message)) {
            when (button.kind) {
                NotificationButton.Kind.OPEN -> builder.addAction(
                    com.gabriel.agentwatch.core.R.drawable.ic_agent,
                    context.getString(R.string.notification_open),
                    openApp(context, message.key)
                )
                NotificationButton.Kind.ANSWER -> {
                    val optionId = button.optionId.orEmpty()
                    val action = if (isQuestion) MobileNotificationActions.ANSWER
                    else MobileNotificationActions.ALLOW
                    val pending = receiverAction(
                        context, message.key, action, slot = 1,
                        optionId = optionId
                    ) {
                        putExtra("option_id", optionId)
                        putExtra("state_change_seq", message.seq)
                        putExtra("fingerprint", message.fingerprint)
                        putExtra("is_choice", isQuestion)
                        putExtra("notif_id", idFor(message.key))
                    }
                    val actionBuilder = NotificationCompat.Action.Builder(
                        com.gabriel.agentwatch.core.R.drawable.ic_agent_check,
                        button.label,
                        pending
                    )
                    // Approving actions need the phone unlocked (phase 7 decision): API 31+ asks the
                    // system, and the receiver refuses anyway while the keyguard is up.
                    actionBuilder.setAuthenticationRequired(true)
                    builder.addAction(actionBuilder.build())
                }
                NotificationButton.Kind.DENY -> {
                    val optionId = button.optionId.orEmpty()
                    val pending = receiverAction(
                        context, message.key, MobileNotificationActions.DENY, slot = 2,
                        optionId = optionId
                    ) {
                        putExtra("option_id", optionId)
                        putExtra("state_change_seq", message.seq)
                        putExtra("fingerprint", message.fingerprint)
                        putExtra("notif_id", idFor(message.key))
                    }
                    builder.addAction(
                        com.gabriel.agentwatch.core.R.drawable.ic_agent_close,
                        button.label,
                        pending
                    )
                }
                NotificationButton.Kind.CANCEL -> {
                    val pending = receiverAction(
                        context, message.key, MobileNotificationActions.CANCEL, slot = 3
                    ) {
                        putExtra("state_change_seq", message.seq)
                        putExtra("fingerprint", message.fingerprint)
                        putExtra("notif_id", idFor(message.key))
                    }
                    builder.addAction(
                        com.gabriel.agentwatch.core.R.drawable.ic_agent_close,
                        button.label,
                        pending
                    )
                }
            }
        }

        manager(context).notify(idFor(message.key), builder.build())
        refreshSummary(context)
    }

    fun showDone(context: Context, message: PushMessage.Done) {
        if (muted(context, message.key)) return
        ensure(context)
        val shown = person(context, message.label, message.agent)
        val style = NotificationCompat.MessagingStyle(
            Person.Builder().setName(context.getString(R.string.notification_you)).build()
        )
            .setConversationTitle(message.label)
            .addMessage(message.body, System.currentTimeMillis(), shown)

        val builder = base(
            context, KIND_DONE, message.key, message.seq, message.label,
            com.gabriel.agentwatch.core.R.drawable.ic_agent
        )
            .setStyle(style)
            .setContentTitle(message.label)
            .setContentText(message.body)
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
        if (message.hostName.isNotBlank()) builder.setSubText(message.hostName)

        val remoteInput = RemoteInput.Builder("KEY_TEXT_REPLY")
            .setLabel(context.getString(R.string.notification_reply_hint, message.label))
            .build()
        val replyPending = receiverAction(
            context, message.key, MobileNotificationActions.PROMPT, slot = 4, mutable = true
        ) {
            putExtra("state_change_seq", message.seq)
            putExtra("notif_id", idFor(message.key))
        }
        val replyAction = NotificationCompat.Action.Builder(
            com.gabriel.agentwatch.core.R.drawable.ic_agent,
            context.getString(R.string.notification_reply),
            replyPending
        ).addRemoteInput(remoteInput).build()
        builder.addAction(replyAction)
        builder.addAction(
            com.gabriel.agentwatch.core.R.drawable.ic_agent,
            context.getString(R.string.notification_open),
            openApp(context, message.key)
        )

        manager(context).notify(idFor(message.key), builder.build())
        refreshSummary(context)
    }

    fun showDigest(context: Context, message: PushMessage.Digest) {
        ensure(context)
        val builder = NotificationCompat.Builder(context, CHATS)
            .setSmallIcon(com.gabriel.agentwatch.core.R.drawable.ic_agent)
            .setContentTitle(message.title)
            .setContentText(MarkdownFormatter.clean(message.body))
            .setStyle(NotificationCompat.BigTextStyle().bigText(MarkdownFormatter.clean(message.body)))
            .setAutoCancel(true)
            .setLocalOnly(true)
            .setContentIntent(openApp(context, null))
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .addExtras(extras(KIND_DIGEST, null))
        manager(context).notify(DIGEST_ID, builder.build())
    }

    /** A data-only `resolved` push: dismiss a shown approval for the pane, unless it is newer. */
    fun onResolved(context: Context, message: PushMessage.Resolved) {
        try {
            val manager = manager(context)
            val approval = shown(manager).firstOrNull { it.key == message.key }
            if (shouldDismissOnResolved(approval, message.seq)) {
                manager.cancel(approval!!.notificationId)
                refreshSummary(context)
            }
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not dismiss a resolved approval: ${e.javaClass.simpleName}")
        }
    }

    /** Approval notifications made obsolete by what the app just learned from SSE. */
    fun dismissStale(context: Context, update: AgentsUpdate) {
        try {
            val manager = manager(context)
            val obsolete = approvalsToDismiss(shown(manager), update)
            if (obsolete.isEmpty()) return
            obsolete.forEach(manager::cancel)
            refreshSummary(context)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not dismiss stale approvals: ${e.javaClass.simpleName}")
        }
    }

    /** The chat was opened in the app: its notification has been seen. */
    fun cancel(context: Context, agent: AgentKey) {
        val manager = manager(context)
        manager.cancel(idFor(agent))
        manager.cancel(KEEP_CHAT_FEEDBACK_ID)
        refreshSummary(context)
    }

    /** The group summary was dismissed: take its chats with it. */
    fun cancelAllChats(context: Context) {
        try {
            val manager = manager(context)
            manager.activeNotifications.forEach { sbn ->
                val kind = sbn.notification.extras.getString(EXTRA_KIND)
                if (kind == KIND_APPROVAL || kind == KIND_DONE) manager.cancel(sbn.id)
            }
            manager.cancel(SUMMARY_ID)
            manager.cancel(KEEP_CHAT_FEEDBACK_ID)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not clear the chats: ${e.javaClass.simpleName}")
        }
    }

    /**
     * Silent result of an action taken on a notification. By default it replaces that chat's
     * notification (the action consumed it); [keepChat] leaves the chat in place, for a refusal that
     * did not consume the prompt (a locked phone, an empty reply) so it can be answered after unlocking.
     */
    fun showFeedback(
        context: Context,
        agent: AgentKey,
        message: String,
        isSuccess: Boolean,
        keepChat: Boolean = false
    ) {
        ensure(context)
        val open = openApp(context, agent)
        val builder = NotificationCompat.Builder(context, FEEDBACK)
            .setSmallIcon(com.gabriel.agentwatch.core.R.drawable.ic_agent)
            .setContentTitle(context.getString(R.string.app_name))
            .setContentText(message)
            .setAutoCancel(true)
            .setLocalOnly(true)
            .setSilent(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(open)
            .addExtras(extras(KIND_FEEDBACK, agent))
        if (isSuccess) {
            builder.setTimeoutAfter(SUCCESS_FEEDBACK_MS)
        } else {
            builder.addAction(
                com.gabriel.agentwatch.core.R.drawable.ic_agent,
                context.getString(R.string.notification_open),
                open
            )
        }
        val id = if (keepChat) KEEP_CHAT_FEEDBACK_ID else idFor(agent)
        manager(context).notify(id, builder.build())
        refreshSummary(context)
    }

    /** The approvals currently shown, as tagged in their extras. */
    private fun shown(manager: NotificationManager): List<ShownApproval> =
        manager.activeNotifications.mapNotNull { sbn ->
            val extras = sbn.notification.extras
            if (extras.getString(EXTRA_KIND) != KIND_APPROVAL) return@mapNotNull null
            val paneId = extras.getString(EXTRA_PANE) ?: return@mapNotNull null
            ShownApproval(sbn.id, paneId, extras.getLong(EXTRA_SEQ, 0L), extras.getString(EXTRA_HOST).orEmpty())
        }

    /**
     * One summary for the chat group, built from what is currently shown: WhatsApp-style, it says how
     * many conversations need attention and lists their names. Less than two chats: no summary.
     */
    fun refreshSummary(context: Context) {
        try {
            val manager = manager(context)
            val chats = manager.activeNotifications.filter { sbn ->
                val kind = sbn.notification.extras.getString(EXTRA_KIND)
                kind == KIND_APPROVAL || kind == KIND_DONE
            }
            if (chats.size < 2) {
                manager.cancel(SUMMARY_ID)
                return
            }
            val count = chats.size
            val text = context.resources.getQuantityString(R.plurals.notification_summary, count, count)
            val style = NotificationCompat.InboxStyle()
                .setBigContentTitle(context.getString(R.string.app_name))
                .setSummaryText(text)
            chats.take(5).forEach { sbn ->
                sbn.notification.extras.getString(EXTRA_LABEL)?.takeIf { it.isNotBlank() }?.let(style::addLine)
            }
            val summary = NotificationCompat.Builder(context, CHATS)
                .setSmallIcon(com.gabriel.agentwatch.core.R.drawable.ic_agent)
                .setContentTitle(context.getString(R.string.app_name))
                .setContentText(text)
                .setStyle(style)
                .setGroup(GROUP)
                .setGroupSummary(true)
                .setAutoCancel(true)
                .setLocalOnly(true)
                .setSilent(true)
                .setOnlyAlertOnce(true)
                .setContentIntent(openApp(context, null))
                .setDeleteIntent(
                    receiverAction(context, null, MobileNotificationActions.DISMISS_ALL, slot = 0) {}
                )
                .build()
            manager.notify(SUMMARY_ID, summary)
        } catch (e: RuntimeException) {
            Log.w(TAG, "Could not refresh the summary: ${e.javaClass.simpleName}")
        }
    }

    private fun manager(context: Context): NotificationManager =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
}
