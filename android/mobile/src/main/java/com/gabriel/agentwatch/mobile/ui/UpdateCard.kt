package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.update.UpdatePhase
import com.gabriel.agentwatch.update.UpdateState

/**
 * The card that offers a newer build of the app: it lives in the settings now, where the update
 * check is, instead of over the chat list.
 */
@Composable
fun UpdateCard(
    update: UpdateState,
    onUpdate: () -> Unit,
    onRetry: () -> Unit,
    onCancel: () -> Unit,
    onInstall: () -> Unit,
    onDismiss: () -> Unit
) {
    if (update.phase == UpdatePhase.IDLE || update.phase == UpdatePhase.CHECKING || update.phase == UpdatePhase.UP_TO_DATE) {
        return
    }
    val info = update.info
    val version = info?.version_name?.takeIf { it.isNotBlank() } ?: info?.version_code?.toString().orEmpty()

    Card(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
        Column(
            modifier = Modifier.padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp)
        ) {
            Text(stringResource(R.string.update_title), style = MaterialTheme.typography.titleSmall)
            when (update.phase) {
                UpdatePhase.AVAILABLE -> Text(stringResource(R.string.update_available, version))
                UpdatePhase.DOWNLOADING -> {
                    Text(stringResource(R.string.update_downloading, (update.progress * 100).toInt()))
                    LinearProgressIndicator(
                        progress = { update.progress.coerceIn(0f, 1f) },
                        modifier = Modifier.fillMaxWidth()
                    )
                }
                UpdatePhase.READY -> Text(stringResource(R.string.update_ready, version))
                UpdatePhase.NEEDS_PERMISSION -> Text(stringResource(R.string.update_permission))
                UpdatePhase.FAILED -> Text(updateErrorText(update.errorCode))
                else -> Unit
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                when (update.phase) {
                    UpdatePhase.AVAILABLE -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        TextButton(onClick = onUpdate) { Text(stringResource(R.string.update_action)) }
                    }
                    UpdatePhase.DOWNLOADING -> {
                        TextButton(onClick = onCancel) { Text(stringResource(R.string.update_cancel)) }
                    }
                    UpdatePhase.READY -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        TextButton(onClick = onInstall) { Text(stringResource(R.string.update_install)) }
                    }
                    UpdatePhase.NEEDS_PERMISSION -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        TextButton(onClick = onInstall) { Text(stringResource(R.string.update_open_settings)) }
                    }
                    UpdatePhase.FAILED -> {
                        TextButton(onClick = onDismiss) { Text(stringResource(R.string.update_later)) }
                        TextButton(onClick = onRetry) { Text(stringResource(R.string.update_retry)) }
                    }
                    else -> Unit
                }
            }
        }
    }
}

@Composable
private fun updateErrorText(code: String?): String = when (code) {
    "bad_download", "bad_update" -> stringResource(R.string.update_error_download)
    "install_unavailable", "install_settings_unavailable" -> stringResource(R.string.update_error_install)
    "not_paired", "unauthorized" -> stringResource(R.string.update_error_auth)
    "network", "timeout" -> stringResource(R.string.update_error_network)
    else -> stringResource(R.string.update_error_generic)
}
