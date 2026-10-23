package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.DoneAll
import androidx.compose.material.icons.filled.MarkEmailUnread
import androidx.compose.material.icons.filled.NotificationsOff
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.MobileApplication
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.key

/**
 * The options a long press on a chat offers, WhatsApp-style: a sheet over the list with pin, mute,
 * mark as read and hide.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatOptionsSheet(
    agent: AgentState,
    chatStore: ChatStore? = null,
    chatState: MobileChatState? = null,
    pinned: Boolean,
    muted: Boolean,
    onDismiss: () -> Unit,
    onMarkRead: () -> Unit,
    onMarkUnread: () -> Unit
) {
    val context = LocalContext.current
    val actualChatState = chatState ?: MobileApplication.chatState(context)
    val token = agent.key.token
    val name = agent.label.ifBlank { agent.agent }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().padding(bottom = 24.dp)) {
            Text(
                text = name,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp)
            )
            SheetAction(
                label = stringResource(if (pinned) R.string.chat_unpin else R.string.chat_pin),
                icon = Icons.Filled.PushPin,
                onClick = {
                    if (pinned) actualChatState.unpin(token) else actualChatState.pin(token, name)
                    onDismiss()
                }
            )
            SheetAction(
                label = stringResource(if (muted) R.string.chat_unmute else R.string.chat_mute),
                icon = Icons.Filled.NotificationsOff,
                onClick = {
                    if (muted) actualChatState.unmute(token) else actualChatState.mute(token, name)
                    onDismiss()
                }
            )
            SheetAction(
                label = stringResource(R.string.mark_read),
                icon = Icons.Filled.DoneAll,
                onClick = onMarkRead
            )
            SheetAction(
                label = stringResource(R.string.chat_mark_unread),
                icon = Icons.Filled.MarkEmailUnread,
                onClick = {
                    onMarkUnread()
                    onDismiss()
                }
            )
            SheetAction(
                label = stringResource(R.string.chat_archive),
                icon = Icons.Filled.Archive,
                onClick = {
                    actualChatState.hide(token, name)
                    onDismiss()
                }
            )
        }
    }
}

/** One option in the sheet: its icon and its name, like the ones a messaging app shows. */
@Composable
private fun SheetAction(label: String, icon: ImageVector, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onSurface)
        Spacer(Modifier.width(20.dp))
        Text(label, style = MaterialTheme.typography.bodyLarge)
    }
}
