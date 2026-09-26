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
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.Red

/** The pairing (relay and state), pair again, unpair, version. Unpairing flips `auth`, and the app shows pairing. */
@Composable
fun SettingsScreen(onPairAgain: () -> Unit) {
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
                secondaryLabel = { Text(prefs.relayUrl, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                label = { Text(stringResource(R.string.pair_again)) }
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
                prefs.clearAuth()
                RelayRepository.restart(context)
            })
        },
        title = { Text(stringResource(R.string.unpair_confirm_title)) },
        text = { Text(stringResource(R.string.unpair_confirm_text), textAlign = TextAlign.Center) }
    )
}
