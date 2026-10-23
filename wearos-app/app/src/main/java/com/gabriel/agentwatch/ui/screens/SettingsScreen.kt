package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material3.AlertDialog
import androidx.wear.compose.material3.AlertDialogDefaults
import androidx.wear.compose.material3.ButtonDefaults
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.ListHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.OutlinedButton
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.BuildConfig
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.logic.displayHost
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.Red
import com.gabriel.agentwatch.update.UpdatePhase
import com.gabriel.agentwatch.update.UpdateState

/**
 * The pairing (relay and state), the update check, pair again, unpair, version. Unpairing flips
 * `auth`, and the app shows pairing.
 */
@Composable
fun SettingsScreen(
    update: UpdateState,
    onPairAgain: () -> Unit,
    onCheckForUpdates: () -> Unit
) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    var confirmUnpair by remember { mutableStateOf(false) }

    ScreenList { spec ->
        item(key = "title") {
            ListHeader(
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec)
            ) { Text(stringResource(R.string.settings)) }
        }
        item(key = "relay") {
            FilledTonalButton(
                onClick = onPairAgain,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_link, null, MaterialTheme.colorScheme.onSurface) },
                secondaryLabel = { Text(displayHost(prefs.relayUrl), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                label = { Text(stringResource(R.string.pair_again)) }
            )
        }
        item(key = "updates") {
            FilledTonalButton(
                onClick = onCheckForUpdates,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                enabled = update.phase !in UPDATE_CHECK_BUSY,
                icon = { ResIcon(R.drawable.ic_sync, null, MaterialTheme.colorScheme.onSurface) },
                secondaryLabel = { Text(updateStatus(update), maxLines = 2, overflow = TextOverflow.Ellipsis) },
                label = { Text(stringResource(R.string.update_check)) }
            )
        }
        item(key = "unpair") {
            OutlinedButton(
                onClick = { confirmUnpair = true },
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                colors = ButtonDefaults.outlinedButtonColors(contentColor = Red, iconColor = Red),
                icon = { ResIcon(R.drawable.ic_close, null, Red) },
                label = { Text(stringResource(R.string.unpair)) }
            )
        }
        item(key = "version") {
            Text(
                stringResource(R.string.version, BuildConfig.VERSION_NAME),
                modifier = transformedItem(spec).padding(top = 8.dp),
                color = OnSurfaceVariant,
                style = MaterialTheme.typography.bodySmall,
                textAlign = TextAlign.Center
            )
        }
    }

    AlertDialog(
        visible = confirmUnpair,
        onDismissRequest = { confirmUnpair = false },
        confirmButton = {
            AlertDialogDefaults.ConfirmButton(onClick = {
                confirmUnpair = false
                RelayRepository.clearPairing(context)
                RelayRepository.restart(context)
            })
        },
        title = { Text(stringResource(R.string.unpair_confirm_title)) }
    )
}

private val UPDATE_CHECK_BUSY = setOf(
    UpdatePhase.CHECKING,
    UpdatePhase.DOWNLOADING,
    UpdatePhase.READY,
    UpdatePhase.NEEDS_PERMISSION
)

@Composable
private fun updateStatus(update: UpdateState): String {
    val info = update.info
    val version = info?.version_name?.takeIf { it.isNotBlank() } ?: info?.version_code?.toString().orEmpty()
    return when (update.phase) {
        UpdatePhase.CHECKING -> stringResource(R.string.update_checking)
        UpdatePhase.UP_TO_DATE -> stringResource(R.string.update_up_to_date, version.ifBlank { BuildConfig.VERSION_NAME })
        UpdatePhase.AVAILABLE -> stringResource(R.string.update_available, version)
        UpdatePhase.DOWNLOADING -> stringResource(R.string.update_downloading, (update.progress * 100).toInt())
        UpdatePhase.READY, UpdatePhase.NEEDS_PERMISSION -> stringResource(R.string.update_ready_short)
        UpdatePhase.FAILED -> stringResource(R.string.update_failed)
        else -> stringResource(R.string.version, BuildConfig.VERSION_NAME)
    }
}
