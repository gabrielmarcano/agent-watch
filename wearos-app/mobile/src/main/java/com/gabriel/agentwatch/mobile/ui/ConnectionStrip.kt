package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.warningAccent
import com.gabriel.agentwatch.ui.logic.ListNotice

/**
 * The thin strip that says the phone is reconnecting, that the relay is gone, or that a machine is
 * offline. It sits under the top bar, where a messaging app puts its own connection line.
 */
@Composable
fun ConnectionStrip(notice: ListNotice) {
    val text = when (notice) {
        ListNotice.CONNECTING -> stringResource(R.string.notice_connecting)
        ListNotice.RELAY_UNREACHABLE -> stringResource(R.string.notice_relay_unreachable)
        ListNotice.MAC_OFFLINE -> stringResource(R.string.notice_device_offline)
        ListNotice.HERDR_STOPPED -> stringResource(R.string.notice_herdr_stopped)
    }
    val color = if (notice == ListNotice.CONNECTING) {
        MaterialTheme.colorScheme.onSurfaceVariant
    } else {
        warningAccent()
    }
    Box(
        Modifier
            .fillMaxWidth()
            .background(color.copy(alpha = 0.12f))
            .padding(horizontal = 16.dp, vertical = 6.dp)
    ) {
        Text(text, color = color, style = MaterialTheme.typography.labelLarge)
    }
}
